package traffic_monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/pmacct"

	"go.uber.org/zap"
)

// LifecycleManager 流量监控生命周期管理器
type LifecycleManager struct {
	mu sync.RWMutex
}

var (
	manager     *LifecycleManager
	managerOnce sync.Once
)

// GetManager 获取流量监控生命周期管理器单例
func GetManager() *LifecycleManager {
	managerOnce.Do(func() {
		manager = &LifecycleManager{}
	})
	return manager
}

// AttachMonitor 为单个实例附加流量监控
func (m *LifecycleManager) AttachMonitor(ctx context.Context, instanceID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查实例是否存在
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("实例不存在: %w", err)
	}

	// 检查Provider是否启用流量控制
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("Provider不存在: %w", err)
	}

	if !provider.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量控制，跳过监控附加",
			zap.Uint("instanceID", instanceID),
			zap.Uint("providerID", provider.ID))
		return nil
	}

	// 检查是否已存在监控记录
	var existingMonitor monitoringModel.PmacctMonitor
	err := global.APP_DB.Where("instance_id = ?", instanceID).First(&existingMonitor).Error
	if err == nil {
		if existingMonitor.IsEnabled {
			global.APP_LOG.Debug("监控已存在且已启用",
				zap.Uint("instanceID", instanceID),
				zap.Uint("monitorID", existingMonitor.ID))
			return nil
		}
	}

	// 使用pmacct服务初始化监控
	pmacctService := pmacct.NewServiceWithContext(ctx)
	pmacctService.SetProviderID(instance.ProviderID)

	if err := pmacctService.InitializePmacctForInstance(instanceID); err != nil {
		return fmt.Errorf("初始化流量监控失败: %w", err)
	}

	global.APP_LOG.Info("成功附加流量监控",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name))

	return nil
}

// DetachMonitor 为单个实例删除流量监控
func (m *LifecycleManager) DetachMonitor(ctx context.Context, instanceID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查实例是否存在
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		global.APP_LOG.Warn("实例不存在，继续清理监控数据",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
	}

	// 使用pmacct服务清理监控
	pmacctService := pmacct.NewServiceWithContext(ctx)
	if instance.ID > 0 {
		pmacctService.SetProviderID(instance.ProviderID)
	}

	if err := pmacctService.CleanupPmacctData(instanceID); err != nil {
		return fmt.Errorf("清理pmacct监控失败: %w", err)
	}

	global.APP_LOG.Info("成功删除流量监控",
		zap.Uint("instanceID", instanceID))

	return nil
}

// batchCheckPmacctProcesses 批量检查多个实例的pmacct服务状态
// 兼容多种初始化系统（systemd/OpenRC/SysVinit），使用临时脚本检查所有服务，执行后自动清理
func (m *LifecycleManager) batchCheckPmacctProcesses(providerInstance provider.Provider, instanceNames []string) map[string]bool {
	resultMap := make(map[string]bool)

	if len(instanceNames) == 0 {
		return resultMap
	}

	// 初始化所有实例为false
	for _, name := range instanceNames {
		resultMap[name] = false
	}

	// 生成唯一的临时脚本名
	scriptPath := fmt.Sprintf("/tmp/check_pmacct_%d.sh", time.Now().UnixNano())

	// 构建检查脚本 - 兼容多种初始化系统
	scriptContent := "#!/bin/bash\n"
	scriptContent += "# 批量检查pmacct服务状态（兼容systemd/OpenRC/SysVinit）\n\n"
	scriptContent += "# 检测初始化系统类型\n"
	scriptContent += "check_service_status() {\n"
	scriptContent += "    local service_name=$1\n"
	scriptContent += "    local instance_name=$2\n"
	scriptContent += "    \n"
	scriptContent += "    # 优先尝试systemd\n"
	scriptContent += "    if command -v systemctl >/dev/null 2>&1; then\n"
	scriptContent += "        if systemctl is-active --quiet \"${service_name}\" 2>/dev/null; then\n"
	scriptContent += "            echo \"${instance_name}:active\"\n"
	scriptContent += "            return 0\n"
	scriptContent += "        fi\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    # 尝试OpenRC\n"
	scriptContent += "    if command -v rc-service >/dev/null 2>&1; then\n"
	scriptContent += "        if rc-service \"${service_name}\" status 2>/dev/null | grep -q \"started\\|running\"; then\n"
	scriptContent += "            echo \"${instance_name}:active\"\n"
	scriptContent += "            return 0\n"
	scriptContent += "        fi\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    # 尝试传统service命令\n"
	scriptContent += "    if command -v service >/dev/null 2>&1; then\n"
	scriptContent += "        if service \"${service_name}\" status 2>/dev/null | grep -q \"running\\|active\"; then\n"
	scriptContent += "            echo \"${instance_name}:active\"\n"
	scriptContent += "            return 0\n"
	scriptContent += "        fi\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    # 最后降级为进程检查\n"
	scriptContent += "    if ps aux 2>/dev/null | grep \"pmacctd\" | grep \"${instance_name}\" | grep -v grep >/dev/null; then\n"
	scriptContent += "        echo \"${instance_name}:active\"\n"
	scriptContent += "        return 0\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    echo \"${instance_name}:inactive\"\n"
	scriptContent += "}\n\n"

	// 为每个实例调用检查函数
	for _, name := range instanceNames {
		scriptContent += fmt.Sprintf("check_service_status \"pmacctd-%s\" \"%s\"\n", name, name)
	}
	scriptContent += fmt.Sprintf("\nrm -f %s\n", scriptPath) // 脚本执行完自动删除

	// 上传脚本
	if err := m.uploadScriptViaSFTP(providerInstance, scriptContent, scriptPath); err != nil {
		global.APP_LOG.Warn("上传检查脚本失败", zap.Error(err))
		return resultMap
	}

	// 执行脚本（脚本会自动删除自己）
	execCtx, execCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, fmt.Sprintf("bash %s", scriptPath))
	if err != nil {
		global.APP_LOG.Warn("批量检查pmacct服务失败", zap.Error(err))
		// 尝试手动清理脚本（以防自动清理失败）
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", scriptPath))
		return resultMap
	}

	// 解析输出，格式为: 实例名:状态
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[0])
			status := strings.TrimSpace(parts[1])
			resultMap[name] = (status == "active")
		}
	}

	global.APP_LOG.Debug("批量检查pmacct服务完成",
		zap.Int("totalInstances", len(instanceNames)),
		zap.Int("runningCount", countTrue(resultMap)))

	return resultMap
}

// uploadScriptViaSFTP 通过SFTP上传脚本文件
func (m *LifecycleManager) uploadScriptViaSFTP(providerInstance provider.Provider, content, remotePath string) error {
	// 尝试使用echo写入（更简单，无需SFTP）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 转义特殊字符
	escapedContent := strings.ReplaceAll(content, "'", "'\\''")
	cmd := fmt.Sprintf("echo '%s' > %s && chmod +x %s", escapedContent, remotePath, remotePath)

	_, err := providerInstance.ExecuteSSHCommand(ctx, cmd)
	return err
}

// countTrue 计算map中true值的数量
func countTrue(m map[string]bool) int {
	count := 0
	for _, v := range m {
		if v {
			count++
		}
	}
	return count
}

// updateTaskStatus 更新任务状态
func (m *LifecycleManager) updateTaskStatus(taskID uint, status string, progress int, message string, startedAt, completedAt *time.Time) error {
	updates := map[string]interface{}{
		"status":   status,
		"progress": progress,
		"message":  message,
	}

	if startedAt != nil {
		updates["started_at"] = startedAt
	}
	if completedAt != nil {
		updates["completed_at"] = completedAt
	}

	return global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).
		Where("id = ?", taskID).
		Updates(updates).Error
}

// updateTaskProgress 更新任务进度
func (m *LifecycleManager) updateTaskProgress(taskID uint, progress int, message string) {
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).
		Where("id = ?", taskID).
		Updates(map[string]interface{}{
			"progress": progress,
			"message":  message,
		})
}
