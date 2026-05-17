// tunnel.rs — Agent 侧隧道处理器。
//
// 当收到控制端 tunnel_open 帧时，本模块在 Agent 本地连接目标 TCP 地址，
// 并在 WebSocket（共享写半边）与本地 TCP 之间进行双向数据转发。
//
// 协议（与 Go 侧 tunnel.go 对称）：
//   Controller → Agent (text JSON): tunnel_open  { id, host, port }
//   Agent → Controller (text JSON): tunnel_ack   { id, ok, error? }
//   Agent → Controller (text JSON): tunnel_close { id }
//   双向二进制帧: [8-byte FNV hash of connID][TCP data]
//
// Anti-DPI: buffer size varies per read (8KB-64KB), occasional micro-delays
// (0-3ms, ~20% probability) to break fixed-size/fixed-interval signatures.

use rand;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::sync::mpsc;
use tokio::sync::Mutex;
use tokio_tungstenite::tungstenite::Message;
use tracing::{info, warn};

const TUNNEL_KEEPALIVE_INTERVAL_SECS: u64 = 30;

/// 一个会话对应的 TCP 写入端（Agent 侧接收控制端的二进制数据并写入本地 TCP）。
type SessionTx = mpsc::Sender<Vec<u8>>;

/// 全局会话表：connHash → Sender
pub type SessionMap = Arc<Mutex<HashMap<u64, SessionTx>>>;

/// 发送 WebSocket 消息的通道（避免 trait object 复杂性）
pub type WsSink = mpsc::Sender<Message>;

/// 通用帧 envelope（与 ws_client.rs 中的 WsFrame 相同）
#[derive(Serialize, Deserialize, Debug)]
pub struct WsFrame {
    #[serde(rename = "type")]
    pub msg_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub payload: Option<serde_json::Value>,
}

#[derive(Deserialize, Debug)]
struct TunnelOpenPayload {
    id: String,
    host: String,
    port: u16,
}

#[derive(Serialize)]
struct TunnelAckPayload {
    id: String,
    ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

#[derive(Serialize)]
struct TunnelClosePayload {
    id: String,
}

#[derive(Serialize)]
struct TunnelKeepalivePayload {
    id: String,
}

/// FNV-1a 64 位 hash（与 Go 侧 hashString 一致）
fn fnv1a_64(s: &str) -> u64 {
    let mut h: u64 = 14695981039346656037;
    for b in s.bytes() {
        h ^= b as u64;
        h = h.wrapping_mul(1099511628211);
    }
    h
}



/// 处理 tunnel_open 帧：建立本地 TCP 连接并开始转发。
/// `hi_sink` — high-priority control channel (tunnel_ack / tunnel_close).
/// `data_sink` — low-priority bulk channel (binary data frames).
pub async fn handle_tunnel_open(
    payload_val: serde_json::Value,
    hi_sink: WsSink,
    data_sink: WsSink,
    sessions: SessionMap,
) {
    let payload: TunnelOpenPayload = match serde_json::from_value(payload_val) {
        Ok(p) => p,
        Err(e) => {
            warn!(error = %e, "failed to parse tunnel_open payload");
            return;
        }
    };

    let conn_id = payload.id.clone();
    let conn_hash = fnv1a_64(&conn_id);
    let addr = format!("{}:{}", payload.host, payload.port);

    info!(conn_id = %conn_id, addr = %addr, "opening tunnel to target");

    // 连接目标
    let tcp = match TcpStream::connect(&addr).await {
        Ok(s) => s,
        Err(e) => {
            warn!(conn_id = %conn_id, addr = %addr, error = %e, "failed to connect tunnel target");
            send_ack(hi_sink.clone(), &conn_id, false, Some(e.to_string())).await;
            return;
        }
    };

    // 回复 ack (hi-priority control channel)
    send_ack(hi_sink.clone(), &conn_id, true, None).await;

    let (mut tcp_rx, mut tcp_tx) = tcp.into_split();

    // 控制端 → Agent（二进制帧 → TCP）
    let (data_tx, mut data_rx) = mpsc::channel::<Vec<u8>>(64);
    {
        let mut map = sessions.lock().await;
        map.insert(conn_hash, data_tx);
    }

    // Agent → 控制端（TCP → 二进制帧 → lo-priority data channel）
    // Anti-DPI: vary read buffer (8KB-64KB), occasional micro-delay (20% prob).
    let data_sink_clone = data_sink.clone();
    let hi_sink_keepalive = hi_sink.clone();
    let conn_id_clone = conn_id.clone();
    let sessions_clone = sessions.clone();
    tokio::spawn(async move {
        let header_arr = conn_hash.to_be_bytes();
        loop {
            let buf_size = 8192 + (rand::random::<usize>() % 57344); // 8KB-64KB
            let mut buf = vec![0u8; buf_size];
            match tcp_rx.read(&mut buf).await {
                Ok(0) | Err(_) => break,
                Ok(n) => {
                    let mut frame = Vec::with_capacity(8 + n);
                    frame.extend_from_slice(&header_arr);
                    frame.extend_from_slice(&buf[..n]);
                    if data_sink_clone.send(Message::Binary(frame.into())).await.is_err() {
                        break;
                    }
                    // Occasional micro-delay (0-3ms, ~20% probability)
                    if rand::random::<u8>() % 5 == 0 {
                        let us = (rand::random::<u64>() % 3000) as u64;
                        tokio::time::sleep(std::time::Duration::from_micros(us)).await;
                    }
                }
            }
        }
        // TCP 读完后发 tunnel_close (hi-priority control channel)
        let close_payload = TunnelClosePayload { id: conn_id_clone.clone() };
        if let Ok(body) = serde_json::to_string(&WsFrame {
            msg_type: "tunnel_close".to_string(),
            id: None,
            payload: serde_json::to_value(close_payload).ok(),
        }) {
            let _ = hi_sink.send(Message::Text(body.into())).await;
        }
        let mut map = sessions_clone.lock().await;
        map.remove(&conn_hash);
    });

    let sessions_keepalive = sessions.clone();
    let conn_id_keepalive = conn_id.clone();
    tokio::spawn(async move {
        let mut ticker = tokio::time::interval(std::time::Duration::from_secs(
            TUNNEL_KEEPALIVE_INTERVAL_SECS,
        ));
        loop {
            ticker.tick().await;
            {
                let map = sessions_keepalive.lock().await;
                if !map.contains_key(&conn_hash) {
                    break;
                }
            }
            let keepalive_payload = TunnelKeepalivePayload {
                id: conn_id_keepalive.clone(),
            };
            if let Ok(body) = serde_json::to_string(&WsFrame {
                msg_type: "tunnel_keepalive".to_string(),
                id: None,
                payload: serde_json::to_value(keepalive_payload).ok(),
            }) {
                if hi_sink_keepalive.send(Message::Text(body.into())).await.is_err() {
                    break;
                }
            }
        }
    });

    // 控制端 → Agent 数据写入 TCP
    let sessions_clone2 = sessions.clone();
    tokio::spawn(async move {
        while let Some(data) = data_rx.recv().await {
            if tcp_tx.write_all(&data).await.is_err() {
                break;
            }
        }
        let mut map = sessions_clone2.lock().await;
        map.remove(&conn_hash);
    });
}

/// 处理来自控制端的二进制帧（路由到对应 session）
pub async fn handle_binary_frame(data: &[u8], sessions: &SessionMap) {
    if data.len() <= 8 {
        return;
    }
    let hash = u64::from_be_bytes(data[..8].try_into().unwrap_or([0; 8]));
    let payload = data[8..].to_vec();
    let map = sessions.lock().await;
    if let Some(tx) = map.get(&hash) {
        let _ = tx.try_send(payload);
    }
}

/// 处理 tunnel_close 帧（移除 session）
pub async fn handle_tunnel_close(payload_val: serde_json::Value, sessions: &SessionMap) {
    #[derive(Deserialize)]
    struct ClosePayload {
        id: String,
    }
    if let Ok(p) = serde_json::from_value::<ClosePayload>(payload_val) {
        let hash = fnv1a_64(&p.id);
        let mut map = sessions.lock().await;
        map.remove(&hash);
    }
}

pub async fn handle_tunnel_keepalive(_payload_val: serde_json::Value, _sessions: &SessionMap) {
    // Session-level keepalive: no-op on agent side.
}

async fn send_ack(ws_sink: WsSink, conn_id: &str, ok: bool, error: Option<String>) {
    let ack = TunnelAckPayload {
        id: conn_id.to_string(),
        ok,
        error,
    };
    let frame = WsFrame {
        msg_type: "tunnel_ack".to_string(),
        id: None,
        payload: serde_json::to_value(ack).ok(),
    };
    if let Ok(text) = serde_json::to_string(&frame) {
        let _ = ws_sink.send(Message::Text(text.into())).await;
    }
}
