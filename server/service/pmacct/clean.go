package pmacct

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CleanupPmacctData 清理实例的pmacct数据（包括宿主机服务和数据库记录）
func (s *Service) CleanupPmacctData(instanceID uint) error {
	// 使用默认上下文
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return s.CleanupPmacctDataWithContext(ctx, instanceID)
}

// CleanupPmacctDataWithContext 清理实例的pmacct数据（包括宿主机服务和数据库记录），支持自定义上下文
func (s *Service) CleanupPmacctDataWithContext(ctx context.Context, instanceID uint) error {
	// 第一步：从数据库获取实例和监控信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		global.APP_LOG.Warn("获取实例信息失败，跳过宿主机清理",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		// 即使获取实例失败，仍然清理数据库记录
	} else {
		// 第二步：清理宿主机上的 pmacct 服务和配置文件
		if cleanupErr := s.cleanupPmacctOnHostWithContext(ctx, instanceID, instance.ProviderID); cleanupErr != nil {
			global.APP_LOG.Warn("清理宿主机pmacct服务失败（不影响数据库清理）",
				zap.Uint("instanceID", instanceID),
				zap.Uint("providerID", instance.ProviderID),
				zap.Error(cleanupErr))
			// 不返回错误，继续清理数据库
		}
	}

	// 第三步：清理数据库记录
	// 不删除 pmacct_traffic_records，保留流量历史数据用于统计
	// 只删除监控配置，停止后续的流量采集
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {

		// 删除监控记录（彻底删除，不走软删除），停止后续流量采集
		if err := tx.Unscoped().Where("instance_id = ?", instanceID).Delete(&monitoringModel.PmacctMonitor{}).Error; err != nil {
			return err
		}

		// 清除实例表中保存的网络接口信息，确保下次重新检测
		// 这样可以避免容器重启后网卡名变化导致的问题
		if err := tx.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Updates(map[string]interface{}{
			"pmacct_interface_v4": "",
			"pmacct_interface_v6": "",
		}).Error; err != nil {
			global.APP_LOG.Warn("清除实例网络接口信息失败（不影响整体清理）",
				zap.Uint("instanceID", instanceID),
				zap.Error(err))
			// 不返回错误，继续清理流程
		} else {
			global.APP_LOG.Info("已清除实例网络接口信息，下次将重新检测",
				zap.Uint("instanceID", instanceID))
		}

		global.APP_LOG.Info("pmacct监控配置清理完成（流量历史数据已保留）",
			zap.Uint("instanceID", instanceID))

		return nil
	})
}

// cleanupPmacctOnHost 在宿主机上清理 pmacct 服务和配置文件
// 删除systemd服务、配置目录、SQLite数据库、pipe文件等
func (s *Service) cleanupPmacctOnHost(instanceID uint, providerID uint) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return s.cleanupPmacctOnHostWithContext(ctx, instanceID, providerID)
}

// cleanupPmacctOnHostWithContext 在宿主机上清理 pmacct 服务和配置文件（支持自定义上下文）
// 删除systemd服务、配置目录、SQLite数据库、pipe文件等
func (s *Service) cleanupPmacctOnHostWithContext(ctx context.Context, instanceID uint, providerID uint) error {
	// 检查上下文是否已取消
	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled before cleanup: %w", ctx.Err())
	default:
	}

	// 获取实例信息（用于构建路径）
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		global.APP_LOG.Warn("未找到实例记录，跳过宿主机清理",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return nil
	}

	// 获取provider配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.First(&providerRecord, providerID).Error; err != nil {
		return fmt.Errorf("failed to find provider: %w", err)
	}

	instanceName := instance.Name
	serviceName := fmt.Sprintf("pmacctd-%s", instanceName)
	configDir := fmt.Sprintf("/var/lib/pmacct/%s", instanceName)

	global.APP_LOG.Info("开始清理宿主机pmacct资源",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instanceName),
		zap.String("serviceName", serviceName),
		zap.String("configDir", configDir))

	// 创建清理脚本（每个实例独立的服务和配置）
	// 不使用 set -e，确保所有清理步骤都执行
	cleanupScript := fmt.Sprintf(`#!/bin/bash

echo "=== 开始清理 pmacct 资源: %s ==="

# 1. 停止并禁用 systemd 服务
if command -v systemctl >/dev/null 2>&1; then
    if systemctl list-units --all --type=service --full --no-pager | grep -q "%s.service"; then
        echo "发现 systemd 服务: %s"
        
        # 停止服务
        if systemctl is-active --quiet %s 2>/dev/null; then
            echo "停止 systemd 服务: %s"
            systemctl stop %s 2>/dev/null || echo "警告: 停止服务失败"
        fi
        
        # 禁用服务
        if systemctl is-enabled --quiet %s 2>/dev/null; then
            echo "禁用 systemd 服务: %s"
            systemctl disable %s 2>/dev/null || echo "警告: 禁用服务失败"
        fi
    fi
    
    # 删除服务文件（支持多个可能的位置）
    for service_file in "/etc/systemd/system/%s.service" "/lib/systemd/system/%s.service" "/usr/lib/systemd/system/%s.service"; do
        if [ -f "$service_file" ]; then
            echo "删除服务文件: $service_file"
            rm -f "$service_file" || echo "警告: 删除 $service_file 失败"
        fi
    done
    
    # 重载 systemd
    systemctl daemon-reload 2>/dev/null || echo "警告: daemon-reload 失败"
    systemctl reset-failed 2>/dev/null || true
fi

# 2. 停止 OpenRC 服务（Alpine Linux）
if command -v rc-service >/dev/null 2>&1; then
    if rc-service %s status 2>/dev/null | grep -q started; then
        echo "停止 OpenRC 服务: %s"
        rc-service %s stop 2>/dev/null || echo "警告: 停止 OpenRC 服务失败"
    fi
    if [ -f "/etc/init.d/%s" ]; then
        echo "删除 OpenRC 服务: %s"
        rc-update del %s default 2>/dev/null || echo "警告: rc-update del 失败"
        rm -f /etc/init.d/%s || echo "警告: 删除 OpenRC 服务文件失败"
    fi
fi

# 3. 停止 SysV init 服务
if [ -f "/etc/init.d/%s" ]; then
    echo "停止并删除 SysV init 服务: %s"
    /etc/init.d/%s stop 2>/dev/null || echo "警告: 停止 SysV 服务失败"
    update-rc.d -f %s remove 2>/dev/null || chkconfig %s off 2>/dev/null || echo "警告: 移除 SysV 服务失败"
    rm -f /etc/init.d/%s || echo "警告: 删除 SysV 服务文件失败"
fi

# 4. 杀死可能残留的进程（多种方式确保清理）
echo "清理残留进程"
# 方式1: 通过进程名
pkill -9 -f "pmacctd.*%s" 2>/dev/null || true
# 方式2: 通过配置文件路径
pkill -9 -f "%s/pmacctd.conf" 2>/dev/null || true
sleep 1

# 再次检查并强制清理
if pgrep -f "pmacctd.*%s" >/dev/null 2>&1; then
    echo "警告: 仍有残留进程，再次尝试清理"
    pkill -9 -f "pmacctd.*%s" 2>/dev/null || true
    sleep 1
fi

# 5. 删除配置目录及所有文件（包括 SQLite、配置文件、pipe等）
if [ -d "%s" ]; then
    echo "删除配置目录及所有文件: %s"
    # 先尝试普通删除
    rm -rf "%s" 2>/dev/null || {
        echo "警告: 普通删除失败，尝试强制删除"
        # 如果失败，尝试修改权限后删除
        chmod -R 777 "%s" 2>/dev/null || true
        rm -rf "%s" 2>/dev/null || echo "错误: 无法删除配置目录"
    }
fi

# 6. 验证清理结果
echo "=== 验证清理结果 ==="
if command -v systemctl >/dev/null 2>&1; then
    if systemctl list-units --all --type=service --full --no-pager | grep -q "%s.service"; then
        echo "警告: systemd 服务仍然存在"
    else
        echo "✓ systemd 服务已清理"
    fi
fi

if pgrep -f "pmacctd.*%s" >/dev/null 2>&1; then
    echo "警告: pmacctd 进程仍在运行"
else
    echo "✓ pmacctd 进程已清理"
fi

if [ -d "%s" ]; then
    echo "警告: 配置目录仍然存在"
else
    echo "✓ 配置目录已清理"
fi

echo "=== pmacct 资源清理完成: %s ==="
`,
		instanceName,
		serviceName, serviceName, // list-units grep
		serviceName, serviceName, serviceName, // stop
		serviceName, serviceName, serviceName, // disable
		serviceName, serviceName, serviceName, // 删除服务文件（3个位置）
		serviceName, serviceName, serviceName, // OpenRC status/stop
		serviceName, serviceName, serviceName, serviceName, // OpenRC 删除
		serviceName, serviceName, serviceName, serviceName, serviceName, serviceName, // SysV
		instanceName, configDir, // pkill
		instanceName, instanceName, // 再次检查
		configDir, configDir, configDir, configDir, configDir, // 删除配置目录
		serviceName, serviceName, configDir, // 验证
		instanceName,
	)

	// 通过SFTP上传清理脚本
	scriptPath := fmt.Sprintf("/tmp/cleanup_pmacct_%s.sh", instanceName)

	// 解析endpoint获取host和port
	host, port := utils.ParseEndpoint(providerRecord.Endpoint, providerRecord.SSHPort)

	// 从连接池获取SSH客户端
	sshConfig := utils.SSHConfig{
		Host:           host,
		Port:           port,
		Username:       providerRecord.Username,
		Password:       providerRecord.Password,
		PrivateKey:     providerRecord.SSHKey,
		ConnectTimeout: 30 * time.Second,
		ExecuteTimeout: 60 * time.Second,
	}

	sshClient, err := s.sshPool.GetOrCreate(providerID, sshConfig)
	if err != nil {
		return fmt.Errorf("从连接池获取SSH客户端失败: %w", err)
	}
	// 不要关闭客户端，由连接池管理

	// 上传清理脚本
	if err := sshClient.UploadContent(cleanupScript, scriptPath, 0755); err != nil {
		return fmt.Errorf("上传清理脚本失败: %w", err)
	}

	global.APP_LOG.Info("清理脚本已上传，准备执行",
		zap.Uint("instanceID", instanceID),
		zap.String("scriptPath", scriptPath))

	// 执行清理脚本（使用更长的超时时间）
	output, err := sshClient.Execute(fmt.Sprintf("bash %s 2>&1", scriptPath))

	// 记录详细的输出日志
	global.APP_LOG.Info("清理脚本执行完成",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instanceName),
		zap.String("serviceName", serviceName),
		zap.Bool("hasError", err != nil),
		zap.String("output", output))

	if err != nil {
		global.APP_LOG.Warn("执行清理脚本时出现错误（部分资源可能已清理）",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instanceName),
			zap.String("output", output),
			zap.Error(err))
		// 不返回错误，尽力清理，检查输出中是否有成功的部分
	}

	// 验证关键资源是否已清理
	verifyCommands := []struct {
		name    string
		command string
	}{
		{"systemd服务", fmt.Sprintf("systemctl list-units --all --type=service --full --no-pager | grep '%s.service' || echo 'NOT_FOUND'", serviceName)},
		{"进程", fmt.Sprintf("pgrep -f 'pmacctd.*%s' || echo 'NOT_FOUND'", instanceName)},
		{"配置目录", fmt.Sprintf("test -d '%s' && echo 'EXISTS' || echo 'NOT_FOUND'", configDir)},
	}

	for _, vc := range verifyCommands {
		verifyOutput, _ := sshClient.Execute(vc.command)
		verifyOutput = strings.TrimSpace(verifyOutput)

		if strings.Contains(verifyOutput, "NOT_FOUND") {
			global.APP_LOG.Info(fmt.Sprintf("✓ %s已清理", vc.name),
				zap.Uint("instanceID", instanceID),
				zap.String("instanceName", instanceName))
		} else {
			global.APP_LOG.Warn(fmt.Sprintf("✗ %s可能未完全清理", vc.name),
				zap.Uint("instanceID", instanceID),
				zap.String("instanceName", instanceName),
				zap.String("verifyOutput", verifyOutput))
		}
	}

	// 删除临时脚本
	cleanupOutput, cleanupErr := sshClient.Execute(fmt.Sprintf("rm -f %s", scriptPath))
	if cleanupErr != nil {
		global.APP_LOG.Debug("删除临时脚本失败（可忽略）",
			zap.String("scriptPath", scriptPath),
			zap.String("output", cleanupOutput),
			zap.Error(cleanupErr))
	}

	return nil
}

// CleanupOldPmacctData 清理过期的pmacct数据（分层清理策略）
// 保留策略：
// - 7天内：保留所有5分钟级数据
// - 7-30天：聚合为小时级数据（保留minute=0的记录）
// - 30-90天：聚合为日度数据（保留hour=0, minute=0的记录）
// - 90天以上：全部删除
func (s *Service) CleanupOldPmacctData(retentionDays int) error {
	now := time.Now()

	// 第一步：删除90天以上的所有数据
	cutoffTime90Days := now.AddDate(0, 0, -90)
	result90Days := global.APP_DB.Where("record_time < ?", cutoffTime90Days).
		Delete(&monitoringModel.PmacctTrafficRecord{})
	if result90Days.Error != nil {
		return result90Days.Error
	}

	global.APP_LOG.Info("清理90天以上的pmacct数据",
		zap.Int64("deletedRecords", result90Days.RowsAffected))

	// 第二步：聚合30-90天的数据为日度（保留hour=0, minute=0）
	cutoffTime30Days := now.AddDate(0, 0, -30)
	if err := s.aggregateToDailyBetween(cutoffTime90Days, cutoffTime30Days); err != nil {
		global.APP_LOG.Warn("聚合为日度数据失败", zap.Error(err))
	}

	// 删除30-90天的非日度数据
	result30_90 := global.APP_DB.Where("record_time < ? AND record_time >= ? AND (hour > 0 OR minute > 0)",
		cutoffTime30Days, cutoffTime90Days).
		Delete(&monitoringModel.PmacctTrafficRecord{})
	if result30_90.Error != nil {
		return result30_90.Error
	}

	global.APP_LOG.Info("清理30-90天的非日度pmacct数据",
		zap.Int64("deletedRecords", result30_90.RowsAffected))

	// 第三步：聚合7-30天的数据为小时级（保留minute=0）
	cutoffTime7Days := now.AddDate(0, 0, -7)
	if err := s.aggregateToHourlyBetween(cutoffTime30Days, cutoffTime7Days); err != nil {
		global.APP_LOG.Warn("聚合为小时数据失败", zap.Error(err))
	}

	// 删除7-30天的5分钟级数据
	result7_30 := global.APP_DB.Where("record_time < ? AND record_time >= ? AND minute > 0",
		cutoffTime7Days, cutoffTime30Days).
		Delete(&monitoringModel.PmacctTrafficRecord{})
	if result7_30.Error != nil {
		return result7_30.Error
	}

	global.APP_LOG.Info("清理7-30天的5分钟级pmacct数据",
		zap.Int64("deletedRecords", result7_30.RowsAffected))

	totalDeleted := result90Days.RowsAffected + result30_90.RowsAffected + result7_30.RowsAffected
	global.APP_LOG.Info("pmacct数据清理完成",
		zap.Int("retentionDays", retentionDays),
		zap.Int64("totalDeletedRecords", totalDeleted))

	return nil
}

// ResetPmacctDaemon 完全重置pmacct守护进程和数据库
// 正确的清理方式：
// 1. 停止pmacct守护进程
// 2. 删除SQLite数据库文件
// 3. 删除pipe文件
// 4. 重新初始化数据库
// 5. 重启pmacct守护进程
// 重置期间的数据丢失是可接受的，因为这是定期维护操作
func (s *Service) ResetPmacctDaemon(instanceID uint) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("failed to find instance: %w", err)
	}

	var monitor monitoringModel.PmacctMonitor
	if err := global.APP_DB.Where("instance_id = ?", instanceID).First(&monitor).Error; err != nil {
		return fmt.Errorf("pmacct monitor not found: %w", err)
	}

	providerInstance, exists := providerService.GetProviderService().GetProviderByID(instance.ProviderID)
	if !exists {
		return fmt.Errorf("provider ID %d not found", instance.ProviderID)
	}

	s.SetProviderID(instance.ProviderID)

	configDir := fmt.Sprintf("/var/lib/pmacct/%s", instance.Name)
	configFile := fmt.Sprintf("%s/pmacctd.conf", configDir)
	dbPath := fmt.Sprintf("%s/traffic.db", configDir)
	// pipeFile已不再使用（Memory插件已禁用）
	// pipeFile := fmt.Sprintf("%s/pmacctd.pipe", configDir)

	global.APP_LOG.Info("开始重置pmacct守护进程",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name))

	// 步骤1: 停止pmacct守护进程
	stopCmd := fmt.Sprintf(`
# 停止pmacct守护进程
pkill -9 -f "%s/pmacctd.conf" 2>/dev/null || true
sleep 2
`, configDir)

	ctx1, cancel1 := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel1()
	if _, err := providerInstance.ExecuteSSHCommand(ctx1, stopCmd); err != nil {
		global.APP_LOG.Warn("停止pmacct守护进程失败（可能未运行）",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
	}

	// 步骤2: 删除旧数据
	cleanupCmd := fmt.Sprintf(`
# 删除SQLite数据库
rm -f "%s" 2>/dev/null || true

# 删除pipe文件（Memory插件已禁用，旧文件会自动过期）
# pipe文件路径为 %s/pmacctd.pipe，已不再使用

# 确保目录存在
mkdir -p "%s"
chmod 755 "%s"
`, dbPath, configDir, configDir, configDir)

	ctx2, cancel2 := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel2()
	if _, err := providerInstance.ExecuteSSHCommand(ctx2, cleanupCmd); err != nil {
		return fmt.Errorf("failed to cleanup old data: %w", err)
	}

	// 步骤3: 重新初始化数据库
	if err := s.initializePmacctDatabase(providerInstance, dbPath); err != nil {
		return fmt.Errorf("failed to reinitialize database: %w", err)
	}

	// 步骤4: 重启pmacct守护进程
	startCmd := fmt.Sprintf(`
# 检测init系统并重启服务
if command -v systemctl >/dev/null 2>&1; then
    # systemd
    systemctl restart pmacct-%s.service 2>/dev/null || \
    nohup /usr/sbin/pmacctd -f %s >/dev/null 2>&1 &
elif command -v rc-service >/dev/null 2>&1; then
    # OpenRC (Alpine)
    rc-service pmacct-%s restart 2>/dev/null || \
    nohup /usr/sbin/pmacctd -f %s >/dev/null 2>&1 &
else
    # 降级方案：直接使用nohup启动
    nohup /usr/sbin/pmacctd -f %s >/dev/null 2>&1 &
fi

# 等待服务启动
sleep 2

# 验证进程是否在运行
if pgrep -f "%s/pmacctd.conf" >/dev/null 2>&1; then
    echo "pmacct daemon started successfully"
    exit 0
else
    echo "pmacct daemon failed to start"
    exit 1
fi
`, instance.Name, configFile, instance.Name, configFile, configFile, configDir)

	ctx3, cancel3 := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel3()
	output, err := providerInstance.ExecuteSSHCommand(ctx3, startCmd)
	if err != nil {
		return fmt.Errorf("failed to restart pmacct daemon: %w, output: %s", err, output)
	}

	global.APP_LOG.Info("pmacct守护进程重置完成",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("output", output))

	return nil
}
