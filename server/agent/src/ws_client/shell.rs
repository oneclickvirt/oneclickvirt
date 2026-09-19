// PTY-based shell session management for the agent WebSocket client.
use std::collections::HashMap;
use std::os::unix::io::{AsRawFd, FromRawFd, OwnedFd};
use std::process::Stdio;
use std::sync::Arc;
use std::time::Duration;
use tokio::io::unix::AsyncFd;
use tokio::process::Command;
use tokio::sync::{Mutex, OwnedSemaphorePermit, mpsc, watch};
use tokio_tungstenite::tungstenite::Message;

use super::types::{ShellHandle, ShellOpenPayload};
use crate::tunnel::WsFrame;

/// Find the best available interactive shell (bash preferred, then zsh, fish, sh as fallback).
pub(super) fn find_best_shell() -> Option<String> {
    let candidates = [
        "/usr/bin/bash",
        "/bin/bash",
        "/usr/bin/zsh",
        "/bin/zsh",
        "/usr/bin/fish",
        "/usr/local/bin/fish",
        "/bin/sh",
        "/usr/bin/sh",
    ];
    candidates
        .iter()
        .find(|p| std::path::Path::new(p).exists())
        .map(|s| s.to_string())
}

// Linux's ptsname uses shared storage. Different Provider connections can open
// PTYs concurrently, so each lookup must own its buffer until open completes.
#[cfg(any(target_os = "linux", target_os = "android"))]
fn pty_slave_name(master_fd: i32) -> Result<std::ffi::CString, String> {
    let mut buffer = [0 as libc::c_char; 4096];
    let status = unsafe { libc::ptsname_r(master_fd, buffer.as_mut_ptr(), buffer.len()) };
    if status != 0 {
        return Err(format!(
            "ptsname_r: {}",
            std::io::Error::from_raw_os_error(status)
        ));
    }
    Ok(unsafe { std::ffi::CStr::from_ptr(buffer.as_ptr()) }.to_owned())
}

#[cfg(not(any(target_os = "linux", target_os = "android")))]
fn pty_slave_name(master_fd: i32) -> Result<std::ffi::CString, String> {
    // macOS has no ptsname_r. Serialize the legacy call and copy its result
    // while locked; do not keep a pointer into libc storage after unlocking.
    static NAME_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());
    let _guard = NAME_LOCK.lock().map_err(|_| "PTY name lock poisoned")?;
    let name = unsafe { libc::ptsname(master_fd) };
    if name.is_null() {
        return Err(format!("ptsname: {}", std::io::Error::last_os_error()));
    }
    Ok(unsafe { std::ffi::CStr::from_ptr(name) }.to_owned())
}

/// Both ends are owned and close on exec. Failed setup must not retain a raw
/// slave descriptor or let an unrelated concurrently spawned command inherit it.
pub(super) fn open_pty(cols: u16, rows: u16) -> Result<(OwnedFd, OwnedFd), String> {
    // PTY allocation can transiently return ENXIO/EAGAIN when several Agent
    // sessions open terminals at once (notably on macOS and constrained
    // containers). Retry only those resource-contention errors; all other
    // failures remain immediate and actionable.
    let mut master_fd = -1;
    for attempt in 0..100 {
        master_fd = unsafe { libc::posix_openpt(libc::O_RDWR | libc::O_NOCTTY | libc::O_CLOEXEC) };
        if master_fd >= 0 {
            break;
        }
        let errno = std::io::Error::last_os_error().raw_os_error();
        // Some libc implementations expose a negative errno from the PTY
        // allocation wrapper (notably Darwin's posix_openpt path). Treat both
        // signs as the same transient resource condition; otherwise a burst
        // of concurrent shells can fail nondeterministically despite the
        // bounded retry budget.
        let retryable = errno.is_some_and(|value| {
            let value = value.abs();
            value == libc::ENXIO || value == libc::EAGAIN || value == libc::ENOMEM
        });
        if !retryable || attempt == 99 {
            break;
        }
        std::thread::sleep(Duration::from_millis(1));
    }
    if master_fd < 0 {
        return Err(format!("posix_openpt: {}", std::io::Error::last_os_error()));
    }
    let master = unsafe { OwnedFd::from_raw_fd(master_fd) };
    if unsafe { libc::grantpt(master_fd) } < 0 {
        return Err(format!("grantpt: {}", std::io::Error::last_os_error()));
    }
    if unsafe { libc::unlockpt(master_fd) } < 0 {
        return Err(format!("unlockpt: {}", std::io::Error::last_os_error()));
    }
    let slave_name = pty_slave_name(master_fd)?;
    let slave_fd = unsafe {
        libc::open(
            slave_name.as_ptr(),
            libc::O_RDWR | libc::O_NOCTTY | libc::O_CLOEXEC,
        )
    };
    if slave_fd < 0 {
        return Err(format!(
            "open slave PTY: {}",
            std::io::Error::last_os_error()
        ));
    }
    let slave = unsafe { OwnedFd::from_raw_fd(slave_fd) };
    // Configure the opened slave: macOS rejects TIOCSWINSZ on a master before
    // its slave is open. Do not silently ignore an initial-size failure.
    let ws = libc::winsize {
        ws_row: rows,
        ws_col: cols,
        ws_xpixel: 0,
        ws_ypixel: 0,
    };
    if unsafe { libc::ioctl(slave_fd, libc::TIOCSWINSZ, &ws) } < 0 {
        return Err(format!(
            "set PTY dimensions: {}",
            std::io::Error::last_os_error()
        ));
    }
    // Set O_NONBLOCK on master so tokio's AsyncFd can poll it without blocking.
    let flags = unsafe { libc::fcntl(master_fd, libc::F_GETFL) };
    if flags < 0 || unsafe { libc::fcntl(master_fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0 {
        return Err(format!(
            "set PTY nonblocking: {}",
            std::io::Error::last_os_error()
        ));
    }
    Ok((master, slave))
}

/// Resize the PTY window and deliver SIGWINCH to the shell's process group.
pub(super) fn pty_resize(master_fd: i32, _child_pid: u32, cols: u16, rows: u16) {
    unsafe {
        let ws = libc::winsize {
            ws_row: rows,
            ws_col: cols,
            ws_xpixel: 0,
            ws_ypixel: 0,
        };
        libc::ioctl(master_fd, libc::TIOCSWINSZ, &ws);
        // TIOCSWINSZ signals the PTY foreground group itself.
    }
}

/// Kill the shell's entire process group (ensures background jobs also die).
pub(super) fn pty_kill(child_pid: u32) {
    if child_pid == 0 {
        return;
    }
    unsafe {
        // Our children call setsid(): their process group ID is their PID.
        // Never resolve a recycled PID into an unrelated process group.
        libc::killpg(child_pid as libc::pid_t, libc::SIGKILL);
    }
}

// PTY read boundaries need not coincide with UTF-8 character boundaries.
fn decode_pty_chunk(pending: &mut Vec<u8>, data: &[u8], eof: bool) -> String {
    pending.extend_from_slice(data);
    let mut output = String::new();
    loop {
        match std::str::from_utf8(pending) {
            Ok(text) => {
                output.push_str(text);
                pending.clear();
                break;
            }
            Err(error) => {
                let valid = error.valid_up_to();
                output.push_str(std::str::from_utf8(&pending[..valid]).unwrap());
                pending.drain(..valid);
                match error.error_len() {
                    Some(len) => {
                        output.push('\u{fffd}');
                        pending.drain(..len);
                    }
                    None => {
                        if eof && !pending.is_empty() {
                            output.push('\u{fffd}');
                            pending.clear();
                        }
                        break;
                    }
                }
            }
        }
    }
    output
}

async fn send_shell_output(tx: &mpsc::Sender<Message>, id: &str, data: String) -> bool {
    if data.is_empty() {
        return true;
    }
    let frame = serde_json::json!({"type":"shell_data", "id":id, "payload":{"data":data}});
    matches!(
        tokio::time::timeout(
            Duration::from_secs(3),
            tx.send(Message::Text(frame.to_string()))
        )
        .await,
        Ok(Ok(()))
    )
}

pub(super) async fn open_shell_session(
    session_id: String,
    payload_val: serde_json::Value,
    ws_tx: mpsc::Sender<Message>,
    shell_sessions: Arc<Mutex<HashMap<String, ShellHandle>>>,
    permit: Arc<OwnedSemaphorePermit>,
    command: Option<String>,
) -> Result<(), String> {
    if session_id.is_empty() || shell_sessions.lock().await.contains_key(&session_id) {
        return Err("empty or duplicate shell session ID".into());
    }
    let payload: ShellOpenPayload =
        serde_json::from_value(payload_val).unwrap_or(ShellOpenPayload {
            cols: Some(80),
            rows: Some(24),
        });
    let cols = payload.cols.unwrap_or(80);
    let rows = payload.rows.unwrap_or(24);

    // Allocate a real PTY (pseudo-terminal).  This gives the shell:
    //   - proper terminal line discipline (Ctrl+C, backspace, readline, etc.)
    //   - ANSI/VT100 escape sequence support
    //   - window resize via SIGWINCH + TIOCSWINSZ
    //   - merged stdout/stderr stream through a single master fd
    let (master_owned, slave) = open_pty(cols, rows)?;
    let shell = find_best_shell()
        .ok_or_else(|| "no usable shell found (tried zsh, fish, bash, sh)".to_string())?;
    let home = std::env::var("HOME").unwrap_or_else(|_| "/root".to_string());
    // Resolve the working directory: prefer /root (standard root home), then $HOME,
    // then / as a final fallback.  This prevents the shell from inheriting the agent
    // process's working directory (e.g. /opt/oneclickvirt/agent).
    let work_dir = if std::path::Path::new("/root").is_dir() {
        "/root".to_string()
    } else if std::path::Path::new(&home).is_dir() {
        home.clone()
    } else {
        "/".to_string()
    };

    // OwnedFd::try_clone atomically sets CLOEXEC on both duplicates. Plain dup
    // leaves an inheritance window while another thread is spawning a command.
    let slave_stdout = slave
        .try_clone()
        .map_err(|e| format!("dup PTY stdout: {e}"))?;
    let slave_stderr = slave
        .try_clone()
        .map_err(|e| format!("dup PTY stderr: {e}"))?;

    // Wrap master in AsyncFd for non-blocking async I/O.  Shared via Arc so
    // both the stdin-writer task and the PTY-reader task can use the same fd.
    let async_master =
        Arc::new(AsyncFd::new(master_owned).map_err(|e| format!("AsyncFd::new: {}", e))?);

    let mut cmd = Command::new(&shell);
    if let Some(command) = command {
        // Start the command directly; no interactive host shell can receive
        // tenant input if container startup fails.
        cmd.arg("-c").arg(format!("exec {}; exit $?", command));
    }
    cmd.kill_on_drop(true);
    cmd.env("TERM", "xterm-256color")
        .env("HOME", &work_dir)
        .env("COLUMNS", cols.to_string())
        .env("LINES", rows.to_string())
        .current_dir(&work_dir)
        .stdin(Stdio::from(slave))
        .stdout(Stdio::from(slave_stdout))
        .stderr(Stdio::from(slave_stderr));

    // pre_exec runs in the forked child AFTER dup2 has mapped the slave to
    // fd 0/1/2.  We must:
    //   1. Call setsid() to create a new session (detach from parent's
    //      controlling terminal).
    //   2. Call TIOCSCTTY on fd 0 (now the slave PTY) to set it as the
    //      controlling terminal for the new session, enabling job control.
    unsafe {
        cmd.pre_exec(|| {
            if libc::setsid() < 0 {
                return Err(std::io::Error::last_os_error());
            }
            // fd 0 is the slave PTY after dup2; set as controlling terminal.
            if libc::ioctl(0, libc::TIOCSCTTY.into(), 1 as libc::c_int) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }

    let mut child = cmd.spawn().map_err(|e| e.to_string())?;
    drop(cmd); // Close the parent's slave handles before waiting for PTY EOF.
    let child_pid = child.id().unwrap_or(0);

    // Ordered stdin writer: channel → PTY master.
    // Using a channel (not per-frame tokio::spawn) guarantees FIFO write order.
    let (stdin_tx, mut stdin_rx) = mpsc::channel::<Vec<u8>>(64);
    let write_master = async_master.clone();
    let stdin_task = tokio::spawn(async move {
        while let Some(data) = stdin_rx.recv().await {
            let mut offset = 0;
            while offset < data.len() {
                match write_master.writable().await {
                    Err(_) => return,
                    Ok(mut guard) => {
                        match guard.try_io(|inner| {
                            let n = unsafe {
                                libc::write(
                                    inner.as_raw_fd(),
                                    data[offset..].as_ptr() as *const libc::c_void,
                                    data.len() - offset,
                                )
                            };
                            if n < 0 {
                                Err(std::io::Error::last_os_error())
                            } else {
                                Ok(n as usize)
                            }
                        }) {
                            Ok(Ok(n)) => {
                                offset += n;
                            }
                            Ok(Err(e)) if e.kind() == std::io::ErrorKind::WouldBlock => {}
                            _ => return,
                        }
                    }
                }
            }
        }
    });

    let (cancel, mut cancelled) = watch::channel(false);
    let task_permit = permit.clone();
    let handle = ShellHandle {
        stdin_tx,
        master: async_master.clone(),
        child_pid,
        _permit: permit,
        cancel,
    };
    // Registration must be an atomic check-and-insert. A controller normally
    // generates random IDs, but a duplicate or replayed ID must not overwrite
    // an existing session after allocating a second PTY and permit.
    let duplicate = {
        let mut sessions = shell_sessions.lock().await;
        if sessions.contains_key(&session_id) {
            true
        } else {
            sessions.insert(session_id.clone(), handle);
            false
        }
    };
    if duplicate {
        pty_kill(child_pid);
        let _ = child.kill().await;
        let _ = child.wait().await;
        return Err("duplicate shell session ID".into());
    }
    let ready = serde_json::json!({"type":"shell_ready", "id":session_id});
    if ws_tx.try_send(Message::Text(ready.to_string())).is_err() {
        pty_kill(child_pid);
        // No child-wait task has been spawned yet, so explicitly reap the
        // process here.  Dropping a killed Child without wait() can leave a
        // zombie until the agent exits, and the stdin writer would otherwise
        // keep polling the PTY after the session was removed.
        stdin_task.abort();
        let _ = stdin_task.await;
        let _ = child.kill().await;
        let _ = child.wait().await;
        shell_sessions.lock().await.remove(&session_id);
        return Err("shell control queue is full".into());
    }

    // PTY reader: master fd → WebSocket.
    // Reading from the master gives us the shell's combined stdout+stderr.
    // EIO is the normal EOF signal when the last slave fd is closed (shell exits).
    let read_master = async_master.clone();
    let ws_tx_reader = ws_tx.clone();
    let session_id_reader = session_id.clone();
    let mut reader_task = tokio::spawn(async move {
        let mut buf = vec![0u8; 8192];
        let mut pending = Vec::new();
        loop {
            let mut guard = match read_master.readable().await {
                Ok(g) => g,
                Err(_) => break,
            };
            let result = guard.try_io(|inner| {
                let n = unsafe {
                    libc::read(
                        inner.as_raw_fd(),
                        buf.as_mut_ptr() as *mut libc::c_void,
                        buf.len(),
                    )
                };
                if n < 0 {
                    Err(std::io::Error::last_os_error())
                } else {
                    Ok(n as usize)
                }
            });
            match result {
                Ok(Ok(0)) => break,
                Ok(Ok(n)) => {
                    let output = decode_pty_chunk(&mut pending, &buf[..n], false);
                    if !send_shell_output(&ws_tx_reader, &session_id_reader, output).await {
                        return;
                    }
                }
                // EIO = shell exited and all slave fds are closed (normal PTY EOF)
                Ok(Err(e)) if e.raw_os_error() == Some(libc::EIO) => break,
                Ok(Err(e)) if e.kind() == std::io::ErrorKind::WouldBlock => {}
                Ok(Err(_)) => break,
                Err(_would_block) => {}
            }
        }
        let output = decode_pty_chunk(&mut pending, &[], true);
        let _ = send_shell_output(&ws_tx_reader, &session_id_reader, output).await;
    });

    // Child-wait task: detect shell exit and notify the controller.
    let shell_sessions_clone = shell_sessions.clone();
    let ws_tx_clone = ws_tx.clone();
    tokio::spawn(async move {
        let _permit = task_permit;
        let status = tokio::select! {
            _ = async {
                if !*cancelled.borrow_and_update() { let _ = cancelled.changed().await; }
            } => {
                pty_kill(child_pid);
                reader_task.abort();
                let _ = reader_task.await;
                child.wait().await.ok()
            }
            status = child.wait() => {
                // EOF data must be queued before shell_close. A descendant
                // retaining the PTY must not hold cleanup forever.
                if tokio::time::timeout(Duration::from_secs(3), &mut reader_task).await.is_err() {
                    reader_task.abort();
                    let _ = reader_task.await;
                }
                status.ok()
            }
            _ = &mut reader_task => {
                pty_kill(child_pid);
                child.wait().await.ok()
            }
        };
        stdin_task.abort();
        let _ = stdin_task.await;
        // Only send shell_close if the session is still registered (not already
        // closed by an explicit shell_close frame from the controller).
        let removed = {
            let mut sessions = shell_sessions_clone.lock().await;
            if sessions
                .get(&session_id)
                .is_some_and(|h| Arc::ptr_eq(&h.master, &async_master))
            {
                sessions.remove(&session_id);
                true
            } else {
                false
            }
        };
        if removed {
            let reason = status
                .and_then(|s| {
                    s.code()
                        .map(|code| format!("shell exited with code {}", code))
                })
                .unwrap_or_else(|| "shell closed".to_string());
            let close_frame = WsFrame {
                msg_type: "shell_close".to_string(),
                id: Some(session_id),
                payload: Some(serde_json::json!({ "reason": reason })),
            };
            if let Ok(text) = serde_json::to_string(&close_frame) {
                let msg = Message::Text(text);
                if ws_tx_clone.try_send(msg.clone()).is_err() {
                    let _ =
                        tokio::time::timeout(Duration::from_secs(3), ws_tx_clone.send(msg)).await;
                }
            }
        }
    });

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::sync::Semaphore;

    #[test]
    fn pty_descriptors_do_not_escape_into_other_commands() {
        let (master, slave) = open_pty(93, 31).unwrap();
        let duplicate = slave.try_clone().unwrap();
        for descriptor in [&master, &slave, &duplicate] {
            let flags = unsafe { libc::fcntl(descriptor.as_raw_fd(), libc::F_GETFD) };
            assert!(flags >= 0);
            assert_ne!(flags & libc::FD_CLOEXEC, 0, "PTY leaked across exec");
        }
        let flags = unsafe { libc::fcntl(master.as_raw_fd(), libc::F_GETFL) };
        assert_ne!(
            flags & libc::O_NONBLOCK,
            0,
            "PTY reader would block the executor"
        );
        let mut dimensions = unsafe { std::mem::zeroed::<libc::winsize>() };
        assert_eq!(
            unsafe { libc::ioctl(slave.as_raw_fd(), libc::TIOCGWINSZ, &mut dimensions) },
            0
        );
        assert_eq!((dimensions.ws_col, dimensions.ws_row), (93, 31));
    }

    #[test]
    fn unrelated_child_cannot_keep_another_session_pty_open() {
        let (master, slave) = open_pty(80, 24).unwrap();
        let duplicate = slave.try_clone().unwrap();
        let mut child = std::process::Command::new("sleep")
            .arg("10")
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        drop(duplicate);
        drop(slave);
        let mut event = libc::pollfd {
            fd: master.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        let ready = unsafe { libc::poll(&mut event, 1, 1000) };
        // Dispose our exact child before assertions, including a regression.
        let _ = child.kill();
        child.wait().unwrap();
        assert_eq!(
            ready, 1,
            "unrelated command retained this session's slave PTY"
        );
        assert_ne!(event.revents & libc::POLLHUP, 0);
    }

    #[test]
    fn concurrent_pty_allocations_never_cross_streams() {
        let start = Arc::new(std::sync::Barrier::new(8));
        std::thread::scope(|scope| {
            let workers: Vec<_> = (0..8)
                .map(|worker| {
                    let start = start.clone();
                    scope.spawn(move || {
                        start.wait();
                        for sequence in 0..64 {
                            let (master, slave) = open_pty(80, 24).unwrap();
                            let mut termios = unsafe { std::mem::zeroed::<libc::termios>() };
                            assert_eq!(
                                unsafe { libc::tcgetattr(slave.as_raw_fd(), &mut termios) },
                                0
                            );
                            unsafe { libc::cfmakeraw(&mut termios) };
                            assert_eq!(
                                unsafe {
                                    libc::tcsetattr(slave.as_raw_fd(), libc::TCSANOW, &termios)
                                },
                                0
                            );
                            let payload = format!("pty-{worker}-{sequence}");
                            assert_eq!(
                                unsafe {
                                    libc::write(
                                        slave.as_raw_fd(),
                                        payload.as_ptr().cast(),
                                        payload.len(),
                                    )
                                },
                                payload.len() as isize
                            );
                            let mut received = Vec::new();
                            while received.len() < payload.len() {
                                let mut event = libc::pollfd {
                                    fd: master.as_raw_fd(),
                                    events: libc::POLLIN,
                                    revents: 0,
                                };
                                assert_eq!(
                                    unsafe { libc::poll(&mut event, 1, 2000) },
                                    1,
                                    "PTY connected to another session"
                                );
                                let mut buffer = [0u8; 64];
                                let size = unsafe {
                                    libc::read(
                                        master.as_raw_fd(),
                                        buffer.as_mut_ptr().cast(),
                                        buffer.len(),
                                    )
                                };
                                assert!(size > 0);
                                received.extend_from_slice(&buffer[..size as usize]);
                            }
                            assert_eq!(received, payload.as_bytes());
                        }
                    })
                })
                .collect();
            for worker in workers {
                worker.join().unwrap();
            }
        });
    }

    #[test]
    fn utf8_survives_every_pty_chunk_boundary() {
        let text = "终端输出🙂\r\n";
        for split in 0..=text.len() {
            let mut pending = Vec::new();
            let mut got = decode_pty_chunk(&mut pending, &text.as_bytes()[..split], false);
            got.push_str(&decode_pty_chunk(
                &mut pending,
                &text.as_bytes()[split..],
                true,
            ));
            assert_eq!(got, text);
            assert!(pending.is_empty());
        }
    }

    #[tokio::test]
    async fn duplicate_shell_registration_does_not_overwrite_or_leak() {
        let sessions = Arc::new(Mutex::new(HashMap::new()));
        let permits = Arc::new(Semaphore::new(2));
        let permit_a = Arc::new(permits.clone().acquire_owned().await.unwrap());
        let permit_b = Arc::new(permits.clone().acquire_owned().await.unwrap());
        let (tx, _rx) = mpsc::channel(16);
        let payload = serde_json::json!({"cols": 80, "rows": 24});

        let first = open_shell_session(
            "replayed-session".to_string(),
            payload.clone(),
            tx.clone(),
            sessions.clone(),
            permit_a,
            None,
        );
        let second = open_shell_session(
            "replayed-session".to_string(),
            payload,
            tx,
            sessions.clone(),
            permit_b,
            None,
        );
        let (first_result, second_result) = tokio::join!(first, second);
        assert_ne!(first_result.is_ok(), second_result.is_ok());
        let duplicate_error = [first_result.as_ref().err(), second_result.as_ref().err()]
            .into_iter()
            .flatten()
            .next()
            .expect("one duplicate registration must fail");
        assert!(duplicate_error.contains("duplicate shell session ID"));

        if let Some(handle) = sessions.lock().await.remove("replayed-session") {
            let _ = handle.cancel.send(true);
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
        assert!(sessions.lock().await.is_empty());
        assert_eq!(permits.available_permits(), 2);
    }

    #[tokio::test]
    async fn full_control_queue_reaps_child_and_releases_permit() {
        let sessions = Arc::new(Mutex::new(HashMap::new()));
        let permits = Arc::new(Semaphore::new(1));
        let permit = Arc::new(permits.clone().acquire_owned().await.unwrap());
        // A zero-capacity channel makes the initial shell_ready try_send fail
        // deterministically.  The setup path must still kill/reap the child,
        // remove the reservation, and return the permit to the pool.
        let (tx, _rx) = mpsc::channel(1);
        tx.try_send(Message::Text("occupied".into())).unwrap();
        let result = open_shell_session(
            "queue-full".to_string(),
            serde_json::json!({"cols": 80, "rows": 24}),
            tx,
            sessions.clone(),
            permit,
            None,
        )
        .await;
        assert!(result.is_err());
        assert!(sessions.lock().await.is_empty());
        assert_eq!(permits.available_permits(), 1);
    }
}
