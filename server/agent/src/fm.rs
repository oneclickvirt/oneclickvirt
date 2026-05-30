// fm.rs — Agent 侧文件管理器实现。
// 处理来自控制端的 fm_list / fm_download / fm_upload / fm_delete / fm_mkdir 请求，
// 通过 WebSocket 控制通道返回文件操作结果。
//
// 协议与 Go 控制端 service/agent/fm.go 对称：
//   controller → agent: fm_list    { id, payload: {path} }
//   controller → agent: fm_download { id, payload: {path} }
//   controller → agent: fm_upload   { id, payload: {path, data(base64), size} }
//   controller → agent: fm_delete   { id, payload: {path} }
//   controller → agent: fm_mkdir    { id, payload: {path} }
//
//   agent → controller: fm_list_resp / fm_download_resp / fm_upload_resp /
//                        fm_delete_resp / fm_mkdir_resp  (成功)
//   agent → controller: fm_error   { id, payload: {message} }  (失败)

use base64::{engine::general_purpose::STANDARD as B64, Engine};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::path::Path;
use std::time::UNIX_EPOCH;
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::Message;
use tracing::{info, warn};

use crate::tunnel::WsFrame;

/// 最大下载文件大小（50 MB），保护 WebSocket 通道不被大文件阻塞。
const FM_MAX_DOWNLOAD_BYTES: u64 = 50 * 1024 * 1024;

// ── 本地 payload 结构体（仅在此模块使用）────────────────────────────────────

#[derive(Deserialize)]
struct FMListPayload { path: String }

#[derive(Deserialize)]
struct FMDownloadPayload { path: String }

#[derive(Deserialize)]
struct FMUploadPayload { path: String, data: String, size: i64 }

#[derive(Deserialize)]
struct FMDeletePayload { path: String }

#[derive(Deserialize)]
struct FMMkdirPayload { path: String }

#[derive(Serialize)]
struct FMEntry {
    name: String,
    #[serde(rename = "isDir")]
    is_dir: bool,
    size: i64,
    #[serde(rename = "modTime")]
    mod_time: i64,
    mode: String,
}

#[derive(Serialize)]
struct FMListRespPayload { path: String, entries: Vec<FMEntry> }

#[derive(Serialize)]
struct FMDownloadRespPayload { data: String, size: i64 }

#[derive(Serialize)]
struct FMOkRespPayload {}

#[derive(Serialize)]
struct FMErrorPayload { message: String }

// ── 辅助发送函数 ─────────────────────────────────────────────────────────────

fn send_fm_ok<P: Serialize>(ws_tx: &mpsc::Sender<Message>, resp_type: &str, req_id: &str, payload: &P) {
    let payload_val = match serde_json::to_value(payload) {
        Ok(v) => v,
        Err(e) => { warn!("FM serialize ok payload failed: {e}"); return; }
    };
    let frame = WsFrame { msg_type: resp_type.to_string(), id: Some(req_id.to_string()), payload: Some(payload_val) };
    if let Ok(text) = serde_json::to_string(&frame) {
        let _ = ws_tx.try_send(Message::Text(text.into()));
    }
}

fn send_fm_error(ws_tx: &mpsc::Sender<Message>, req_id: &str, msg: &str) {
    warn!("FM error (id={}): {}", req_id, msg);
    let payload_val = match serde_json::to_value(FMErrorPayload { message: msg.to_string() }) {
        Ok(v) => v,
        Err(_) => return,
    };
    let frame = WsFrame { msg_type: "fm_error".to_string(), id: Some(req_id.to_string()), payload: Some(payload_val) };
    if let Ok(text) = serde_json::to_string(&frame) {
        let _ = ws_tx.try_send(Message::Text(text.into()));
    }
}

// ── 公开处理函数（由 ws_client/handler.rs 调用）──────────────────────────────

/// 处理 fm_list 请求：列出目录内容
pub async fn handle_fm_list(id: Option<String>, payload: Option<Value>, ws_tx: mpsc::Sender<Message>) {
    let req_id = id.unwrap_or_default();
    let p = match payload.and_then(|v| serde_json::from_value::<FMListPayload>(v).ok()) {
        Some(p) => p,
        None => { send_fm_error(&ws_tx, &req_id, "invalid fm_list payload"); return; }
    };
    info!(id = %req_id, path = %p.path, "fm_list");

    let dir_path = if p.path.is_empty() { "/".to_string() } else { p.path };

    // 尝试读目录；若失败，回退到 HOME
    let actual_path = match tokio::fs::metadata(&dir_path).await {
        Ok(m) if m.is_dir() => dir_path,
        _ => std::env::var("HOME").unwrap_or_else(|_| "/root".to_string()),
    };

    let mut read_dir = match tokio::fs::read_dir(&actual_path).await {
        Ok(rd) => rd,
        Err(e) => { send_fm_error(&ws_tx, &req_id, &format!("read dir failed: {e}")); return; }
    };

    let mut entries = Vec::new();
    while let Ok(Some(entry)) = read_dir.next_entry().await {
        let name = entry.file_name().to_string_lossy().to_string();
        if let Ok(meta) = entry.metadata().await {
            let mod_time = meta.modified().ok()
                .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
                .map(|d| d.as_millis() as i64)
                .unwrap_or(0);
            let mode = {
                #[cfg(unix)]
                {
                    use std::os::unix::fs::PermissionsExt;
                    format!("{:o}", meta.permissions().mode())
                }
                #[cfg(not(unix))]
                { "0000".to_string() }
            };
            entries.push(FMEntry { name, is_dir: meta.is_dir(), size: meta.len() as i64, mod_time, mode });
        }
    }

    send_fm_ok(&ws_tx, "fm_list_resp", &req_id, &FMListRespPayload { path: actual_path, entries });
}

/// 处理 fm_download 请求：读取文件内容并以 base64 返回
pub async fn handle_fm_download(id: Option<String>, payload: Option<Value>, ws_tx: mpsc::Sender<Message>) {
    let req_id = id.unwrap_or_default();
    let p = match payload.and_then(|v| serde_json::from_value::<FMDownloadPayload>(v).ok()) {
        Some(p) => p,
        None => { send_fm_error(&ws_tx, &req_id, "invalid fm_download payload"); return; }
    };
    info!(id = %req_id, path = %p.path, "fm_download");

    let meta = match tokio::fs::metadata(&p.path).await {
        Ok(m) => m,
        Err(e) => { send_fm_error(&ws_tx, &req_id, &format!("stat failed: {e}")); return; }
    };
    if meta.is_dir() { send_fm_error(&ws_tx, &req_id, "cannot download a directory"); return; }
    if meta.len() > FM_MAX_DOWNLOAD_BYTES { send_fm_error(&ws_tx, &req_id, "file too large (max 50 MB)"); return; }

    let data = match tokio::fs::read(&p.path).await {
        Ok(d) => d,
        Err(e) => { send_fm_error(&ws_tx, &req_id, &format!("read failed: {e}")); return; }
    };
    let size = data.len() as i64;
    send_fm_ok(&ws_tx, "fm_download_resp", &req_id, &FMDownloadRespPayload { data: B64.encode(&data), size });
}

/// 处理 fm_upload 请求：解码 base64 写入指定路径
pub async fn handle_fm_upload(id: Option<String>, payload: Option<Value>, ws_tx: mpsc::Sender<Message>) {
    let req_id = id.unwrap_or_default();
    let p = match payload.and_then(|v| serde_json::from_value::<FMUploadPayload>(v).ok()) {
        Some(p) => p,
        None => { send_fm_error(&ws_tx, &req_id, "invalid fm_upload payload"); return; }
    };
    info!(id = %req_id, path = %p.path, size = %p.size, "fm_upload");

    let data = match B64.decode(&p.data) {
        Ok(d) => d,
        Err(e) => { send_fm_error(&ws_tx, &req_id, &format!("base64 decode failed: {e}")); return; }
    };
    if let Some(parent) = Path::new(&p.path).parent() {
        if !parent.as_os_str().is_empty() {
            if let Err(e) = tokio::fs::create_dir_all(parent).await {
                send_fm_error(&ws_tx, &req_id, &format!("create parent dir failed: {e}"));
                return;
            }
        }
    }
    match tokio::fs::write(&p.path, &data).await {
        Ok(_) => send_fm_ok(&ws_tx, "fm_upload_resp", &req_id, &FMOkRespPayload {}),
        Err(e) => send_fm_error(&ws_tx, &req_id, &format!("write failed: {e}")),
    }
}

/// 处理 fm_delete 请求：删除文件或空目录
pub async fn handle_fm_delete(id: Option<String>, payload: Option<Value>, ws_tx: mpsc::Sender<Message>) {
    let req_id = id.unwrap_or_default();
    let p = match payload.and_then(|v| serde_json::from_value::<FMDeletePayload>(v).ok()) {
        Some(p) => p,
        None => { send_fm_error(&ws_tx, &req_id, "invalid fm_delete payload"); return; }
    };
    info!(id = %req_id, path = %p.path, "fm_delete");

    let meta = match tokio::fs::metadata(&p.path).await {
        Ok(m) => m,
        Err(e) => { send_fm_error(&ws_tx, &req_id, &format!("stat failed: {e}")); return; }
    };
    let result = if meta.is_dir() { tokio::fs::remove_dir(&p.path).await } else { tokio::fs::remove_file(&p.path).await };
    match result {
        Ok(_) => send_fm_ok(&ws_tx, "fm_delete_resp", &req_id, &FMOkRespPayload {}),
        Err(e) => send_fm_error(&ws_tx, &req_id, &format!("delete failed: {e}")),
    }
}

/// 处理 fm_mkdir 请求：递归创建目录
pub async fn handle_fm_mkdir(id: Option<String>, payload: Option<Value>, ws_tx: mpsc::Sender<Message>) {
    let req_id = id.unwrap_or_default();
    let p = match payload.and_then(|v| serde_json::from_value::<FMMkdirPayload>(v).ok()) {
        Some(p) => p,
        None => { send_fm_error(&ws_tx, &req_id, "invalid fm_mkdir payload"); return; }
    };
    info!(id = %req_id, path = %p.path, "fm_mkdir");

    match tokio::fs::create_dir_all(&p.path).await {
        Ok(_) => send_fm_ok(&ws_tx, "fm_mkdir_resp", &req_id, &FMOkRespPayload {}),
        Err(e) => send_fm_error(&ws_tx, &req_id, &format!("mkdir failed: {e}")),
    }
}


