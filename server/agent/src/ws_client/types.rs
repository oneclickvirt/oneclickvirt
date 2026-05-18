// WebSocket frame and session types for the agent WebSocket client.
use serde::{Deserialize, Serialize};
use std::os::unix::io::OwnedFd;
use std::sync::Arc;
use tokio::io::unix::AsyncFd;
use tokio::sync::mpsc;

/// Generic envelope used for all frames.
#[derive(Serialize, Deserialize, Debug)]
pub(super) struct WsFrameLocal {
    #[serde(rename = "type")]
    pub(super) msg_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(super) id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(super) payload: Option<serde_json::Value>,
}

/// Payload received in `exec_req` frames.
#[derive(Deserialize, Debug)]
pub(super) struct ExecReqPayload {
    #[serde(rename = "command")]
    pub(super) cmd: String,
}

/// Payload sent in `exec_resp` frames.
#[derive(Serialize, Debug)]
pub(super) struct ExecRespPayload {
    pub(super) stdout: String,
    pub(super) stderr: String,
    pub(super) exit_code: i32,
}

/// Payload sent in the initial `info` frame.
#[derive(Serialize, Debug)]
pub(super) struct InfoPayload {
    pub(super) hostname: String,
    pub(super) version: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(super) secret: Option<String>,
}

#[derive(Deserialize, Debug)]
pub(super) struct ShellOpenPayload {
    pub(super) cols: Option<u16>,
    pub(super) rows: Option<u16>,
}

#[derive(Deserialize, Debug)]
pub(super) struct ShellDataPayload {
    pub(super) data: String,
}

#[derive(Deserialize, Debug)]
pub(super) struct ShellResizePayload {
    pub(super) cols: Option<u16>,
    pub(super) rows: Option<u16>,
}

/// Shell session handle using a real PTY (pseudo-terminal).
/// `master` is the PTY master fd wrapped in AsyncFd for async I/O.
/// `stdin_tx` sends ordered input bytes to a dedicated writer task.
/// `child_pid` is used for SIGWINCH (resize) and killpg (close).
#[derive(Clone)]
pub(super) struct ShellHandle {
    pub(super) stdin_tx: mpsc::Sender<Vec<u8>>,
    pub(super) master: Arc<AsyncFd<OwnedFd>>,
    pub(super) child_pid: u32,
}
