//! Request-owned processes. Cancellation drops this owner, not the WebSocket.
use std::process::{Output, Stdio};
use tokio::io::AsyncReadExt;
use tokio::process::{Child, Command};
use tokio::sync::watch;

struct ProcessOwner {
    child: Child,
    pid: u32,
    finished: bool,
}

impl Drop for ProcessOwner {
    fn drop(&mut self) {
        if !self.finished && self.pid != 0 {
            // The parent is not reaped until both pipes finish, so this ID
            // cannot have been recycled while a descendant keeps a pipe open.
            unsafe {
                libc::killpg(self.pid as libc::pid_t, libc::SIGKILL);
            }
            let _ = self.child.start_kill();
        }
    }
}

pub(super) async fn execute(command: &str) -> std::io::Result<Output> {
    execute_with_cancel(command, None, std::time::Duration::from_secs(300)).await
}

pub(super) async fn execute_with_cancel(
    command: &str,
    cancelled: Option<&mut watch::Receiver<bool>>,
    timeout: std::time::Duration,
) -> std::io::Result<Output> {
    let child = Command::new("sh")
        .arg("-c")
        .arg(command)
        .process_group(0)
        .kill_on_drop(true)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()?;
    let mut owner = ProcessOwner {
        pid: child.id().unwrap_or(0),
        child,
        finished: false,
    };
    let mut stdout = owner.child.stdout.take().unwrap();
    let mut stderr = owner.child.stderr.take().unwrap();
    let mut out = Vec::new();
    let mut err = Vec::new();
    let deadline = tokio::time::sleep(timeout);
    tokio::pin!(deadline);
    let mut cancelled = cancelled;
    let drain_exit = if let Some(cancelled) = cancelled.as_deref_mut() {
        tokio::select! {
            result = async { tokio::try_join!(stdout.read_to_end(&mut out), stderr.read_to_end(&mut err)) } => { result?; None },
            _ = wait_cancelled(cancelled) => Some(std::io::ErrorKind::Interrupted),
            _ = &mut deadline => Some(std::io::ErrorKind::TimedOut),
        }
    } else {
        tokio::select! {
            result = async { tokio::try_join!(stdout.read_to_end(&mut out), stderr.read_to_end(&mut err)) } => { result?; None },
            _ = &mut deadline => Some(std::io::ErrorKind::TimedOut),
        }
    };
    if let Some(kind) = drain_exit {
        stop_and_reap(&mut owner).await?;
        let message = if kind == std::io::ErrorKind::Interrupted {
            "command cancelled"
        } else {
            "command execution timed out"
        };
        return Err(std::io::Error::new(kind, message));
    }

    let status = if let Some(cancelled) = cancelled.as_deref_mut() {
        tokio::select! {
            status = owner.child.wait() => status?,
            _ = wait_cancelled(cancelled) => {
                stop_and_reap(&mut owner).await?;
                return Err(std::io::Error::new(std::io::ErrorKind::Interrupted, "command cancelled"));
            }
            _ = &mut deadline => {
                stop_and_reap(&mut owner).await?;
                return Err(std::io::Error::new(std::io::ErrorKind::TimedOut, "command execution timed out"));
            }
        }
    } else {
        tokio::select! {
            status = owner.child.wait() => status?,
            _ = &mut deadline => {
                stop_and_reap(&mut owner).await?;
                return Err(std::io::Error::new(std::io::ErrorKind::TimedOut, "command execution timed out"));
            }
        }
    };
    owner.finished = true;
    Ok(Output {
        status,
        stdout: out,
        stderr: err,
    })
}

async fn wait_cancelled(cancelled: &mut watch::Receiver<bool>) {
    if !*cancelled.borrow_and_update() {
        let _ = cancelled.changed().await;
    }
}

async fn stop_and_reap(owner: &mut ProcessOwner) -> std::io::Result<()> {
    if owner.pid != 0 {
        unsafe {
            libc::killpg(owner.pid as libc::pid_t, libc::SIGKILL);
        }
    }
    owner.child.kill().await?;
    let _ = owner.child.wait().await?;
    owner.finished = true;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[tokio::test]
    async fn timeout_with_descendant_holding_pipes_is_bounded() {
        let result =
            tokio::time::timeout(Duration::from_millis(80), execute("sleep 20 & wait")).await;
        assert!(result.is_err());
        let output = tokio::time::timeout(Duration::from_secs(1), execute("printf independent"))
            .await
            .unwrap()
            .unwrap();
        assert_eq!(output.stdout, b"independent");
    }

    #[tokio::test]
    async fn normal_output_includes_both_streams() {
        let result = execute("printf tail; printf error-tail >&2").await.unwrap();
        assert!(result.status.success());
        assert_eq!(result.stdout, b"tail");
        assert_eq!(result.stderr, b"error-tail");
    }

    #[tokio::test]
    async fn cancellation_kills_the_process_group_before_returning() {
        let (cancel, mut cancelled) = watch::channel(false);
        let task = tokio::spawn(async move {
            execute_with_cancel(
                "sleep 20 & wait",
                Some(&mut cancelled),
                Duration::from_secs(30),
            )
            .await
        });
        tokio::time::sleep(Duration::from_millis(80)).await;
        cancel.send(true).unwrap();

        let result = tokio::time::timeout(Duration::from_secs(2), task)
            .await
            .expect("cancellation did not reap the process group")
            .unwrap();
        assert_eq!(result.unwrap_err().kind(), std::io::ErrorKind::Interrupted);

        let output = tokio::time::timeout(Duration::from_secs(1), execute("printf ready"))
            .await
            .unwrap()
            .unwrap();
        assert_eq!(output.stdout, b"ready");
    }
}
