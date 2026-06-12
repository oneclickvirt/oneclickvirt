package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerSvc "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type ProviderImageCleanupItem struct {
	Kind    string `json:"kind"`
	Runtime string `json:"runtime"`
	ID      string `json:"id"`
	Path    string `json:"path"`
	Name    string `json:"name"`
}

type ProviderImageCleanupPayload struct {
	Items []ProviderImageCleanupItem `json:"items"`
}

func CreateProviderImageCleanupTask(providerID uint, adminID uint, items []ProviderImageCleanupItem) (*adminModel.Task, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("请选择需要清理的镜像或缓存")
	}
	payload, err := json.Marshal(ProviderImageCleanupPayload{Items: items})
	if err != nil {
		return nil, err
	}
	providerIDPtr := providerID
	taskSvc := GetTaskService()
	task, err := taskSvc.CreateTask(adminID, &providerIDPtr, nil, "provider-image-cleanup", string(payload), 1800)
	if err != nil {
		return nil, err
	}
	if err := taskSvc.StartTask(task.ID); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *TaskService) executeProviderImageCleanupTask(ctx context.Context, task *adminModel.Task) error {
	if task.ProviderID == nil {
		return fmt.Errorf("任务缺少ProviderID")
	}
	var payload ProviderImageCleanupPayload
	if err := json.Unmarshal([]byte(task.TaskData), &payload); err != nil {
		return fmt.Errorf("解析清理任务数据失败: %w", err)
	}
	if len(payload.Items) == 0 {
		return fmt.Errorf("没有需要清理的镜像或缓存")
	}

	providerInstance, err := providerSvc.GetProviderInstanceByID(*task.ProviderID)
	if err != nil {
		return fmt.Errorf("Provider未连接: %w", err)
	}

	total := len(payload.Items)
	successCount := 0
	failures := make([]string, 0)
	utils.UpdateTaskProgress(task.ID, 5, "开始清理节点镜像与缓存")

	for i, item := range payload.Items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		progress := 5 + int(float64(i)/float64(total)*90)
		utils.UpdateTaskProgress(task.ID, progress, fmt.Sprintf("清理 %s", displayCleanupItem(item)))

		cmd, err := buildProviderImageCleanupCommand(item)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", displayCleanupItem(item), err))
			continue
		}

		itemCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		output, err := providerInstance.ExecuteSSHCommand(itemCtx, cmd)
		cancel()
		utils.AppendTaskCommandResult(task.ID, progress, "清理命令输出", cmd, output, err)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", displayCleanupItem(item), err))
			continue
		}
		successCount++
	}

	if len(failures) > 0 {
		utils.UpdateTaskProgress(task.ID, 100, fmt.Sprintf("清理完成：成功 %d，失败 %d", successCount, len(failures)))
		return errors.New(strings.Join(failures, "; "))
	}
	utils.UpdateTaskProgress(task.ID, 100, fmt.Sprintf("清理完成：成功 %d/%d", successCount, total))
	return nil
}

func displayCleanupItem(item ProviderImageCleanupItem) string {
	if item.Name != "" {
		return item.Name
	}
	if item.Path != "" {
		return item.Path
	}
	return item.ID
}

func buildProviderImageCleanupCommand(item ProviderImageCleanupItem) (string, error) {
	kind := strings.TrimSpace(item.Kind)
	runtime := strings.TrimSpace(item.Runtime)
	if kind == "file" {
		path := filepath.Clean(strings.TrimSpace(item.Path))
		if !isSafeImageCachePath(path) {
			return "", fmt.Errorf("拒绝清理非白名单路径: %s", path)
		}
		return "rm -f -- " + shellQuote(path), nil
	}
	if kind == "runtime-image" {
		id := strings.TrimSpace(item.ID)
		if !isSafeImageID(id) {
			return "", fmt.Errorf("镜像ID非法")
		}
		switch runtime {
		case "docker":
			return "docker image rm -f -- " + shellQuote(id), nil
		case "podman":
			return "podman image rm -f -- " + shellQuote(id), nil
		case "containerd":
			return "ctr -n k8s.io images rm -- " + shellQuote(id) + " || ctr images rm -- " + shellQuote(id), nil
		case "lxc":
			return "lxc image delete -- " + shellQuote(id), nil
		case "incus":
			return "incus image delete -- " + shellQuote(id), nil
		default:
			return "", fmt.Errorf("不支持的runtime: %s", runtime)
		}
	}
	return "", fmt.Errorf("不支持的清理类型: %s", kind)
}

func isSafeImageCachePath(path string) bool {
	allowedPrefixes := []string{
		"/var/lib/vz/template/cache/",
		"/var/lib/vz/template/iso/",
		"/var/lib/oneclickvirt/images/",
		"/var/lib/oneclickvirt/cache/",
		"/var/cache/oneclickvirt/",
		"/var/lib/lxc/images/",
		"/var/lib/incus/images/",
		"/var/lib/docker/overlay2/",
		"/var/lib/containerd/io.containerd.content.v1.content/blobs/",
	}
	if path == "/" || path == "." || strings.Contains(path, "..") {
		return false
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isSafeImageID(id string) bool {
	if id == "" || len(id) > 256 || strings.ContainsAny(id, " ;|&`$<>\\\n\r") {
		return false
	}
	return true
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func parseUintOrZero(s string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return v
}

func logProviderCleanupWarning(msg string, fields ...zap.Field) {
	if global.APP_LOG != nil {
		global.APP_LOG.Warn(msg, fields...)
	}
}
