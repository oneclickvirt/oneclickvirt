// PTY-based shell session management for the agent WebSocket client.
use std::collections::HashMap;
use std::os::unix::io::{OwnedFd, FromRawFd, AsRawFd};
use std::process::Stdio;
use std::sync::Arc;
use std::time::Duration;
use tokio::io::unix::AsyncFd;
use tokio::process::Command;
use tokio::sync::{mpsc, Mutex};
use tokio_tungstenite::tungstenite::Message;

use crate::tunnel::WsFrame;
use super::types::{ShellHandle, ShellOpenPayload};

/// Find the best available interactive shell (zsh > fish > bash > sh).
pub(super) fn find_best_shell() -> Option<String> {
    let candidates = [
        "/usr/bin/zsh", "/bin/zsh",
        "/usr/bin/fish", "/usr/local/bin/fish",
        "/usr/bin/bash", "/bin/bash",
        "/bin/sh", "/usr/bin/sh",
    ];
    candidates.iter()
        .find(|p| std::path::Path::new(p).exists())
        .map(|s| s.to_string())
}

/// Allocate a PTY master/slave pair, set the initial window size, and
/// return (master_owned_fd, slave_raw_fd).  The caller is responsible for
/// closing `slave_raw_fd` in the parent process after spawning the child.
pub(super) fn open_pty(cols: u16, rows: u16) -> Result<(OwnedFd, i32), String> {
    let master_fd = unsafe {
        libc::posix_openpt(libc::O_RDWR | libc::O_NOCTTY | libc::O_CLOEXEC)
    };
    if master_fd < 0 {
        return Err(format!("posix_openpt: {}", std::io::Error::last_os_error()));
    }
    if unsafe { libc::grantpt(master_fd) } < 0 {
        unsafe { libc::close(master_fd) };
        return Err(format!("grantpt: {}", std::io::Error::last_os_error()));
    }
    if unsafe { libc::unlockpt(master_fd) } < 0 {
        unsafe { libc::close(master_fd) };
        return Err(format!("unlockpt: {}", std::io::Error::last_os_error()));
    }
    // Set initial window size on the master before opening the slave.
    let ws = libc::winsize { ws_row: rows, ws_col: cols, ws_xpixel: 0, ws_ypixel: 0 };
    unsafe { libc::ioctl(master_fd, libc::TIOCSWINSZ, &ws) };
    // Get the slave PTY path.
    let slave_name_ptr = unsafe { libc::ptsname(master_fd) };
    if slave_name_ptr.is_null() {
        unsafe { libc::close(master_fd) };
        return Err("ptsname returned null".to_string());
    }
    let slave_name = unsafe { std::ffi::CStr::from_ptr(slave_name_ptr) }
        .to_str()
        .map_err(|e| e.to_string())?
        .to_owned();
    // Open the slave PTY.
    let slave_name_c = std::ffi::CString::new(slave_name).map_err(|e| e.to_string())?;
    let slave_fd = unsafe { libc::open(slave_name_c.as_ptr(), libc::O_RDWR | libc::O_NOCTTY) };
    if slave_fd < 0 {
        unsafe { libc::close(master_fd) };
        return Err(format!("open slave PTY: {}", std::io::Error::last_os_error()));
    }
    // Set O_NONBLOCK on master so tokio's AsyncFd can poll it without blocking.
    let flags = unsafe { libc::fcntl(master_fd, libc::F_GETFL) };
    unsafe { libc::fcntl(master_fd, libc::F_SETFL, flags | libc::O_NONBLOCK) };
    let master_owned = unsafe { OwnedFd::from_raw_fd(master_fd) };
    Ok((master_owned, slave_fd))
}

/// Resize the PTY window and deliver SIGWINCH to the shell's process group.
pub(super) fn pty_resize(master_fd: i32, child_pid: u32, cols: u16, rows: u16) {
    unsafe {
        let ws = libc::winsize { ws_row: rows, ws_col: cols, ws_xpixel: 0, ws_ypixel: 0 };
        libc::ioctl(master_fd, libc::TIOCSWINSZ.into(), &ws);
        let pgid = libc::getpgid(child_pid as libc::pid_t);
        if pgid > 0 {
            libc::killpg(pgid, libc::SIGWINCH);
        } else {
            libc::kill(child_pid as libc::pid_t, libc::SIGWINCH);
        }
    }
}

/// Kill the shell's entire process group (ensures background jobs also die).
pub(super) fn pty_kill(child_pid: u32) {
    unsafe {
        let pgid = libc::getpgid(child_pid as libc::pid_t);
        if pgid > 0 {
            libc::killpg(pgid, libc::SIGKILL);
        } else {
            libc::kill(child_pid as libc::pid_t, libc::SIGKILL);
        }
    }
}

pub(super) async fn open_shell_session(
    session_id: String,
    payload_val: serde_json::Value,
    ws_tx: mpsc::Sender<Message>,
    shell_sessions: Arc<Mutex<HashMap<String, ShellHandle>>>,
) -> Result<(), String> {
    let payload: ShellOpenPayload = serde_json::from_value(payload_val).unwrap_or(ShellOpenPayload {
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
    let (master_owned, slave_fd) = open_pty(cols, rows)?;
    let shell = find_best_shell()
        .ok_or_else(|| "no usable shell found (tried zsh, fish, bash, sh)".to_string())?;
    let home = std::env::var("HOME").unwrap_or_else(|_| "/root".to_string());

    // Duplicate slave_fd for stdout and stderr — tokio Command::stdin() takes
    // ownership of the fd, so we need separate file descriptors for each stdio stream.
    let slave_stdout = unsafe { libc::dup(slave_fd) };
    let slave_stderr = unsafe { libc::dup(slave_fd) };
    if slave_stdout < 0 || slave_stderr < 0 {
        unsafe {
            if slave_stdout >= 0 { libc::close(slave_stdout); }
            if slave_stderr >= 0 { libc::close(slave_stderr); }
            libc::close(slave_fd);
        }
        return Err(format!("dup slave PTY fd: {}", std::io::Error::last_os_error()));
    }

    // Wrap master in AsyncFd for non-blocking async I/O.  Shared via Arc so
    // both the stdin-writer task and the PTY-reader task can use the same fd.
    let async_master = Arc::new(
        AsyncFd::new(master_owned).map_err(|e| format!("AsyncFd::new: {}", e))?
    );

    let mut cmd = Command::new(&shell);
    cmd.env("TERM", "xterm-256color")
       .env("HOME", &home)
       .env("COLUMNS", cols.to_string())
       .env("LINES", rows.to_string())
       .stdin(unsafe { Stdio::from_raw_fd(slave_fd) })
       .stdout(unsafe { Stdio::from_raw_fd(slave_stdout) })
       .stderr(unsafe { Stdio::from_raw_fd(slave_stderr) });

    // pre_exec runs in the forked child AFTER dup2 has mapped slave_fd to
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
            libc::ioctl(0, libc::TIOCSCTTY.into(), 1 as libc::c_int);
            Ok(())
        });
    }

    let mut child = cmd.spawn().map_err(|e| e.to_string())?;
    let child_pid = child.id().unwrap_or(0);

    // Ordered stdin writer: channel → PTY master.
    // Using a channel (not per-frame tokio::spawn) guarantees FIFO write order.
    let (stdin_tx, mut stdin_rx) = mpsc::channel::<Vec<u8>>(64);
    let write_master = async_master.clone();
    tokio::spawn(async move {
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
                            if n < 0 { Err(std::io::Error::last_os_error()) }
                            else { Ok(n as usize) }
                        }) {
                            Ok(Ok(n)) => { offset += n; }
                            Ok(Err(e)) if e.kind() == std::io::ErrorKind::WouldBlock => {}
                            _ => return,
                        }
                    }
                }
            }
        }
    });

    let handle = ShellHandle { stdin_tx, master: async_master.clone(), child_pid };
    shell_sessions.lock().await.insert(session_id.clone(), handle);

    // PTY reader: master fd → WebSocket.
    // Reading from the master gives us the shell's combined stdout+stderr.
    // EIO is the normal EOF signal when the last slave fd is closed (shell exits).
    let read_master = async_master.clone();
    let ws_tx_reader = ws_tx.clone();
    let session_id_reader = session_id.clone();
    tokio::spawn(async move {
        let mut buf = vec![0u8; 8192];
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
                if n < 0 { Err(std::io::Error::last_os_error()) }
                else { Ok(n as usize) }
            });
            match result {
                Ok(Ok(0)) => break,
                Ok(Ok(n)) => {
                    let frame = WsFrame {
                        msg_type: "shell_data".to_string(),
                        id: Some(session_id_reader.clone()),
                        payload: Some(serde_json::json!({
                            "data": String::from_utf8_lossy(&buf[..n]).to_string()
                        })),
                    };
                    if let Ok(text) = serde_json::to_string(&frame) {
                        let msg = Message::Text(text.into());
                        if ws_tx_reader.try_send(msg.clone()).is_err() {
                            let _ = tokio::time::timeout(
                                Duration::from_secs(3),
                                ws_tx_reader.send(msg),
                            ).await;
                        }
                    }
                }
                // EIO = shell exited and all slave fds are closed (normal PTY EOF)
                Ok(Err(e)) if e.raw_os_error() == Some(libc::EIO) => break,
                Ok(Err(e)) if e.kind() == std::io::ErrorKind::WouldBlock => {}
                Ok(Err(_)) => break,
                Err(_would_block) => {}
            }
        }
    });

    // Child-wait task: detect shell exit and notify the controller.
    let shell_sessions_clone = shell_sessions.clone();
    let ws_tx_clone = ws_tx.clone();
    tokio::spawn(async move {
        let status = child.wait().await.ok();
        // Only send shell_close if the session is still registered (not already
        // closed by an explicit shell_close frame from the controller).
        if shell_sessions_clone.lock().await.remove(&session_id).is_some() {
            let reason = status
                .and_then(|s| s.code().map(|code| format!("shell exited with code {}", code)))
                .unwrap_or_else(|| "shell closed".to_string());
            let close_frame = WsFrame {
                msg_type: "shell_close".to_string(),
                id: Some(session_id),
                payload: Some(serde_json::json!({ "reason": reason })),
            };
            if let Ok(text) = serde_json::to_string(&close_frame) {
                let msg = Message::Text(text.into());
                if ws_tx_clone.try_send(msg.clone()).is_err() {
                    let _ = tokio::time::timeout(
                        Duration::from_secs(3),
                        ws_tx_clone.send(msg),
                    ).await;
                }
            }
        }
    });

    Ok(())
}


