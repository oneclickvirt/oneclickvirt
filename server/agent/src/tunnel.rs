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

use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::sync::Mutex;
use tokio::sync::{OwnedSemaphorePermit, mpsc, watch};
use tokio_tungstenite::tungstenite::Message;
use tracing::warn;

const TUNNEL_KEEPALIVE_INTERVAL_SECS: u64 = 30;
/// Timeout for TCP connect to target — prevents indefinite hang when target is
/// unreachable during rapid toggle sequences.
const TUNNEL_CONNECT_TIMEOUT_SECS: u64 = 10;
/// Timeout for sending control frames (ack / close) via the hi-priority channel.
/// Uses try_send first; falls back to timed send to avoid blocking the tunnel
/// task indefinitely when the write path is congested.
const TUNNEL_ACK_SEND_TIMEOUT_SECS: u64 = 5;

/// 一个会话对应的 TCP 写入端（Agent 侧接收控制端的二进制数据并写入本地 TCP）。
pub struct TunnelSession {
    id: String,
    data: mpsc::Sender<Vec<u8>>,
    cancel: watch::Sender<bool>,
    ready: std::sync::atomic::AtomicBool,
}
impl TunnelSession {
    pub fn cancel(&self) {
        self.cancel.send_replace(true);
    }
}

/// 全局会话表：connHash → Sender
pub type SessionMap = Arc<Mutex<HashMap<u64, Arc<TunnelSession>>>>;

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

/// FNV-1a 64 位 hash（与 Go 侧 hashString 一致）
fn fnv1a_64(s: &str) -> u64 {
    let mut h: u64 = 14695981039346656037;
    for b in s.bytes() {
        h ^= b as u64;
        h = h.wrapping_mul(1099511628211);
    }
    h
}

async fn send_ack_with_timeout(
    ws_sink: &WsSink,
    conn_id: &str,
    ok: bool,
    error: Option<String>,
) -> bool {
    let ack = TunnelAckPayload {
        id: conn_id.to_string(),
        ok,
        error: error.filter(|e| !e.is_empty()),
    };
    let frame = WsFrame {
        msg_type: "tunnel_ack".to_string(),
        id: None,
        payload: serde_json::to_value(ack).ok(),
    };
    let text = match serde_json::to_string(&frame) {
        Ok(t) => t,
        Err(e) => {
            warn!(conn_id = %conn_id, error = %e, "failed to serialize tunnel_ack");
            return false;
        }
    };
    matches!(
        tokio::time::timeout(
            std::time::Duration::from_secs(TUNNEL_ACK_SEND_TIMEOUT_SECS),
            ws_sink.send(Message::Text(text)),
        )
        .await,
        Ok(Ok(()))
    )
}

/// Reject a tunnel_open frame before a full TCP session is created.
///
/// This is used when the agent is locally saturated (for example, tunnel
/// concurrency permits are exhausted). Sending an explicit negative ACK lets the
/// controller close the accepted client connection immediately instead of
/// waiting for the controller-side ACK timeout and retrying a request that the
/// agent already knows it cannot accept.
pub async fn reject_tunnel_open(payload_val: serde_json::Value, hi_sink: WsSink, reason: &str) {
    let conn_id = payload_val
        .get("id")
        .and_then(|v| v.as_str())
        .unwrap_or_default()
        .to_string();
    if conn_id.is_empty() {
        warn!(reason, "cannot reject tunnel_open without id");
        return;
    }
    if !send_ack_with_timeout(&hi_sink, &conn_id, false, Some(reason.to_string())).await {
        warn!(conn_id = %conn_id, reason, "failed to reject tunnel_open");
    }
}

/// Reserve the ID before returning to the WebSocket reader. The owned task
/// connects and forwards; close/disconnect can cancel it even before first poll.
pub async fn handle_tunnel_open(
    payload_val: serde_json::Value,
    hi_sink: WsSink,
    data_sink: WsSink,
    sessions: SessionMap,
    permit: Option<OwnedSemaphorePermit>,
) {
    let payload: TunnelOpenPayload =
        match serde_json::from_value::<TunnelOpenPayload>(payload_val.clone()) {
            Ok(p) if !p.id.is_empty() => p,
            _ => {
                reject_tunnel_open(payload_val, hi_sink, "invalid tunnel_open payload").await;
                return;
            }
        };
    let hash = fnv1a_64(&payload.id);
    let (tx, rx) = mpsc::channel(64);
    let (cancel, cancelled) = watch::channel(false);
    let session = Arc::new(TunnelSession {
        id: payload.id.clone(),
        data: tx,
        cancel,
        ready: std::sync::atomic::AtomicBool::new(false),
    });
    {
        let mut map = sessions.lock().await;
        if let Some(existing) = map.get(&hash) {
            let same_id = existing.id == payload.id;
            let ready = existing.ready.load(std::sync::atomic::Ordering::Acquire);
            drop(map);
            // Pending duplicates share the original attempt and its eventual ACK.
            if !same_id {
                send_ack_with_timeout(
                    &hi_sink,
                    &payload.id,
                    false,
                    Some("session hash collision".into()),
                )
                .await;
            } else if ready {
                send_ack_with_timeout(&hi_sink, &payload.id, true, None).await;
            }
            return;
        }
        if permit.is_none() {
            drop(map);
            reject_tunnel_open(payload_val, hi_sink, "tunnel permit exhausted").await;
            return;
        }
        map.insert(hash, session.clone());
    }
    tokio::spawn(async move {
        let mut cancelled = cancelled;
        let work = run_tunnel(&payload, hash, &session, &hi_sink, &data_sink, rx, permit);
        tokio::pin!(work);
        tokio::select! {
            biased;
            _ = async {
                if !*cancelled.borrow_and_update() { let _ = cancelled.changed().await; }
            } => {}
            _ = &mut work => {}
        }
        session.cancel();
        let mut map = sessions.lock().await;
        if map
            .get(&hash)
            .is_some_and(|current| Arc::ptr_eq(current, &session))
            && let Some(session) = map.remove(&hash)
        {
            session.cancel();
        }
    });
}

async fn run_tunnel(
    payload: &TunnelOpenPayload,
    hash: u64,
    session: &TunnelSession,
    hi_sink: &WsSink,
    data_sink: &WsSink,
    mut incoming: mpsc::Receiver<Vec<u8>>,
    permit: Option<OwnedSemaphorePermit>,
) {
    // Tuple addressing also accepts raw IPv6 without double brackets.
    let connected = tokio::time::timeout(
        std::time::Duration::from_secs(TUNNEL_CONNECT_TIMEOUT_SECS),
        TcpStream::connect((payload.host.as_str(), payload.port)),
    )
    .await;
    let tcp = match connected {
        Ok(Ok(tcp)) => tcp,
        result => {
            let reason = match result {
                Ok(Err(e)) => e.to_string(),
                _ => "tunnel connect timeout".to_string(),
            };
            send_ack_with_timeout(hi_sink, &payload.id, false, Some(reason)).await;
            return;
        }
    };
    if !send_ack_with_timeout(hi_sink, &payload.id, true, None).await {
        return;
    }
    session
        .ready
        .store(true, std::sync::atomic::Ordering::Release);
    // This budget limits concurrent TCP opens, not established forwarding
    // sessions. Keep the original capacity semantics for busy providers.
    drop(permit);
    let (mut reader, mut writer) = tcp.into_split();
    // Both I/O futures belong to this task. Dropping the losing future closes
    // its TCP half, including a blocked read/write on close or disconnect.
    let outgoing = async {
        loop {
            let mut buf = vec![0u8; 8192 + rand::random::<usize>() % 57344];
            let n = match reader.read(&mut buf).await {
                Ok(0) | Err(_) => break,
                Ok(n) => n,
            };
            let mut frame = Vec::with_capacity(8 + n);
            frame.extend_from_slice(&hash.to_be_bytes());
            frame.extend_from_slice(&buf[..n]);
            if !matches!(
                tokio::time::timeout(
                    std::time::Duration::from_secs(15),
                    data_sink.send(Message::Binary(frame)),
                )
                .await,
                Ok(Ok(()))
            ) {
                break;
            }
            if rand::random::<u8>().is_multiple_of(5) {
                tokio::time::sleep(std::time::Duration::from_micros(
                    rand::random::<u64>() % 3000,
                ))
                .await;
            }
        }
    };
    let inbound = async {
        while let Some(data) = incoming.recv().await {
            if data.is_empty() {
                if writer.shutdown().await.is_err() {
                    break;
                }
                // Peer half-closed its input; keep the response reader alive.
                std::future::pending::<()>().await;
            }
            if writer.write_all(&data).await.is_err() {
                break;
            }
        }
    };
    let keepalive = async {
        let mut ticker = tokio::time::interval(std::time::Duration::from_secs(
            TUNNEL_KEEPALIVE_INTERVAL_SECS,
        ));
        loop {
            ticker.tick().await;
            let frame = serde_json::json!({"type":"tunnel_keepalive","payload":{"id":payload.id}});
            // A skipped keepalive under backpressure is not a broken transport.
            let _ = hi_sink.try_send(Message::Text(frame.to_string()));
        }
    };
    tokio::select! { _ = outgoing => {}, _ = inbound => {}, _ = keepalive => {} }
    // The terminal frame MUST share the data FIFO: hi-priority close can
    // otherwise overtake the final TCP response already queued in data_sink.
    let close = serde_json::json!({"type":"tunnel_close","payload":{"id":payload.id}});
    let _ = tokio::time::timeout(
        std::time::Duration::from_secs(15),
        data_sink.send(Message::Text(close.to_string())),
    )
    .await;
}

/// 处理来自控制端的二进制帧（路由到对应 session）
pub async fn handle_binary_frame(data: &[u8], sessions: &SessionMap) {
    if data.len() <= 8 {
        return;
    }
    let hash = u64::from_be_bytes(data[..8].try_into().unwrap_or([0; 8]));
    let payload = data[8..].to_vec();
    let mut map = sessions.lock().await;
    if let Some(session) = map.get(&hash) {
        match session.data.try_send(payload) {
            Ok(()) => {}
            Err(mpsc::error::TrySendError::Closed(_)) => {
                if let Some(session) = map.remove(&hash) {
                    session.cancel();
                }
            }
            Err(mpsc::error::TrySendError::Full(_)) => {
                warn!(
                    conn_hash = hash,
                    "tunnel inbound buffer full, closing session"
                );
                if let Some(session) = map.remove(&hash) {
                    session.cancel();
                }
            }
        }
    }
}

/// 处理 tunnel_close 帧（移除 session）。
/// 显式唤醒连接和 I/O 的取消分支，不等待下一次 TCP 数据。
pub async fn handle_tunnel_close(payload_val: serde_json::Value, sessions: &SessionMap) {
    #[derive(Deserialize)]
    struct ClosePayload {
        id: String,
    }
    if let Ok(p) = serde_json::from_value::<ClosePayload>(payload_val) {
        let hash = fnv1a_64(&p.id);
        let mut map = sessions.lock().await;
        // The wire frame carries the full ID, while the map is indexed by a
        // compact hash for binary routing. Verify the ID before removal so a
        // hash collision or stale close frame cannot terminate another
        // tunnel session.
        if map.get(&hash).is_some_and(|session| session.id == p.id)
            && let Some(session) = map.remove(&hash)
        {
            session.cancel();
        }
    }
}

pub async fn handle_tunnel_keepalive(_payload_val: serde_json::Value, _sessions: &SessionMap) {
    // Session-level keepalive: no-op on agent side.
}

pub async fn handle_tunnel_eof(payload: serde_json::Value, sessions: &SessionMap) {
    if let Some(id) = payload.get("id").and_then(|id| id.as_str()) {
        let map = sessions.lock().await;
        if let Some(session) = map.get(&fnv1a_64(id))
            && session.id == id
            && session.data.try_send(Vec::new()).is_err()
        {
            session.cancel();
        }
    }
}

#[cfg(test)]
mod lifecycle_tests {
    use super::*;
    use std::time::Duration;
    use tokio::sync::Semaphore;

    #[tokio::test]
    async fn close_before_connect_task_runs_cancels_reservation() {
        let sessions = Arc::new(Mutex::new(HashMap::new()));
        let permits = Arc::new(Semaphore::new(1));
        let (hi, _hi_rx) = mpsc::channel(8);
        let (lo, _lo_rx) = mpsc::channel(8);
        handle_tunnel_open(
            serde_json::json!({"id":"a","host":"127.0.0.1","port":9}),
            hi,
            lo,
            sessions.clone(),
            Some(permits.clone().try_acquire_owned().unwrap()),
        )
        .await;
        assert_eq!(sessions.lock().await.len(), 1);
        handle_tunnel_close(serde_json::json!({"id":"a"}), &sessions).await;
        tokio::time::timeout(Duration::from_secs(1), async {
            while permits.available_permits() != 1 {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        assert!(sessions.lock().await.is_empty());
    }

    #[tokio::test]
    async fn tcp_tail_precedes_close_and_releases_session_permit() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let peer = tokio::spawn(async move {
            let (mut tcp, _) = listener.accept().await.unwrap();
            tcp.write_all(b"final response").await.unwrap();
            tcp.shutdown().await.unwrap();
        });
        let sessions = Arc::new(Mutex::new(HashMap::new()));
        let permits = Arc::new(Semaphore::new(1));
        let (hi, mut hi_rx) = mpsc::channel(8);
        let (lo, mut lo_rx) = mpsc::channel(8);
        handle_tunnel_open(
            serde_json::json!({"id":"tail","host":"127.0.0.1","port":port}),
            hi,
            lo,
            sessions.clone(),
            Some(permits.clone().try_acquire_owned().unwrap()),
        )
        .await;
        let ack = tokio::time::timeout(Duration::from_secs(1), hi_rx.recv())
            .await
            .unwrap()
            .unwrap();
        assert!(ack.to_text().unwrap().contains("tunnel_ack"));
        let mut bytes = Vec::new();
        loop {
            let frame = tokio::time::timeout(Duration::from_secs(1), lo_rx.recv())
                .await
                .unwrap()
                .unwrap();
            match frame {
                Message::Binary(data) => bytes.extend_from_slice(&data[8..]),
                Message::Text(text) => {
                    assert!(text.contains("tunnel_close"));
                    break;
                }
                _ => panic!("unexpected tunnel frame"),
            }
        }
        assert_eq!(bytes, b"final response");
        peer.await.unwrap();
        tokio::time::timeout(Duration::from_secs(1), async {
            while permits.available_permits() != 1 {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        assert!(sessions.lock().await.is_empty());
    }

    #[tokio::test]
    async fn stale_or_colliding_close_id_cannot_remove_another_session() {
        let sessions = Arc::new(Mutex::new(HashMap::new()));
        let (data, _data_rx) = mpsc::channel(1);
        let (cancel, _cancelled) = watch::channel(false);
        let session = Arc::new(TunnelSession {
            id: "real-session".to_string(),
            data,
            cancel,
            ready: std::sync::atomic::AtomicBool::new(true),
        });

        // Model a hash collision/stale map entry without relying on finding a
        // natural FNV collision in the test process.
        sessions
            .lock()
            .await
            .insert(fnv1a_64("forged-session"), session.clone());
        handle_tunnel_close(serde_json::json!({"id":"forged-session"}), &sessions).await;
        assert!(
            sessions
                .lock()
                .await
                .values()
                .any(|item| Arc::ptr_eq(item, &session))
        );
        sessions.lock().await.clear();
    }
}
