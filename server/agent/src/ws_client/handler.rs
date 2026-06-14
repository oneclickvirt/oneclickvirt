// WebSocket connection handler for the agent WebSocket client.
use futures_util::{SinkExt, StreamExt};
use rand;
use std::collections::HashMap;
use std::os::unix::io::AsRawFd;
use std::process::Stdio;
use std::sync::Arc;
use std::time::Duration;
use tokio::process::Command;
use tokio::sync::{Mutex, Semaphore, mpsc, oneshot};
use tokio_tungstenite::tungstenite::Message;
use tracing::{info, warn};

use super::shell::{open_shell_session, pty_kill, pty_resize};
use super::types::*;
use crate::tunnel::{
    SessionMap, WsFrame, handle_binary_frame, handle_tunnel_close, handle_tunnel_keepalive,
    handle_tunnel_open, reject_tunnel_open,
};

pub(super) async fn handle_connection<S>(
    ws_stream: tokio_tungstenite::WebSocketStream<S>,
    secret: &str,
) -> Result<(), String>
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
    let (write_done_tx, mut write_done_rx) = oneshot::channel::<()>();

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
                Err(mpsc::error::TryRecvError::Disconnected) => match ws_rx_lo.recv().await {
                    Some(msg) => Some(msg),
                    None => break,
                },
            };
            match msg {
                Some(msg) => {
                    match tokio::time::timeout(Duration::from_secs(15), write.send(msg)).await {
                        Ok(Ok(())) => {} // write succeeded
                        Ok(Err(_)) => {
                            warn!("write.send returned error, closing write forwarder");
                            break;
                        }
                        Err(_elapsed) => {
                            warn!(
                                "write.send timed out (15 s), closing write forwarder to trigger reconnect"
                            );
                            break;
                        }
                    }
                }
                None => break,
            }
        }
        let _ = write_done_tx.send(());
    });

    // Shared session map for tunnel routing
    let sessions: SessionMap = Arc::new(Mutex::new(HashMap::new()));
    let shell_sessions: Arc<Mutex<HashMap<String, ShellHandle>>> =
        Arc::new(Mutex::new(HashMap::new()));

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
    // irregular intervals (45-120s) to break traffic-analysis signatures.
    // Interval is deliberately long so the noise does NOT produce a
    // recognisable high-frequency heartbeat (which would itself become
    // a fingerprint).  The payload field key is randomised each cycle to
    // prevent structural matching on {"h":"..."}-style patterns.
    let noise_tx = ws_tx_hi.clone();
    tokio::spawn(async move {
        // Randomise field key pool; pick one per cycle.
        const NOISE_KEYS: &[&str] = &["d", "v", "p", "r", "c", "b", "m", "x", "e", "q"];
        loop {
            let delay_secs = 45 + (rand::random::<u64>() % 76); // 45-120 s
            tokio::time::sleep(std::time::Duration::from_secs(delay_secs)).await;
            let noise_len = rand::random::<usize>() % 513; // 0-512 bytes
            let noise: Vec<u8> = (0..noise_len).map(|_| rand::random::<u8>()).collect();
            // Pick a random field key for the payload so consecutive noise
            // frames don't share the same JSON structure.
            let key_idx = (rand::random::<usize>()) % NOISE_KEYS.len();
            let key = NOISE_KEYS[key_idx];
            let frame = WsFrame {
                msg_type: "nop".to_string(),
                id: None,
                payload: if noise.is_empty() {
                    None
                } else {
                    // Encode as hex string for JSON compatibility
                    let hex: String = noise.iter().map(|b| format!("{:02x}", b)).collect();
                    Some(serde_json::json!({ key: hex }))
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
        payload: Some(
            serde_json::to_value(InfoPayload {
                hostname,
                version: env!("CARGO_PKG_VERSION").to_string(),
                secret: Some(secret.to_string()),
            })
            .unwrap(),
        ),
    };
    let info_text = serde_json::to_string(&info_frame).map_err(|e| e.to_string())?;
    ws_tx_hi
        .send(Message::Text(info_text.into()))
        .await
        .map_err(|e| e.to_string())?;

    // Read messages with a 180 s timeout so a silently-broken TCP
    // connection (e.g. NAT gateway dropping state) doesn't cause the
    // agent to block forever.  The controller sends application-level
    // pings every ~30 s and noise every 45-120 s, so a 180 s gap means
    // the connection is truly dead.
    let mut loop_err: Option<String> = None;
    'main_loop: loop {
        let msg_result = tokio::select! {
            _ = &mut write_done_rx => {
                warn!("WebSocket write forwarder stopped, reconnecting");
                loop_err = Some("write forwarder stopped".to_string());
                break 'main_loop;
            }
            result = tokio::time::timeout(Duration::from_secs(180), read.next()) => {
                match result {
                    Ok(Some(result)) => result,
                    Ok(None) => break 'main_loop, // stream ended cleanly
                    Err(_elapsed) => {
                        warn!("WebSocket read timeout (180 s), connection may be dead, reconnecting");
                        loop_err = Some("read timeout".to_string());
                        break 'main_loop;
                    }
                }
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
                            let _permit = match tokio::time::timeout(
                                Duration::from_secs(10),
                                permits.acquire_owned(),
                            )
                            .await
                            {
                                Ok(Ok(permit)) => permit,
                                _ => {
                                    let resp_payload = ExecRespPayload {
                                        stdout: String::new(),
                                        stderr: "agent exec concurrency limit exceeded".to_string(),
                                        exit_code: -1,
                                    };
                                    let resp_frame = WsFrame {
                                        msg_type: "exec_resp".to_string(),
                                        id: Some(req_id.clone()),
                                        payload: Some(serde_json::to_value(resp_payload).unwrap()),
                                    };
                                    if let Ok(resp_text) = serde_json::to_string(&resp_frame) {
                                        let msg = Message::Text(resp_text.into());
                                        if ws_tx_hi_clone.try_send(msg.clone()).is_err() {
                                            let _ = tokio::time::timeout(
                                                Duration::from_secs(5),
                                                ws_tx_hi_clone.send(msg),
                                            )
                                            .await;
                                        }
                                    }
                                    return;
                                }
                            };

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
                                    stderr: "command execution timed out (300s) on agent"
                                        .to_string(),
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
                                )
                                .await;
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
                            let noise: Vec<u8> =
                                (0..noise_len).map(|_| rand::random::<u8>()).collect();
                            let pong = WsFrame {
                                msg_type: "pong".to_string(),
                                id: pong_id,
                                payload: if noise.is_empty() {
                                    None
                                } else {
                                    let hex: String =
                                        noise.iter().map(|b| format!("{:02x}", b)).collect();
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
                                )
                                .await;
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
                                        warn!(
                                            "tunnel permit exhausted, dropping tunnel_open frame"
                                        );
                                        reject_tunnel_open(
                                            payload_val,
                                            hi_clone,
                                            "tunnel permit exhausted",
                                        )
                                        .await;
                                        return;
                                    }
                                };
                                handle_tunnel_open(payload_val, hi_clone, lo_clone, sess_clone)
                                    .await;
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
                                        payload: Some(
                                            serde_json::json!({ "reason": "shell permit exhausted" }),
                                        ),
                                    };
                                    if let Ok(text) = serde_json::to_string(&close_frame) {
                                        let msg = Message::Text(text.into());
                                        if ws_tx_hi_clone.try_send(msg.clone()).is_err() {
                                            let _ = tokio::time::timeout(
                                                Duration::from_secs(5),
                                                ws_tx_hi_clone.send(msg),
                                            )
                                            .await;
                                        }
                                    }
                                    return;
                                }
                            };
                            if let Err(err) = open_shell_session(
                                req_id.clone(),
                                payload,
                                ws_tx_hi_clone.clone(),
                                shell_sessions_clone,
                            )
                            .await
                            {
                                warn!(error = %err, "failed to open shell session");
                                // Tell the controller immediately. Otherwise StartShell() has already
                                // returned successfully and the browser terminal waits forever.
                                let close_frame = WsFrame {
                                    msg_type: "shell_close".to_string(),
                                    id: Some(req_id),
                                    payload: Some(
                                        serde_json::json!({ "reason": format!("failed to open shell session: {}", err) }),
                                    ),
                                };
                                if let Ok(text) = serde_json::to_string(&close_frame) {
                                    let msg = Message::Text(text.into());
                                    if ws_tx_hi_clone.try_send(msg.clone()).is_err() {
                                        let _ = tokio::time::timeout(
                                            Duration::from_secs(5),
                                            ws_tx_hi_clone.send(msg),
                                        )
                                        .await;
                                    }
                                }
                            }
                        });
                    }
                    "shell_data" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        if let Some(payload_val) = frame.payload {
                            if let Ok(payload) =
                                serde_json::from_value::<ShellDataPayload>(payload_val)
                            {
                                // Send data to the session's dedicated stdin writer task.
                                // The channel preserves FIFO order, so stdin bytes always
                                // arrive in the same order as WebSocket frames — unlike
                                // spawning a new task per frame which allows out-of-order writes.
                                if let Some(handle) =
                                    shell_sessions.lock().await.get(&req_id).cloned()
                                {
                                    let data = payload.data.into_bytes();
                                    // Non-blocking try_send; fall back to short-timeout send
                                    // to avoid stalling the WS read loop on a full channel.
                                    if handle.stdin_tx.try_send(data.clone()).is_err() {
                                        let tx = handle.stdin_tx.clone();
                                        tokio::spawn(async move {
                                            let _ = tokio::time::timeout(
                                                Duration::from_secs(3),
                                                tx.send(data),
                                            )
                                            .await;
                                        });
                                    }
                                }
                            }
                        }
                    }
                    "shell_resize" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        if let Some(payload_val) = frame.payload {
                            if let Ok(payload) =
                                serde_json::from_value::<ShellResizePayload>(payload_val)
                            {
                                if let Some(handle) =
                                    shell_sessions.lock().await.get(&req_id).cloned()
                                {
                                    let cols = payload.cols.unwrap_or(80);
                                    let rows = payload.rows.unwrap_or(24);
                                    pty_resize(
                                        handle.master.as_raw_fd(),
                                        handle.child_pid,
                                        cols,
                                        rows,
                                    );
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
                    "fm_list" => {
                        let id = frame.id;
                        let payload = frame.payload;
                        let tx = ws_tx_hi.clone();
                        tokio::spawn(async move {
                            crate::fm::handle_fm_list(id, payload, tx).await;
                        });
                    }
                    "fm_download" => {
                        let id = frame.id;
                        let payload = frame.payload;
                        let tx = ws_tx_hi.clone();
                        tokio::spawn(async move {
                            crate::fm::handle_fm_download(id, payload, tx).await;
                        });
                    }
                    "fm_upload" => {
                        let id = frame.id;
                        let payload = frame.payload;
                        let tx = ws_tx_hi.clone();
                        tokio::spawn(async move {
                            crate::fm::handle_fm_upload(id, payload, tx).await;
                        });
                    }
                    "fm_delete" => {
                        let id = frame.id;
                        let payload = frame.payload;
                        let tx = ws_tx_hi.clone();
                        tokio::spawn(async move {
                            crate::fm::handle_fm_delete(id, payload, tx).await;
                        });
                    }
                    "fm_mkdir" => {
                        let id = frame.id;
                        let payload = frame.payload;
                        let tx = ws_tx_hi.clone();
                        tokio::spawn(async move {
                            crate::fm::handle_fm_mkdir(id, payload, tx).await;
                        });
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
                let pong_msg = Message::Pong(data.clone());
                if ws_tx_hi.try_send(pong_msg).is_err() {
                    // Channel closed or full — spawn a one-shot task with
                    // a short timeout so the read loop stays unblocked.
                    let tx = ws_tx_hi.clone();
                    tokio::spawn(async move {
                        let _ = tokio::time::timeout(
                            Duration::from_secs(5),
                            tx.send(Message::Pong(data)),
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
