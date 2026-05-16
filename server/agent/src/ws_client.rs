// WebSocket reverse-connect client for NAT traversal (agent mode).
// The controller acts as WebSocket server; the agent connects back and handles
// exec_req / ping / info / tunnel_open frames.

use futures_util::{SinkExt, StreamExt};
use regex;
use serde::{Deserialize, Serialize};
use std::process::Stdio;
use std::sync::Arc;
use std::collections::HashMap;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::process::{Child, ChildStdin, Command};
use tokio::sync::{mpsc, Mutex, Semaphore};
use tokio_tungstenite::{connect_async, tungstenite::Message};
use tokio_tungstenite::tungstenite::{http::Uri, ClientRequestBuilder};
use tracing::{info, warn};
use url;

use crate::tunnel::{handle_binary_frame, handle_tunnel_close, handle_tunnel_open, SessionMap, WsFrame};

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

#[derive(Clone)]
struct ShellHandle {
    stdin: Arc<Mutex<ChildStdin>>,
    child: Arc<Mutex<Child>>,
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

    let mut delay_secs: u64 = 2;
    loop {
        info!(url = %clean_url, "connecting to controller via WebSocket");
        // Use ClientRequestBuilder which properly constructs a WebSocket
        // request from the URI (adding Host, Connection, Upgrade,
        // Sec-WebSocket-Key, Sec-WebSocket-Version) and THEN appends
        // custom headers.  Bare Request::builder() does NOT add WebSocket
        // headers and would cause a handshake failure.
        let uri: Uri = match clean_url.parse() {
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
                info!("WebSocket connected to controller");
                delay_secs = 2; // reset back-off on successful connect
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
        tokio::time::sleep(tokio::time::Duration::from_secs(delay_secs)).await;
        if delay_secs < 60 {
            delay_secs = (delay_secs * 2).min(60);
        }
    }
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
                    if write.send(msg).await.is_err() {
                        break;
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

    while let Some(msg_result) = read.next().await {
        let msg = match msg_result {
            Ok(m) => m,
            Err(e) => return Err(e.to_string()),
        };

        match msg {
            Message::Binary(data) => {
                handle_binary_frame(&data, &sessions).await;
            }
            Message::Text(text) => {
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
                            let _ = ws_tx_hi_clone.send(Message::Text(resp_text.into())).await;
                        });
                    }
                    "ping" => {
                        let pong = WsFrame {
                            msg_type: "pong".to_string(),
                            id: frame.id.clone(),
                            payload: None,
                        };
                        let pong_text = serde_json::to_string(&pong).map_err(|e| e.to_string())?;
                        ws_tx_hi.send(Message::Text(pong_text.into())).await.map_err(|e| e.to_string())?;
                    }
                    "tunnel_open" => {
                        if let Some(payload_val) = frame.payload {
                            let hi_clone = ws_tx_hi.clone();
                            let lo_clone = ws_tx_lo.clone();
                            let sess_clone = sessions.clone();
                            tokio::spawn(async move {
                                handle_tunnel_open(payload_val, hi_clone, lo_clone, sess_clone).await;
                            });
                        }
                    }
                    "tunnel_close" => {
                        if let Some(payload_val) = frame.payload {
                            handle_tunnel_close(payload_val, &sessions).await;
                        }
                    }
                    "shell_open" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        let payload = frame.payload.unwrap_or_default();
                        let ws_tx_hi_clone = ws_tx_hi.clone();
                        let shell_sessions_clone = shell_sessions.clone();
                        tokio::spawn(async move {
                            if let Err(err) = open_shell_session(req_id, payload, ws_tx_hi_clone, shell_sessions_clone).await {
                                warn!(error = %err, "failed to open shell session");
                            }
                        });
                    }
                    "shell_data" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        if let Some(payload_val) = frame.payload {
                            if let Ok(payload) = serde_json::from_value::<ShellDataPayload>(payload_val) {
                                if let Some(handle) = shell_sessions.lock().await.get(&req_id).cloned() {
                                    let mut stdin = handle.stdin.lock().await;
                                    let _ = stdin.write_all(payload.data.as_bytes()).await;
                                    let _ = stdin.flush().await;
                                }
                            }
                        }
                    }
                    "shell_resize" => {
                        // Current fallback shell ignores resize; keep message for protocol compatibility.
                    }
                    "shell_close" => {
                        let req_id = frame.id.clone().unwrap_or_default();
                        if let Some(handle) = shell_sessions.lock().await.remove(&req_id) {
                            let mut child = handle.child.lock().await;
                            let _ = child.kill().await;
                        }
                    }
                    other => {
                        info!(msg_type = %other, "received unhandled frame type");
                    }
                }
            }
            Message::Close(_) => {
                info!("received close frame from controller");
                return Ok(());
            }
            Message::Ping(data) => {
                ws_tx_hi.send(Message::Pong(data)).await.map_err(|e| e.to_string())?;
            }
            _ => {}
        }
    }

    Ok(())
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
    let _ = (payload.cols, payload.rows);

    let mut child = spawn_interactive_shell().await?;
    let stdin = child.stdin.take().ok_or_else(|| "missing shell stdin".to_string())?;
    let stdout = child.stdout.take().ok_or_else(|| "missing shell stdout".to_string())?;
    let stderr = child.stderr.take().ok_or_else(|| "missing shell stderr".to_string())?;

    let handle = ShellHandle {
        stdin: Arc::new(Mutex::new(stdin)),
        child: Arc::new(Mutex::new(child)),
    };
    shell_sessions.lock().await.insert(session_id.clone(), handle.clone());

    spawn_shell_reader(stdout, session_id.clone(), ws_tx.clone());
    spawn_shell_reader(stderr, session_id.clone(), ws_tx.clone());

    let shell_sessions_clone = shell_sessions.clone();
    tokio::spawn(async move {
        let status = {
            let mut child = handle.child.lock().await;
            child.wait().await.ok()
        };
        shell_sessions_clone.lock().await.remove(&session_id);
        let reason = status
            .and_then(|s| s.code().map(|code| format!("shell exited with code {}", code)))
            .unwrap_or_else(|| "shell closed".to_string());
        let close_frame = WsFrame {
            msg_type: "shell_close".to_string(),
            id: Some(session_id),
            payload: Some(serde_json::json!({ "reason": reason })),
        };
        if let Ok(text) = serde_json::to_string(&close_frame) {
            let _ = ws_tx.send(Message::Text(text.into())).await;
        }
    });

    Ok(())
}

fn spawn_shell_reader<R>(mut reader: R, session_id: String, ws_tx: mpsc::Sender<Message>)
where
    R: tokio::io::AsyncRead + Unpin + Send + 'static,
{
    tokio::spawn(async move {
        let mut buf = vec![0u8; 8192];
        loop {
            match reader.read(&mut buf).await {
                Ok(0) => break,
                Ok(n) => {
                    let frame = WsFrame {
                        msg_type: "shell_data".to_string(),
                        id: Some(session_id.clone()),
                        payload: Some(serde_json::json!({
                            "data": String::from_utf8_lossy(&buf[..n]).to_string()
                        })),
                    };
                    if let Ok(text) = serde_json::to_string(&frame) {
                        if ws_tx.send(Message::Text(text.into())).await.is_err() {
                            break;
                        }
                    }
                }
                Err(_) => break,
            }
        }
    });
}

async fn spawn_interactive_shell() -> Result<Child, String> {
    let mut scripted = Command::new("script");
    scripted
        .arg("-qfec")
        .arg("cd /root 2>/dev/null || cd \"$HOME\"; if command -v bash >/dev/null 2>&1; then exec bash -il; fi; exec sh -i")
        .arg("/dev/null")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    match scripted.spawn() {
        Ok(child) => Ok(child),
        Err(_) => {
            let mut plain = Command::new("bash");
            plain
                .arg("-il")
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped());
            match plain.spawn() {
                Ok(child) => Ok(child),
                Err(_) => {
                    let mut sh_plain = Command::new("sh");
                    sh_plain
                        .arg("-i")
                        .stdin(Stdio::piped())
                        .stdout(Stdio::piped())
                        .stderr(Stdio::piped());
                    sh_plain.spawn().map_err(|e| e.to_string())
                }
            }
        }
    }
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
