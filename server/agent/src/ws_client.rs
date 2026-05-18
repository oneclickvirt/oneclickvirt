// WebSocket reverse-connect client for NAT traversal (agent mode).
// The controller acts as WebSocket server; the agent connects back and handles
// exec_req / ping / info / tunnel_open frames.

use futures_util::{SinkExt, StreamExt};
use rand;
use regex;
use serde::{Deserialize, Serialize};
use std::io::{self};
use std::process::Stdio;
use std::sync::Arc;
use std::collections::HashMap;
use std::time::Duration;
use std::os::unix::io::{OwnedFd, FromRawFd, AsRawFd};
use tokio::io::unix::AsyncFd;
use tokio::net::TcpStream;
use tokio::process::Command;
use tokio::sync::{mpsc, Mutex, Semaphore};
use tokio_tungstenite::{client_async, connect_async, tungstenite::Message};
use tokio_tungstenite::tungstenite::{http::Uri, ClientRequestBuilder};
use tracing::{info, warn};
use url;

use crate::tunnel::{handle_binary_frame, handle_tunnel_close, handle_tunnel_keepalive, handle_tunnel_open, SessionMap, WsFrame};

/// Generic envelope used for all frames.
#[derive(Serialize, Deserialize, Debug)]
struct WsFrameLocal {
    #[serde(rename = "type")]
    msg_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    payload: Option<serde_json::Value>,
}

/// Payload received in `exec_req` frames.
#[derive(Deserialize, Debug)]
struct ExecReqPayload {
    #[serde(rename = "command")]
    cmd: String,
}

/// Payload sent in `exec_resp` frames.
#[derive(Serialize, Debug)]
struct ExecRespPayload {
    stdout: String,
    stderr: String,
    exit_code: i32,
}

/// Payload sent in the initial `info` frame.
#[derive(Serialize, Debug)]
struct InfoPayload {
    hostname: String,
    version: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    secret: Option<String>,
}

#[derive(Deserialize, Debug)]
struct ShellOpenPayload {
    cols: Option<u16>,
    rows: Option<u16>,
}

#[derive(Deserialize, Debug)]
struct ShellDataPayload {
    data: String,
}

#[derive(Deserialize, Debug)]
struct ShellResizePayload {
    cols: Option<u16>,
    rows: Option<u16>,
}

/// Shell session handle using a real PTY (pseudo-terminal).
/// `master` is the PTY master fd wrapped in AsyncFd for async I/O.
/// `stdin_tx` sends ordered input bytes to a dedicated writer task.
/// `child_pid` is used for SIGWINCH (resize) and killpg (close).
#[derive(Clone)]
struct ShellHandle {
    stdin_tx: mpsc::Sender<Vec<u8>>,
    master: Arc<AsyncFd<OwnedFd>>,
    child_pid: u32,
}

/// Run the WebSocket reverse-connect loop.
/// Reconnects automatically with exponential back-off (max 60 s).
/// `secret` is sent via HTTP header (Authorization / X-Agent-Secret) AND the
/// initial `info` handshake frame so that it never appears in URL logs.
/// Any `secret` (or `agent_secret`, `token`) query params in `ws_url` are
/// stripped before the HTTP request is built (defense-in-depth).
pub async fn run_ws_client(ws_url: String, secret: String) {
    // Strip any sensitive query params from the URL for security.
    // The secret is transmitted via Authorization header instead.
    let clean_url = strip_secret_params(&ws_url);

    // Track whether we have already tried the ws:// fallback (to avoid loops).
    let mut ws_fallback_tried = false;

    let mut delay_secs: u64 = 2;
    loop {
        let connect_url = if ws_fallback_tried {
            // Already fell back to ws://, use it directly on subsequent attempts.
            clean_url.replacen("wss://", "ws://", 1)
        } else {
            clean_url.clone()
        };

        info!(url = %connect_url, "connecting to controller via WebSocket");

        // For plain ws:// connections, use connect_plain_with_keepalive
        // which configures TCP keepalive (30s idle, 10s interval, 3 probes)
        // on the underlying socket for transport-layer dead-connection detection.
        // For wss:// connections, use the standard connect_async (TLS handled
        // internally by tokio-tungstenite).
        if connect_url.starts_with("wss://") {
            let uri: Uri = match connect_url.parse() {
                Ok(u) => u,
                Err(e) => {
                    warn!(error = %e, "invalid WebSocket URI, retrying");
                    tokio::time::sleep(tokio::time::Duration::from_secs(delay_secs)).await;
                    if delay_secs < 60 { delay_secs = (delay_secs * 2).min(60); }
                    continue;
                }
            };
            let request = ClientRequestBuilder::new(uri)
                .with_header("Authorization", format!("Bearer {}", secret))
                .with_header("X-Agent-Secret", secret.clone());
            match connect_async(request).await {
                Ok((ws_stream, _)) => {
                    // Set TCP keepalive on the underlying socket even for
                    // wss:// connections (TLS wraps the TCP stream but the
                    // kernel-level keepalive still applies).
                    try_set_keepalive_on_maybe_tls(&ws_stream);
                    info!("WebSocket connected to controller");
                    delay_secs = 2;
                    if let Err(e) = handle_connection(ws_stream, &secret).await {
                        warn!(error = %e, "WebSocket connection closed with error");
                    } else {
                        info!("WebSocket connection closed normally");
                    }
                }
                Err(e) => {
                    let err_msg = e.to_string();
                    if !ws_fallback_tried
                        && clean_url.starts_with("wss://")
                        && is_tls_layer_error(&err_msg)
                    {
                        warn!(
                            error = %e,
                            "wss:// failed with TLS error, falling back to ws:// (plain WebSocket)"
                        );
                        ws_fallback_tried = true;
                        delay_secs = 1;
                        continue;
                    }
                    warn!(error = %e, delay_secs, "failed to connect to controller, retrying");
                }
            }
        } else {
            // Plain ws:// with TCP keepalive configured
            match connect_plain_with_keepalive(&connect_url, &secret).await {
                Ok(ws_stream) => {
                    info!("WebSocket connected to controller");
                    delay_secs = 2;
                    if let Err(e) = handle_connection(ws_stream, &secret).await {
                        warn!(error = %e, "WebSocket connection closed with error");
                    } else {
                        info!("WebSocket connection closed normally");
                    }
                }
                Err(e) => {
                    warn!(error = %e, delay_secs, "failed to connect to controller, retrying");
                }
            }
        }
        tokio::time::sleep(tokio::time::Duration::from_secs(delay_secs)).await;
        if delay_secs < 60 {
            delay_secs = (delay_secs * 2).min(60);
        }
    }
}

/// Check whether an error message indicates a TLS-layer failure (as opposed
/// to an HTTP-level or application-level error).  When these patterns appear
/// on a wss:// connection it usually means the server is plain HTTP — not TLS.
fn is_tls_layer_error(err_msg: &str) -> bool {
    let lower = err_msg.to_lowercase();
    lower.contains("invalidcontenttype")
        || lower.contains("corrupt message")
        || lower.contains("tls")
        || lower.contains("ssl")
        || lower.contains("certificate")
        || lower.contains("handshake")
        || lower.contains("bad record mac")
        || lower.contains("unknown protocol")
        || lower.contains("peer misbehaving")
}

async fn handle_connection<S>(ws_stream: tokio_tungstenite::WebSocketStream<S>, secret: &str) -> Result<(), String>
where
    S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
{
    let (mut write, mut read) = ws_stream.split();

    // ── Dual-priority write channels ──────────────────────────────────────
    // hi: control messages (pong, exec_resp, tunnel_ack/close, info, shell)
    //     — small capacity to apply back-pressure early on control floods.
    // lo: tunnel binary data — larger capacity to absorb bulk TCP bursts
    //     without blocking control frames.
    let (ws_tx_hi, mut ws_rx_hi) = mpsc::channel::<Message>(64);
    let (ws_tx_lo, mut ws_rx_lo) = mpsc::channel::<Message>(512);

    // Forwarder: always drain hi-priority channel first.
    // ── Critical: write.send() is wrapped in a 15 s timeout.
    // If the underlying TCP send buffer is full (e.g. server stopped
    // reading), SplitSink::send() can block indefinitely.  A timeout
    // here breaks the deadlock: the forwarder exits → mpsc channels
    // close → all senders get errors → handle_connection returns →
    // run_ws_client reconnects with backoff.
    tokio::spawn(async move {
        loop {
            let msg = match ws_rx_hi.try_recv() {
                Ok(msg) => Some(msg),
                Err(mpsc::error::TryRecvError::Empty) => {
                    tokio::select! {
                        m = ws_rx_hi.recv() => m,
                        m = ws_rx_lo.recv() => m,
                    }
                }
                Err(mpsc::error::TryRecvError::Disconnected) => {
                    match ws_rx_lo.recv().await {
                        Some(msg) => Some(msg),
                        None => break,
                    }
                }
            };
            match msg {
                Some(msg) => {
                    match tokio::time::timeout(
                        Duration::from_secs(15),
                        write.send(msg),
                    )
                    .await
                    {
                        Ok(Ok(())) => {} // write succeeded
                        Ok(Err(_)) => {
                            warn!("write.send returned error, closing write forwarder");
                            break;
                        }
                        Err(_elapsed) => {
                            warn!("write.send timed out (15 s), closing write forwarder to trigger reconnect");
                            break;
                        }
                    }
                }
                None => break,
            }
        }
    });

    // Shared session map for tunnel routing
    let sessions: SessionMap = Arc::new(Mutex::new(HashMap::new()));
    let shell_sessions: Arc<Mutex<HashMap<String, ShellHandle>>> = Arc::new(Mutex::new(HashMap::new()));

    // Limit concurrent command executions to 10 to prevent the agent from
    // spawning an unbounded number of processes when the controller sends
    // many commands in rapid succession.
    let exec_permits: Arc<Semaphore> = Arc::new(Semaphore::new(10));

    // Limit concurrent tunnel open operations to 20 to prevent resource
    // exhaustion (file descriptors, memory, CPU) when the controller rapidly
    // toggles remote connections on the node.  Each tunnel_open spawns a TCP
    // connection and multiple forwarding tasks; without a bound, rapid toggle
    // sequences can cause the agent to become unresponsive.
    let tunnel_permits: Arc<Semaphore> = Arc::new(Semaphore::new(20));

    // Limit concurrent shell sessions to 5 to prevent resource exhaustion
    // when the controller rapidly opens/closes admin terminals.
    let shell_permits: Arc<Semaphore> = Arc::new(Semaphore::new(5));

    // ── Anti-DPI noise sender ───────────────────────────────────────────
    // Periodically sends random-length noise frames ("nop" type) at
    // irregular intervals (15-55s) to break traffic-analysis signatures:
    // message-size distribution, bidirectional symmetry, dead-air patterns.
    let noise_tx = ws_tx_hi.clone();
    tokio::spawn(async move {
        loop {
            let delay_secs = 15 + (rand::random::<u64>() % 41); // 15-55 s
            tokio::time::sleep(std::time::Duration::from_secs(delay_secs)).await;
            let noise_len = rand::random::<usize>() % 513; // 0-512 bytes
            let noise: Vec<u8> = (0..noise_len).map(|_| rand::random::<u8>()).collect();
            let frame = WsFrame {
                msg_type: "nop".to_string(),
                id: None,
                payload: if noise.is_empty() {
                    None
                } else {
                    // Encode as hex string for JSON compatibility
                    let hex: String = noise.iter().map(|b| format!("{:02x}", b)).collect();
                    Some(serde_json::json!({ "h": hex }))
                },
            };
            let text = match serde_json::to_string(&frame) {
                Ok(t) => t,
                Err(_) => continue,
            };
            let msg = Message::Text(text.into());
            // Non-blocking send to avoid stalling the noise task.
            // If the channel is full (write path congested), skip this
            // noise cycle — pong responses already serve as keepalive.
            match noise_tx.try_send(msg) {
                Ok(()) => {}
                Err(mpsc::error::TrySendError::Closed(_)) => {
                    break; // write forwarder exited, connection dead
                }
                Err(mpsc::error::TrySendError::Full(_)) => {
                    // Skip this noise frame rather than blocking.
                    // The write path is congested; pong keepalives
                    // will keep the connection alive.
                }
            }
        }
    });

    // Send initial info frame (includes secret for second-factor validation)
    let hostname = std::fs::read_to_string("/etc/hostname")
        .map(|s| s.trim().to_string())
        .unwrap_or_else(|_| std::env::var("HOSTNAME").unwrap_or_else(|_| "unknown".to_string()));

    let info_frame = WsFrame {
        msg_type: "info".to_string(),
        id: None,
        payload: Some(serde_json::to_value(InfoPayload {
            hostname,
            version: env!("CARGO_PKG_VERSION").to_string(),
            secret: Some(secret.to_string()),
        }).unwrap()),
    };
    let info_text = serde_json::to_string(&info_frame).map_err(|e| e.to_string())?;
    ws_tx_hi.send(Message::Text(info_text.into())).await.map_err(|e| e.to_string())?;

    // Read messages with a 120 s timeout so a silently-broken TCP
    // connection (e.g. NAT gateway dropping state) doesn't cause the
    // agent to block forever.  The controller sends application-level
    // pings every ~30 s and noise every 5-25 s, so a 120 s gap means
    // the connection is truly dead.
    let mut loop_err: Option<String> = None;
    'main_loop: loop {
        let msg_result = match tokio::time::timeout(Duration::from_secs(120), read.next()).await {
            Ok(Some(result)) => result,
            Ok(None) => break 'main_loop, // stream ended cleanly
            Err(_elapsed) => {
                warn!("WebSocket read timeout (120 s), connection may be dead, reconnecting");
                loop_err = Some("read timeout".to_string());
                break 'main_loop;
            }
        };

        let msg = match msg_result {
            Ok(m) => m,
            Err(e) => {
                loop_err = Some(e.to_string());
                break 'main_loop;
            }
        };

        match msg {
            Message::Binary(data) => {
                handle_binary_frame(&data, &sessions).await;
            }
            Message::Text(text) => {
                // Guard against empty text frames (sent by older/buggy
                // controllers) which would produce a confusing "EOF while
                // parsing a value at line 1 column 0" error.
                if text.is_empty() {
                    warn!("received empty WS text frame, skipping");
                    continue;
                }
                let frame: WsFrameLocal = match serde_json::from_str(&text) {
                    Ok(f) => f,
                    Err(e) => {
                        warn!(error = %e, "failed to parse WS frame");
                        continue;
                    }
                };

                match frame.msg_type.as_str() {
                    "exec_req" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        let cmd = frame
                            .payload
                            .as_ref()
                            .and_then(|p| serde_json::from_value::<ExecReqPayload>(p.clone()).ok())
                            .map(|p| p.cmd)
                            .unwrap_or_default();

                        info!(id = %req_id, cmd = %cmd, "executing command from controller");

                        // Spawn command execution in a separate task so the read loop
                        // is never blocked by a slow or hanging command.
                        let ws_tx_hi_clone = ws_tx_hi.clone();
                        let permits = exec_permits.clone();
                        tokio::spawn(async move {
                            let _permit = permits.acquire_owned().await
                                .expect("exec semaphore must not be closed");

                            let output = tokio::time::timeout(
                                std::time::Duration::from_secs(300),
                                Command::new("sh")
                                    .arg("-c")
                                    .arg(&cmd)
                                    .stdout(Stdio::piped())
                                    .stderr(Stdio::piped())
                                    .output(),
                            )
                            .await;

                            let resp_payload = match output {
                                Ok(Ok(out)) => ExecRespPayload {
                                    stdout: String::from_utf8_lossy(&out.stdout).to_string(),
                                    stderr: String::from_utf8_lossy(&out.stderr).to_string(),
                                    exit_code: out.status.code().unwrap_or(-1),
                                },
                                Ok(Err(e)) => ExecRespPayload {
                                    stdout: String::new(),
                                    stderr: e.to_string(),
                                    exit_code: -1,
                                },
                                Err(_elapsed) => ExecRespPayload {
                                    stdout: String::new(),
                                    stderr: "command execution timed out (300s) on agent".to_string(),
                                    exit_code: -1,
                                },
                            };

                            let resp_frame = WsFrame {
                                msg_type: "exec_resp".to_string(),
                                id: Some(req_id.clone()),
                                payload: Some(serde_json::to_value(resp_payload).unwrap()),
                            };
                            let resp_text = serde_json::to_string(&resp_frame)
                                .unwrap_or_else(|e| format!(r#"{{"type":"exec_resp","id":"{}","payload":{{"stdout":"","stderr":"serialize error: {}","exit_code":-1}}}}"#, req_id, e));
                            // Non-blocking send for exec response:
                            // try_send first, fall back to short timeout.
                            let resp_msg = Message::Text(resp_text.into());
                            if ws_tx_hi_clone.try_send(resp_msg.clone()).is_err() {
                                let _ = tokio::time::timeout(
                                    Duration::from_secs(10),
                                    ws_tx_hi_clone.send(resp_msg),
                                ).await;
                            }
                        });
                    }
                    "ping" => {
                        // Spawn pong in a separate task so the jitter sleep never
                        // blocks the main read loop.  Blocking the loop here would
                        // delay processing of shell_data / tunnel frames for up to
                        // 500 ms every heartbeat cycle, making interactive SSH
                        // sessions noticeably laggy.
                        let pong_id = frame.id.clone();
                        let ws_tx_pong = ws_tx_hi.clone();
                        tokio::spawn(async move {
                            // Anti-DPI: random jitter before pong
                            let jitter_ms = rand::random::<u64>() % 500;
                            if jitter_ms > 0 {
                                tokio::time::sleep(Duration::from_millis(jitter_ms)).await;
                            }
                            // Add small random noise to pong payload (0-16 bytes)
                            let noise_len = (rand::random::<u8>() % 17) as usize;
                            let noise: Vec<u8> = (0..noise_len).map(|_| rand::random::<u8>()).collect();
                            let pong = WsFrame {
                                msg_type: "pong".to_string(),
                                id: pong_id,
                                payload: if noise.is_empty() {
                                    None
                                } else {
                                    let hex: String = noise.iter().map(|b| format!("{:02x}", b)).collect();
                                    Some(serde_json::json!({ "n": hex }))
                                },
                            };
                            let pong_text = match serde_json::to_string(&pong) {
                                Ok(t) => t,
                                Err(e) => {
                                    warn!(error = %e, "failed to serialize pong frame");
                                    return;
                                }
                            };
                            let pong_msg = Message::Text(pong_text.into());
                            // Non-blocking try_send; fall back to a short timeout.
                            // Failures here are non-fatal: if the write channel is
                            // closed, the main loop will detect it on the next frame.
                            if ws_tx_pong.try_send(pong_msg.clone()).is_err() {
                                let _ = tokio::time::timeout(
                                    Duration::from_secs(5),
                                    ws_tx_pong.send(pong_msg),
                                ).await;
                            }
                        });
                    }
                    "tunnel_open" => {
                        if let Some(payload_val) = frame.payload {
                            let hi_clone = ws_tx_hi.clone();
                            let lo_clone = ws_tx_lo.clone();
                            let sess_clone = sessions.clone();
                            let permits = tunnel_permits.clone();
                            tokio::spawn(async move {
                                // Acquire a tunnel permit to bound concurrent
                                // tunnel connections.  If all permits are
                                // exhausted, the oldest permit holder must
                                // complete first — this provides natural
                                // backpressure during rapid toggle sequences.
                                let _permit = match permits.try_acquire_owned() {
                                    Ok(p) => p,
                                    Err(_) => {
                                        warn!("tunnel permit exhausted, dropping tunnel_open frame");
                                        return;
                                    }
                                };
                                handle_tunnel_open(payload_val, hi_clone, lo_clone, sess_clone).await;
                            });
                        }
                    }
                    "tunnel_close" => {
                        if let Some(payload_val) = frame.payload {
                            handle_tunnel_close(payload_val, &sessions).await;
                        }
                    }
                    "tunnel_keepalive" => {
                        if let Some(payload_val) = frame.payload {
                            handle_tunnel_keepalive(payload_val, &sessions).await;
                        }
                    }
                    "shell_open" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        let payload = frame.payload.unwrap_or_default();
                        let ws_tx_hi_clone = ws_tx_hi.clone();
                        let shell_sessions_clone = shell_sessions.clone();
                        let permits = shell_permits.clone();
                        tokio::spawn(async move {
                            let _permit = match permits.try_acquire_owned() {
                                Ok(p) => p,
                                Err(_) => {
                                    warn!("shell permit exhausted, dropping shell_open frame");
                                    // Notify the controller that this shell session cannot be opened,
                                    // so it can close its side immediately rather than hanging.
                                    let close_frame = WsFrame {
                                        msg_type: "shell_close".to_string(),
                                        id: Some(req_id),
                                        payload: Some(serde_json::json!({ "reason": "shell permit exhausted" })),
                                    };
                                    if let Ok(text) = serde_json::to_string(&close_frame) {
                                        let _ = ws_tx_hi_clone.try_send(Message::Text(text.into()));
                                    }
                                    return;
                                }
                            };
                            if let Err(err) = open_shell_session(req_id, payload, ws_tx_hi_clone, shell_sessions_clone).await {
                                warn!(error = %err, "failed to open shell session");
                            }
                        });
                    }
                    "shell_data" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        if let Some(payload_val) = frame.payload {
                            if let Ok(payload) = serde_json::from_value::<ShellDataPayload>(payload_val) {
                                // Send data to the session's dedicated stdin writer task.
                                // The channel preserves FIFO order, so stdin bytes always
                                // arrive in the same order as WebSocket frames — unlike
                                // spawning a new task per frame which allows out-of-order writes.
                                if let Some(handle) = shell_sessions.lock().await.get(&req_id).cloned() {
                                    let data = payload.data.into_bytes();
                                    // Non-blocking try_send; fall back to short-timeout send
                                    // to avoid stalling the WS read loop on a full channel.
                                    if handle.stdin_tx.try_send(data.clone()).is_err() {
                                        let tx = handle.stdin_tx.clone();
                                        tokio::spawn(async move {
                                            let _ = tokio::time::timeout(
                                                Duration::from_secs(3),
                                                tx.send(data),
                                            ).await;
                                        });
                                    }
                                }
                            }
                        }
                    }
                    "shell_resize" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        if let Some(payload_val) = frame.payload {
                            if let Ok(payload) = serde_json::from_value::<ShellResizePayload>(payload_val) {
                                if let Some(handle) = shell_sessions.lock().await.get(&req_id).cloned() {
                                    let cols = payload.cols.unwrap_or(80);
                                    let rows = payload.rows.unwrap_or(24);
                                    pty_resize(handle.master.as_raw_fd(), handle.child_pid, cols, rows);
                                }
                            }
                        }
                    }
                    "shell_close" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        // Remove session first (prevents child-wait task from sending a duplicate shell_close).
                        if let Some(handle) = shell_sessions.lock().await.remove(&req_id) {
                            tokio::spawn(async move {
                                // Kill the entire process group so background jobs also die.
                                pty_kill(handle.child_pid);
                                // Dropping the handle closes master fd (stopping the reader task)
                                // and drops stdin_tx (stopping the writer task).
                                drop(handle);
                            });
                        }
                    }
                    "nop" => {
                        // Anti-DPI noise frame — silently discarded.
                        // Contains random-length payload from controller.
                    }
                    other => {
                        info!(msg_type = %other, "received unhandled frame type");
                    }
                }
            }
            Message::Close(_) => {
                info!("received close frame from controller");
                break 'main_loop;
            }
            Message::Ping(data) => {
                // Protocol-level pong: non-blocking send to avoid deadlock.
                let pong_msg = Message::Pong(data);
                if ws_tx_hi.try_send(pong_msg).is_err() {
                    // Channel closed or full — spawn a one-shot task with
                    // a short timeout so the read loop stays unblocked.
                    let tx = ws_tx_hi.clone();
                    tokio::spawn(async move {
                        let _ = tokio::time::timeout(
                            Duration::from_secs(5),
                            tx.send(Message::Pong(vec![])),
                        )
                        .await;
                    });
                }
            }
            _ => {}
        }
    } // end 'main_loop

    // Cleanup: kill all active shell sessions to prevent orphan processes
    // after WebSocket disconnect or reconnect.
    {
        let mut sessions_guard = shell_sessions.lock().await;
        for (_, handle) in sessions_guard.drain() {
            pty_kill(handle.child_pid);
            // Dropping handle closes stdin_tx (writer task exits) and
            // decrements master Arc (reader task gets EIO and exits).
        }
    }

    match loop_err {
        Some(e) => Err(e),
        None => Ok(()),
    }
}
/// Find the best available interactive shell (zsh > fish > bash > sh).
fn find_best_shell() -> Option<String> {
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
fn open_pty(cols: u16, rows: u16) -> Result<(OwnedFd, i32), String> {
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
fn pty_resize(master_fd: i32, child_pid: u32, cols: u16, rows: u16) {
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
fn pty_kill(child_pid: u32) {
    unsafe {
        let pgid = libc::getpgid(child_pid as libc::pid_t);
        if pgid > 0 {
            libc::killpg(pgid, libc::SIGKILL);
        } else {
            libc::kill(child_pid as libc::pid_t, libc::SIGKILL);
        }
    }
}

async fn open_shell_session(
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


/// Strip sensitive query parameters (secret, agent_secret, token) from a URL.
/// Defense-in-depth: even if the caller passes a URL with the secret embedded,
/// we remove it before using the URL in the HTTP request or logging.
fn strip_secret_params(url_str: &str) -> String {
    if !url_str.contains('?') {
        return url_str.to_string();
    }

    // Use the `url` crate for robust parsing
    if let Ok(mut parsed) = url::Url::parse(url_str) {
        let sensitive: &[&str] = &["secret", "agent_secret", "token"];
        let had = sensitive.iter().any(|k| {
            parsed.query_pairs().any(|(qk, _)| *k == qk.as_ref())
        });
        if !had {
            return url_str.to_string();
        }
        let new_query: Vec<String> = parsed
            .query_pairs()
            .filter(|(k, _)| !sensitive.iter().any(|sk| *sk == k.as_ref()))
            .map(|(k, v)| format!("{}={}", k, v))
            .collect();
        if new_query.is_empty() {
            parsed.set_query(None);
        } else {
            parsed.set_query(Some(&new_query.join("&")));
        }
        return parsed.to_string();
    }

    // Fallback: simple regex-based strip
    let re = regex::Regex::new(r"[&?](secret|agent_secret|token)=[^&]*").unwrap();
    re.replace_all(url_str, "")
        .replace("?&", "?")
        .trim_end_matches('?')
        .to_string()
}
/// Set TCP keepalive on the underlying socket of a WebSocketStream that
/// wraps a MaybeTlsStream<TcpStream>.  Works for both Plain (ws://) and
/// Rustls (wss://) variants by extracting the raw file descriptor.
///
/// This is a best-effort operation: failures are silently ignored since
/// keepalive is a defense-in-depth measure, not a correctness requirement.
#[cfg(unix)]
fn try_set_keepalive_on_maybe_tls(
    ws: &tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>,
) {
    use tokio_tungstenite::MaybeTlsStream;

    let inner = ws.get_ref();
    match inner {
        MaybeTlsStream::Plain(tcp) => {
            set_keepalive_on_tcp(tcp);
        }
        MaybeTlsStream::Rustls(tls) => {
            // tokio_rustls::TlsStream<TcpStream>::get_ref() returns
            // &(TcpStream, SessionState) — the TcpStream is in .0
            let (tcp, _) = tls.get_ref();
            set_keepalive_on_tcp(tcp);
        }
        _ => {}
    }
}

/// Configure TCP keepalive on a tokio TcpStream using socket2.
#[cfg(unix)]
fn set_keepalive_on_tcp(stream: &TcpStream) {
    use socket2::SockRef;
    let sock = SockRef::from(stream);
    let _ = sock.set_keepalive(true);
    #[cfg(target_os = "linux")]
    {
        let ka = socket2::TcpKeepalive::new()
            .with_time(Duration::from_secs(30))
            .with_interval(Duration::from_secs(10));
        // Note: socket2 0.5 does not expose TCP_KEEPCNT (retries);
        // the OS default (typically 9 on Linux) is sufficient.
        let _ = sock.set_tcp_keepalive(&ka);
    }
    #[cfg(not(target_os = "linux"))]
    {
        let ka = socket2::TcpKeepalive::new()
            .with_time(Duration::from_secs(30));
        let _ = sock.set_tcp_keepalive(&ka);
    }
}

/// no-op on non-Unix platforms (keepalive not supported).
#[cfg(not(unix))]
fn try_set_keepalive_on_maybe_tls(
    _ws: &tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<TcpStream>>,
) {
}

#[cfg(not(unix))]
fn set_keepalive_on_tcp(_stream: &TcpStream) {
}
/// Create a TCP stream with keepalive configured using tokio's async connect.
///
/// TCP keepalive parameters (applied after connect via socket2::SockRef):
///   - idle:  30 s  (send first probe after 30 s of silence)
///   - interval: 10 s  (wait 10 s between probes)
///
/// Total detection time: 30 + 10*3 = 60 s worst case (with OS default retries=3).
///
/// IMPORTANT: We use tokio::net::TcpStream::connect() (async) instead of
/// socket2::Socket::connect() on a non-blocking socket.  The latter returns
/// EINPROGRESS immediately on Linux — the TCP handshake has not completed
/// yet — which causes every connection attempt to fail.
///
/// `addr` must be a `"host:port"` string.  IPv6 addresses must be wrapped in
/// brackets, e.g. `"[::1]:8080"`.  Hostnames are resolved via tokio's async
/// DNS resolver (getaddrinfo).  Do NOT pass a bare `std::net::SocketAddr` —
/// SocketAddr::parse() rejects hostnames and would break non-IP deployments.
async fn create_tcp_stream_with_keepalive(addr: &str) -> io::Result<TcpStream> {
    let stream = TcpStream::connect(addr).await?;

    // Configure TCP keepalive on the underlying socket via socket2::SockRef.
    // tokio::net::TcpStream implements AsRawFd on Unix, so SockRef can
    // operate on its fd directly.
    use socket2::SockRef;
    let sock_ref = SockRef::from(&stream);
    sock_ref.set_keepalive(true)?;

    // Configure TCP keepalive timing.
    // On Linux: full configuration with idle time and probe interval.
    // On macOS: idle time only (TCP_KEEPALIVE), interval not settable via socket2.
    #[cfg(target_os = "linux")]
    {
        let ka = socket2::TcpKeepalive::new()
            .with_time(Duration::from_secs(30))
            .with_interval(Duration::from_secs(10));
        // Note: socket2 0.5 does not expose TCP_KEEPCNT (retries);
        // the OS default (typically 9 on Linux) is sufficient.
        let _ = sock_ref.set_tcp_keepalive(&ka);
    }
    #[cfg(not(target_os = "linux"))]
    {
        // Fallback: set idle time via TcpKeepalive (interval uses OS defaults)
        let ka = socket2::TcpKeepalive::new()
            .with_time(Duration::from_secs(30));
        let _ = sock_ref.set_tcp_keepalive(&ka);
    }

    Ok(stream)
}

/// Connect to the controller WebSocket with TCP keepalive configured
/// (for plain ws:// connections only).  wss:// connections use the
/// standard connect_async path which handles TLS internally.
async fn connect_plain_with_keepalive(
    url_str: &str,
    secret: &str,
) -> Result<tokio_tungstenite::WebSocketStream<TcpStream>, String> {
    let parsed_url = url::Url::parse(url_str).map_err(|e| format!("invalid URL: {e}"))?;
    let host = parsed_url
        .host_str()
        .ok_or_else(|| "missing host in URL".to_string())?;
    let port = parsed_url.port().unwrap_or(80);
    // Build the "host:port" string for tokio's async connect, which handles
    // DNS resolution, IPv4, and IPv6.  URL parsing strips IPv6 brackets, so
    // we restore them when the host contains ':', otherwise a bare
    // "::1:8080" string would be misparse by ToSocketAddrs.
    let addr_str = if host.contains(':') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    };

    let tcp_stream = create_tcp_stream_with_keepalive(&addr_str).await
        .map_err(|e| format!("TCP connect failed: {e}"))?;

    // Parse the full URL as the request URI (same approach as the wss://
    // path using connect_async).  http::Uri extracts the Host header and
    // request path from the full URL automatically.
    let request_uri: Uri = url_str
        .parse()
        .map_err(|e| format!("invalid URI: {e}"))?;

    let request = ClientRequestBuilder::new(request_uri)
        .with_header("Authorization", format!("Bearer {}", secret))
        .with_header("X-Agent-Secret", secret.to_string());

    let (ws_stream, _) =
        client_async(request, tcp_stream)
            .await
            .map_err(|e| format!("WebSocket handshake failed: {e}"))?;
    Ok(ws_stream)
}
