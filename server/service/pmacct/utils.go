package pmacct

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// PmacctTrafficData pmacct流量数据结构
type PmacctTrafficData struct {
	RxBytes    int64     `json:"rx_bytes"`
	TxBytes    int64     `json:"tx_bytes"`
	TotalBytes int64     `json:"total_bytes"`
	RecordTime time.Time `json:"record_time"`
}

// IsPmacctRunningOnHost 检查Provider宿主机上是否实际运行着指定实例的pmacct监控进程
// 这是检查监控是否实际存在的最可靠方式
func (s *Service) IsPmacctRunningOnHost(instanceID uint) (bool, error) {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return false, fmt.Errorf("failed to find instance: %w", err)
	}

	// 获取provider实例
	providerInstance, exists := providerService.GetProviderService().GetProviderByID(instance.ProviderID)
	if !exists {
		return false, fmt.Errorf("provider ID %d not found", instance.ProviderID)
	}

	// 检查pmacct进程是否在运行
	checkCmd := fmt.Sprintf("pgrep -f 'pmacctd.*%s' >/dev/null 2>&1 && echo 'RUNNING' || echo 'NOT_RUNNING'", instance.Name)

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, checkCmd)
	if err != nil {
		return false, fmt.Errorf("failed to check pmacct process: %w", err)
	}

	isRunning := strings.Contains(strings.TrimSpace(output), "RUNNING")

	global.APP_LOG.Debug("检查pmacct进程状态",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.Bool("isRunning", isRunning))

	return isRunning, nil
}

// uploadFileViaSFTP 通过SFTP上传文件内容到远程服务器（使用连接池）
func (s *Service) uploadFileViaSFTP(providerInstance provider.Provider, content, remotePath string, perm uint32) error {
	// 获取provider的SSH配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.First(&providerRecord, s.providerID).Error; err != nil {
		return fmt.Errorf("failed to find provider: %w", err)
	}

	// 解析endpoint获取host和port
	host, port := utils.ParseEndpoint(providerRecord.Endpoint, providerRecord.SSHPort)

	// 从连接池获取或创建SSH客户端
	sshConfig := utils.SSHConfig{
		Host:           host,
		Port:           port,
		Username:       providerRecord.Username,
		Password:       providerRecord.Password,
		PrivateKey:     providerRecord.SSHKey,
		ConnectTimeout: 30 * time.Second,
		ExecuteTimeout: 60 * time.Second,
	}

	sshClient, err := s.sshPool.GetOrCreate(s.providerID, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to get SSH client from pool: %w", err)
	}
	// 不要关闭客户端，由连接池管理

	// 使用SSH客户端的UploadContent方法上传文件
	if err := sshClient.UploadContent(content, remotePath, os.FileMode(perm)); err != nil {
		return fmt.Errorf("failed to upload file via SFTP: %w", err)
	}

	global.APP_LOG.Debug("文件上传成功（使用连接池）",
		zap.String("remotePath", remotePath),
		zap.Int("contentLength", len(content)),
		zap.Uint("providerID", s.providerID))

	return nil
}

// initializePmacctDatabase 初始化pmacct SQLite数据库表结构
// pmacct不会自动创建表，需要手动创建acct_v9表结构
func (s *Service) initializePmacctDatabase(providerInstance provider.Provider, dbPath string) error {
	global.APP_LOG.Info("初始化pmacct数据库表结构", zap.String("dbPath", dbPath))

	// acct_v9 表结构（兼容方案 - 同时支持 aggregate 字段名和标准 v9 列名）
	// pmacct 可能使用 ip_src/ip_dst 或 src_host/dst_host，都支持
	// aggregate: src_host, dst_host（端口和协议字段已禁用）
	createTableSQL := `
-- 删除旧表（如果存在），确保表结构正确
DROP TABLE IF EXISTS acct_v9;

-- 创建新表结构（所有列允许NULL，因为pmacct可能只填充其中一套）
-- 端口和协议字段保留但不在aggregate中使用
CREATE TABLE acct_v9 (
    -- aggregate 字段名（使用中）
    src_host TEXT,
    dst_host TEXT,
    -- 端口和协议字段（保留但未启用，不占用内存）
    src_port INTEGER DEFAULT 0,
    dst_port INTEGER DEFAULT 0,
    proto TEXT,
    -- 标准 v9 字段名（兼容性）
    ip_src TEXT,
    ip_dst TEXT,
    port_src INTEGER DEFAULT 0,
    port_dst INTEGER DEFAULT 0,
    ip_proto TEXT,
    -- 统计字段（必需）
    packets INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    -- 时间戳（必需）
    stamp_inserted TEXT NOT NULL,
    stamp_updated TEXT
);

-- 创建触发器：自动同步数据（双向）
-- 端口和协议字段的同步保留，但由于aggregate中未启用，实际不会使用
CREATE TRIGGER IF NOT EXISTS sync_on_insert
AFTER INSERT ON acct_v9
WHEN NEW.src_host IS NULL OR NEW.ip_src IS NULL
BEGIN
    UPDATE acct_v9 SET 
        src_host = COALESCE(NEW.src_host, NEW.ip_src),
        dst_host = COALESCE(NEW.dst_host, NEW.ip_dst),
        src_port = COALESCE(NEW.src_port, NEW.port_src),
        dst_port = COALESCE(NEW.dst_port, NEW.port_dst),
        proto = COALESCE(NEW.proto, NEW.ip_proto),
        ip_src = COALESCE(NEW.ip_src, NEW.src_host),
        ip_dst = COALESCE(NEW.ip_dst, NEW.dst_host),
        port_src = COALESCE(NEW.port_src, NEW.src_port),
        port_dst = COALESCE(NEW.port_dst, NEW.dst_port),
        ip_proto = COALESCE(NEW.ip_proto, NEW.proto)
    WHERE rowid = NEW.rowid;
END;

-- 仅为实际使用的字段创建索引
CREATE INDEX idx_stamp_inserted ON acct_v9(stamp_inserted);
CREATE INDEX idx_src_host ON acct_v9(src_host);
CREATE INDEX idx_dst_host ON acct_v9(dst_host);
CREATE INDEX idx_ip_src ON acct_v9(ip_src);
CREATE INDEX idx_ip_dst ON acct_v9(ip_dst);
CREATE INDEX idx_proto ON acct_v9(proto);
`

	// 生成初始化脚本
	initScript := fmt.Sprintf(`#!/bin/bash
set -e

# 确保数据库文件所在目录存在
mkdir -p "$(dirname %s)"

# 使用sqlite3初始化数据库表结构
if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "sqlite3 not found, attempting to install..."
    
    # 检测操作系统并安装sqlite3
    if [ -f /etc/debian_version ]; then
        apt-get update -qq && apt-get install -y sqlite3
    elif [ -f /etc/redhat-release ] || [ -f /etc/centos-release ] || [ -f /etc/almalinux-release ] || [ -f /etc/rocky-release ] || [ -f /etc/oracle-release ]; then
        if command -v dnf >/dev/null 2>&1; then
            dnf install -y sqlite
        else
            yum install -y sqlite
        fi
    elif [ -f /etc/alpine-release ]; then
        apk update && apk add --no-cache sqlite
    elif [ -f /etc/arch-release ] || command -v pacman >/dev/null 2>&1; then
        pacman -Sy --noconfirm --needed sqlite
    else
        echo "Error: Unsupported OS for automatic sqlite3 installation."
        exit 1
    fi
    
    # 再次检查
    if ! command -v sqlite3 >/dev/null 2>&1; then
        echo "Error: sqlite3 installation failed."
        exit 1
    fi
fi

# 执行建表SQL
sqlite3 %s <<'EOF'
%s
EOF

# 验证表是否创建成功
if sqlite3 %s "SELECT name FROM sqlite_master WHERE type='table' AND name='acct_v9';" | grep -q "acct_v9"; then
    echo "Database initialized successfully"
    chmod 644 %s
    exit 0
else
    echo "Failed to create acct_v9 table"
    exit 1
fi
`, dbPath, dbPath, createTableSQL, dbPath, dbPath)

	// 上传并执行初始化脚本
	scriptPath := fmt.Sprintf("/tmp/pmacct_init_db_%d.sh", time.Now().Unix())
	if err := s.uploadFileViaSFTP(providerInstance, initScript, scriptPath, 0755); err != nil {
		return fmt.Errorf("failed to upload database init script: %w", err)
	}

	// 执行初始化脚本
	execCtx, execCancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, scriptPath)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w, output: %s", err, output)
	}

	// 清理临时脚本
	cleanupCtx, cleanupCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cleanupCancel()
	providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", scriptPath))

	global.APP_LOG.Info("pmacct数据库表结构初始化成功",
		zap.String("dbPath", dbPath),
		zap.String("output", output))

	return nil
}

// refreshProviderCache 刷新provider缓存
func (s *Service) refreshProviderCache(providerID uint, providerRecord *providerModel.Provider) error {
	global.APP_LOG.Info("刷新provider缓存", zap.Uint("providerID", providerID))

	// 使用ProviderService的ReloadProvider方法重新加载provider
	providerSvc := providerService.GetProviderService()
	if err := providerSvc.ReloadProvider(providerID); err != nil {
		return fmt.Errorf("failed to reload provider: %w", err)
	}

	global.APP_LOG.Info("provider缓存刷新成功", zap.Uint("providerID", providerID))
	return nil
}

// aggregateToDailyBetween 将小时级数据聚合为日度统计
func (s *Service) aggregateToDailyBetween(startTime, endTime time.Time) error {
	global.APP_LOG.Info("开始聚合小时数据到日度统计",
		zap.Time("startTime", startTime),
		zap.Time("endTime", endTime))

	aggregateSQL := `
		INSERT INTO pmacct_traffic_records (
			instance_id, user_id, provider_id, provider_type, mapped_ip,
			rx_bytes, tx_bytes, total_bytes,
			timestamp, year, month, day, hour, minute,
			record_time, created_at, updated_at
		)
		SELECT 
			instance_id,
			user_id,
			provider_id,
			provider_type,
			mapped_ip,
			MAX(rx_bytes) as rx_bytes,
			MAX(tx_bytes) as tx_bytes,
			MAX(total_bytes) as total_bytes,
			DATE_FORMAT(timestamp, '%Y-%m-%d 00:00:00') as timestamp,
			year,
			month,
			day,
			0 as hour,
			0 as minute,
			MAX(record_time) as record_time,
			NOW() as created_at,
			NOW() as updated_at
		FROM pmacct_traffic_records
		WHERE record_time >= ? AND record_time < ?
			AND (hour > 0 OR minute > 0)
		GROUP BY instance_id, user_id, provider_id, provider_type, mapped_ip, year, month, day
		ON DUPLICATE KEY UPDATE
			rx_bytes = VALUES(rx_bytes),
			tx_bytes = VALUES(tx_bytes),
			total_bytes = VALUES(total_bytes),
			updated_at = VALUES(updated_at)
	`

	result := global.APP_DB.Exec(aggregateSQL, startTime, endTime)
	if result.Error != nil {
		return fmt.Errorf("failed to aggregate to daily: %w", result.Error)
	}

	global.APP_LOG.Info("日度统计聚合完成",
		zap.Int64("aggregatedDays", result.RowsAffected))

	return nil
}

// aggregateToHourlyBetween 将5分钟数据聚合为小时级统计
func (s *Service) aggregateToHourlyBetween(startTime, endTime time.Time) error {
	global.APP_LOG.Info("开始聚合5分钟数据到小时统计",
		zap.Time("startTime", startTime),
		zap.Time("endTime", endTime))

	aggregateSQL := `
		INSERT INTO pmacct_traffic_records (
			instance_id, user_id, provider_id, provider_type, mapped_ip,
			rx_bytes, tx_bytes, total_bytes,
			timestamp, year, month, day, hour, minute,
			record_time, created_at, updated_at
		)
		SELECT 
			instance_id,
			user_id,
			provider_id,
			provider_type,
			mapped_ip,
			MAX(rx_bytes) as rx_bytes,
			MAX(tx_bytes) as tx_bytes,
			MAX(total_bytes) as total_bytes,
			DATE_FORMAT(timestamp, '%Y-%m-%d %H:00:00') as timestamp,
			year,
			month,
			day,
			hour,
			0 as minute,
			MAX(record_time) as record_time,
			NOW() as created_at,
			NOW() as updated_at
		FROM pmacct_traffic_records
		WHERE record_time >= ? AND record_time < ?
			AND minute > 0
		GROUP BY instance_id, user_id, provider_id, provider_type, mapped_ip, year, month, day, hour
		ON DUPLICATE KEY UPDATE
			rx_bytes = VALUES(rx_bytes),
			tx_bytes = VALUES(tx_bytes),
			total_bytes = VALUES(total_bytes),
			updated_at = VALUES(updated_at)
	`

	result := global.APP_DB.Exec(aggregateSQL, startTime, endTime)
	if result.Error != nil {
		return fmt.Errorf("failed to aggregate to hourly: %w", result.Error)
	}

	global.APP_LOG.Info("小时统计聚合完成",
		zap.Int64("aggregatedHours", result.RowsAffected))

	return nil
}

// aggregateTrafficRecords 聚合指定条件的流量记录
func (s *Service) aggregateTrafficRecords(instanceID uint, year, month, day, hour int) *monitoringModel.PmacctTrafficRecord {
	query := global.APP_DB.Model(&monitoringModel.PmacctTrafficRecord{}).
		Where("instance_id = ?", instanceID)

	if year > 0 {
		query = query.Where("year = ?", year)
	}
	if month > 0 {
		query = query.Where("month = ?", month)
	}
	if day > 0 {
		query = query.Where("day = ?", day)
	}
	if hour > 0 {
		query = query.Where("hour = ?", hour)
	}

	// 处理pmacct重启导致的累积值重置问题
	var result struct {
		RxBytes int64
		TxBytes int64
	}

	// 构建查询条件
	whereClause := "instance_id = ?"
	args := []interface{}{instanceID}

	if year > 0 {
		whereClause += " AND year = ?"
		args = append(args, year)
	}
	if month > 0 {
		whereClause += " AND month = ?"
		args = append(args, month)
	}
	if day > 0 {
		whereClause += " AND day = ?"
		args = append(args, day)
	}
	if hour > 0 {
		whereClause += " AND hour = ?"
		args = append(args, hour)
	}

	sql := fmt.Sprintf(`
		SELECT 
			COALESCE(SUM(segment_max_rx), 0) as rx_bytes,
			COALESCE(SUM(segment_max_tx), 0) as tx_bytes
		FROM (
			SELECT 
				segment_id,
				MAX(rx_bytes) as segment_max_rx,
				MAX(tx_bytes) as segment_max_tx
			FROM (
				SELECT 
					t1.timestamp,
					t1.rx_bytes,
					t1.tx_bytes,
					(SELECT COUNT(*)
					 FROM pmacct_traffic_records t2
					 WHERE %s
					   AND t2.timestamp <= t1.timestamp
					   AND (
						 (t2.rx_bytes < (SELECT COALESCE(MAX(t3.rx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE %s
										   AND t3.timestamp < t2.timestamp))
						 OR
						 (t2.tx_bytes < (SELECT COALESCE(MAX(t3.tx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE %s
										   AND t3.timestamp < t2.timestamp))
					   )
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE %s
			) segments
			GROUP BY segment_id
		) segment_max
	`, whereClause, whereClause, whereClause, whereClause)

	global.APP_DB.Raw(sql, append(append(append(args, args...), args...), args...)...).Scan(&result)

	return &monitoringModel.PmacctTrafficRecord{
		InstanceID: instanceID,
		RxBytes:    result.RxBytes,
		TxBytes:    result.TxBytes,
		TotalBytes: result.RxBytes + result.TxBytes,
		Year:       year,
		Month:      month,
		Day:        day,
		Hour:       hour,
		RecordTime: time.Now(),
	}
}

// getAggregatedHistory 获取聚合的历史记录
func (s *Service) getAggregatedHistory(instanceID uint, days int) []*monitoringModel.PmacctTrafficRecord {
	var records []*monitoringModel.PmacctTrafficRecord

	// 获取最近N天的日度统计
	query := `
		SELECT 
			instance_id,
			provider_id,
			provider_type,
			mapped_ip,
			year,
			month,
			day,
			SUM(segment_max_rx) as rx_bytes,
			SUM(segment_max_tx) as tx_bytes,
			SUM(segment_max_total) as total_bytes,
			MAX(record_time) as record_time
		FROM (
			SELECT 
				instance_id,
				provider_id,
				provider_type,
				mapped_ip,
				year,
				month,
				day,
				segment_id,
				MAX(rx_bytes) as segment_max_rx,
				MAX(tx_bytes) as segment_max_tx,
				MAX(total_bytes) as segment_max_total,
				MAX(record_time) as record_time
			FROM (
				SELECT 
					t1.instance_id,
					t1.provider_id,
					t1.provider_type,
					t1.mapped_ip,
					t1.year,
					t1.month,
					t1.day,
					t1.timestamp,
					t1.rx_bytes,
					t1.tx_bytes,
					t1.total_bytes,
					t1.record_time,
					(SELECT COUNT(*)
					 FROM pmacct_traffic_records t2
					 WHERE t2.instance_id = ? 
					   AND t2.day > 0
					   AND t2.year = t1.year
					   AND t2.month = t1.month
					   AND t2.day = t1.day
					   AND t2.timestamp <= t1.timestamp
					   AND (
						 (t2.rx_bytes < (SELECT COALESCE(MAX(t3.rx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE t3.instance_id = ?
										   AND t3.day > 0
										   AND t3.year = t1.year
										   AND t3.month = t1.month
										   AND t3.day = t1.day
										   AND t3.timestamp < t2.timestamp))
						 OR
						 (t2.tx_bytes < (SELECT COALESCE(MAX(t3.tx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE t3.instance_id = ?
										   AND t3.day > 0
										   AND t3.year = t1.year
										   AND t3.month = t1.month
										   AND t3.day = t1.day
										   AND t3.timestamp < t2.timestamp))
					   )
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.instance_id = ? AND t1.day > 0
			) daily_segments
			GROUP BY instance_id, provider_id, provider_type, mapped_ip, year, month, day, segment_id
		) daily_segment_max
		GROUP BY instance_id, provider_id, provider_type, mapped_ip, year, month, day
		ORDER BY year DESC, month DESC, day DESC
		LIMIT ?
	`

	global.APP_DB.Raw(query, instanceID, instanceID, instanceID, instanceID, days).Scan(&records)
	return records
}
