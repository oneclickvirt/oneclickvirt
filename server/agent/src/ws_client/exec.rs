//! Request-owned processes. Cancellation drops this owner, not the WebSocket.
use std::process::{Output, Stdio};
use tokio::io::AsyncReadExt;
use tokio::process::{Child, Command};

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
    // Drain both streams concurrently and before reaping the group leader.
    tokio::try_join!(stdout.read_to_end(&mut out), stderr.read_to_end(&mut err))?;
    let status = owner.child.wait().await?;
    owner.finished = true;
    Ok(Output {
        status,
        stdout: out,
        stderr: err,
    })
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
}
