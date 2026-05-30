package agent

// fm.go — Agent File Manager 服务层。
// 通过 WebSocket 控制通道向 Agent 发送 FM 请求，获取文件列表、下载/上传/删除文件、创建目录。
//
// 协议（与 agent 侧 ws_client/handler.rs 中 FM 处理对称）：
//   Controller → Agent (text JSON): fm_list    { id, payload: { path } }
//   Controller → Agent (text JSON): fm_download { id, payload: { path } }
//   Controller → Agent (text JSON): fm_upload   { id, payload: { path, data(base64), size } }
//   Controller → Agent (text JSON): fm_delete   { id, payload: { path } }
//   Controller → Agent (text JSON): fm_mkdir    { id, payload: { path } }
//   Agent → Controller (text JSON): fm_list_resp / fm_download_resp / fm_upload_resp /
//                                    fm_delete_resp / fm_mkdir_resp / fm_error

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

const (
	fmDefaultTimeout = 60 * time.Second
	fmUploadTimeout  = 120 * time.Second
	// 单次上传文件大小上限（50 MB），超过此大小拒绝请求以保护 WebSocket 通道。
	fmMaxUploadBytes = 50 * 1024 * 1024
)

// sendFMRequest 向 Agent 发送 FM 请求帧，并等待响应。
// 返回 (响应消息类型, 原始 payload, error)。
func (a *AgentConn) sendFMRequest(msgType string, payload interface{}, timeout time.Duration) (string, json.RawMessage, error) {
	reqID := randomID()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("序列化 FM 请求失败: %w", err)
	}

	msg := wsMessage{
		Type:    msgType,
		ID:      reqID,
		Payload: payloadBytes,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return "", nil, fmt.Errorf("序列化 FM 消息失败: %w", err)
	}

	respCh := make(chan fmRawResp, 1)
	a.mu.Lock()
	// 检查连接是否已断开
	select {
	case <-a.doneCh:
		a.mu.Unlock()
		return "", nil, fmt.Errorf("Agent 已断开连接")
	default:
	}
	a.fmPending[reqID] = respCh
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.fmPending, reqID)
		a.mu.Unlock()
	}()

	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		return "", nil, fmt.Errorf("发送 FM 请求失败: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.MsgType == msgTypeFMError {
			var errPayload fmErrorPayload
			if err := json.Unmarshal(resp.Payload, &errPayload); err == nil && errPayload.Message != "" {
				return "", nil, fmt.Errorf("%s", errPayload.Message)
			}
			return "", nil, fmt.Errorf("FM 操作失败")
		}
		return resp.MsgType, resp.Payload, nil
	case <-time.After(timeout):
		return "", nil, fmt.Errorf("FM 请求超时（%s）", timeout)
	case <-a.doneCh:
		return "", nil, fmt.Errorf("Agent 连接断开")
	}
}

// ── AgentHub FM 公开方法 ─────────────────────────────────────────────────────

// FMList 返回远端节点指定路径下的目录列表。
func (h *AgentHub) FMList(providerID uint, path string) (string, []FMEntry, error) {
	conn, ok := h.GetConn(providerID)
	if !ok || conn == nil {
		return "", nil, fmt.Errorf("agent 未连接")
	}

	_, payload, err := conn.sendFMRequest(msgTypeFMList, fmListPayload{Path: path}, fmDefaultTimeout)
	if err != nil {
		global.APP_LOG.Warn("FM List 失败", zap.Uint("providerID", providerID), zap.Error(err))
		return "", nil, err
	}

	var resp fmListRespPayload
	if err := json.Unmarshal(payload, &resp); err != nil {
		return "", nil, fmt.Errorf("解析 FM List 响应失败: %w", err)
	}
	return resp.Path, resp.Entries, nil
}

// FMDownload 下载远端节点指定路径的文件，返回文件字节内容。
func (h *AgentHub) FMDownload(providerID uint, path string) ([]byte, error) {
	conn, ok := h.GetConn(providerID)
	if !ok || conn == nil {
		return nil, fmt.Errorf("agent 未连接")
	}

	_, payload, err := conn.sendFMRequest(msgTypeFMDownload, fmDownloadPayload{Path: path}, fmDefaultTimeout)
	if err != nil {
		return nil, err
	}

	var resp fmDownloadRespPayload
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("解析 FM Download 响应失败: %w", err)
	}

	data, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("解码文件内容失败: %w", err)
	}
	return data, nil
}

// FMUpload 上传文件到远端节点指定路径。
func (h *AgentHub) FMUpload(providerID uint, path string, data []byte) error {
	if len(data) > fmMaxUploadBytes {
		return fmt.Errorf("文件过大（最大 50 MB），请通过其他方式传输")
	}

	conn, ok := h.GetConn(providerID)
	if !ok || conn == nil {
		return fmt.Errorf("agent 未连接")
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	_, _, err := conn.sendFMRequest(msgTypeFMUpload, fmUploadPayload{
		Path: path,
		Data: encoded,
		Size: int64(len(data)),
	}, fmUploadTimeout)
	return err
}

// FMDelete 删除远端节点指定路径的文件或空目录。
func (h *AgentHub) FMDelete(providerID uint, path string) error {
	conn, ok := h.GetConn(providerID)
	if !ok || conn == nil {
		return fmt.Errorf("agent 未连接")
	}

	_, _, err := conn.sendFMRequest(msgTypeFMDelete, fmDeletePayload{Path: path}, fmDefaultTimeout)
	return err
}

// FMMkdir 在远端节点创建目录（含父目录）。
func (h *AgentHub) FMMkdir(providerID uint, path string) error {
	conn, ok := h.GetConn(providerID)
	if !ok || conn == nil {
		return fmt.Errorf("agent 未连接")
	}

	_, _, err := conn.sendFMRequest(msgTypeFMMkdir, fmMkdirPayload{Path: path}, fmDefaultTimeout)
	return err
}
