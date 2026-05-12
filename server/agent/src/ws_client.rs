// WebSocket reverse-connect client for NAT traversal (agent mode).
// The controller acts as WebSocket server; the agent connects back and handles
// exec_req / ping / info / tunnel_open frames.

use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use std::process::Stdio;
use std::sync::Arc;
use std::collections::HashMap;
use tokio::process::Command;
use tokio::sync::{mpsc, Mutex};
use tokio_tungstenite::{connect_async, tungstenite::Message};
use tracing::{info, warn};

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
}

/// Run the WebSocket reverse-connect loop.
/// Reconnects automatically with exponential back-off (max 60 s).
pub async fn run_ws_client(ws_url: String) {
    let mut delay_secs: u64 = 2;
    loop {
        info!(url = %ws_url, "connecting to controller via WebSocket");
        match connect_async(&ws_url).await {
            Ok((ws_stream, _)) => {
                info!("WebSocket connected to controller");
                delay_secs = 2; // reset back-off on successful connect
                if let Err(e) = handle_connection(ws_stream).await {
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

async fn handle_connection<S>(ws_stream: tokio_tungstenite::WebSocketStream<S>) -> Result<(), String>
where
    S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
{
    let (mut write, mut read) = ws_stream.split();

    // mpsc channel — tunnel sessions send outgoing WS frames here
    let (ws_tx, mut ws_rx) = mpsc::channel::<Message>(256);

    // Forward ws_rx → ws write half in a separate task
    tokio::spawn(async move {
        while let Some(msg) = ws_rx.recv().await {
            if write.send(msg).await.is_err() {
                break;
            }
        }
    });

    // Shared session map for tunnel routing
    let sessions: SessionMap = Arc::new(Mutex::new(HashMap::new()));

    // Send initial info frame
    let hostname = std::fs::read_to_string("/etc/hostname")
        .map(|s| s.trim().to_string())
        .unwrap_or_else(|_| std::env::var("HOSTNAME").unwrap_or_else(|_| "unknown".to_string()));

    let info_frame = WsFrame {
        msg_type: "info".to_string(),
        id: None,
        payload: Some(serde_json::to_value(InfoPayload {
            hostname,
            version: env!("CARGO_PKG_VERSION").to_string(),
        }).unwrap()),
    };
    let info_text = serde_json::to_string(&info_frame).map_err(|e| e.to_string())?;
    ws_tx.send(Message::Text(info_text.into())).await.map_err(|e| e.to_string())?;

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

                        let output = Command::new("sh")
                            .arg("-c")
                            .arg(&cmd)
                            .stdout(Stdio::piped())
                            .stderr(Stdio::piped())
                            .output()
                            .await;

                        let resp_payload = match output {
                            Ok(out) => ExecRespPayload {
                                stdout: String::from_utf8_lossy(&out.stdout).to_string(),
                                stderr: String::from_utf8_lossy(&out.stderr).to_string(),
                                exit_code: out.status.code().unwrap_or(-1),
                            },
                            Err(e) => ExecRespPayload {
                                stdout: String::new(),
                                stderr: e.to_string(),
                                exit_code: -1,
                            },
                        };

                        let resp_frame = WsFrame {
                            msg_type: "exec_resp".to_string(),
                            id: Some(req_id),
                            payload: Some(serde_json::to_value(resp_payload).unwrap()),
                        };
                        let resp_text = serde_json::to_string(&resp_frame).map_err(|e| e.to_string())?;
                        ws_tx.send(Message::Text(resp_text.into())).await.map_err(|e| e.to_string())?;
                    }
                    "ping" => {
                        let pong = WsFrame {
                            msg_type: "pong".to_string(),
                            id: frame.id.clone(),
                            payload: None,
                        };
                        let pong_text = serde_json::to_string(&pong).map_err(|e| e.to_string())?;
                        ws_tx.send(Message::Text(pong_text.into())).await.map_err(|e| e.to_string())?;
                    }
                    "tunnel_open" => {
                        if let Some(payload_val) = frame.payload {
                            let sink_clone = ws_tx.clone();
                            let sess_clone = sessions.clone();
                            tokio::spawn(async move {
                                handle_tunnel_open(payload_val, sink_clone, sess_clone).await;
                            });
                        }
                    }
                    "tunnel_close" => {
                        if let Some(payload_val) = frame.payload {
                            handle_tunnel_close(payload_val, &sessions).await;
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
                ws_tx.send(Message::Pong(data)).await.map_err(|e| e.to_string())?;
            }
            _ => {}
        }
    }

    Ok(())
}
