package pmacct

import (
	"context"
	"fmt"
	"os"
	"sort"
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
	global.APP_LOG.Debug("初始化pmacct数据库表结构", zap.String("dbPath", dbPath))

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
	global.APP_LOG.Debug("刷新provider缓存", zap.Uint("providerID", providerID))

	// 使用ProviderService的ReloadProvider方法重新加载provider
	providerSvc := providerService.GetProviderService()
	if err := providerSvc.ReloadProvider(providerID); err != nil {
		return fmt.Errorf("failed to reload provider: %w", err)
	}

	global.APP_LOG.Debug("provider缓存刷新成功", zap.Uint("providerID", providerID))
	return nil
}

// aggregateTrafficRecords 聚合指定条件的流量记录
func (s *Service) aggregateTrafficRecords(instanceID uint, year, month, day, hour int) *monitoringModel.PmacctTrafficRecord {
	start, end, bounded := pmacctAggregateWindow(year, month, day, hour)
	records, err := s.loadPmacctInstanceRecords(instanceID, start, end, bounded)
	if err != nil {
		global.APP_LOG.Warn("聚合pmacct流量记录失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return zeroPmacctAggregate(instanceID, year, month, day, hour)
	}

	aggregate := zeroPmacctAggregate(instanceID, year, month, day, hour)
	for _, delta := range computePmacctRecordDeltas(records, start, bounded) {
		aggregate.ProviderID = delta.record.ProviderID
		aggregate.ProviderType = delta.record.ProviderType
		aggregate.MappedIP = delta.record.MappedIP
		aggregate.RxBytes += delta.rxBytes
		aggregate.TxBytes += delta.txBytes
		aggregate.TotalBytes += delta.rxBytes + delta.txBytes
		aggregate.Timestamp = pmacctRecordTimestamp(delta.record)
		aggregate.RecordTime = delta.record.RecordTime
	}
	return aggregate
}

// getAggregatedHistory 获取聚合的历史记录
func (s *Service) getAggregatedHistory(instanceID uint, days int) []*monitoringModel.PmacctTrafficRecord {
	if days <= 0 {
		days = 30
	}

	now := time.Now()
	start := pmacctDayStart(now.AddDate(0, 0, -days+1))
	records, err := s.loadPmacctInstanceRecords(instanceID, start, now, true)
	if err != nil {
		global.APP_LOG.Warn("获取pmacct历史记录失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return []*monitoringModel.PmacctTrafficRecord{}
	}

	dayMap := make(map[time.Time]*monitoringModel.PmacctTrafficRecord)
	for _, delta := range computePmacctRecordDeltas(records, start, true) {
		ts := pmacctRecordTimestamp(delta.record)
		dayStart := pmacctDayStart(ts)
		record := dayMap[dayStart]
		if record == nil {
			record = &monitoringModel.PmacctTrafficRecord{
				InstanceID:   instanceID,
				ProviderID:   delta.record.ProviderID,
				ProviderType: delta.record.ProviderType,
				MappedIP:     delta.record.MappedIP,
				Year:         dayStart.Year(),
				Month:        int(dayStart.Month()),
				Day:          dayStart.Day(),
				Timestamp:    dayStart,
				RecordTime:   ts,
			}
			dayMap[dayStart] = record
		}
		record.ProviderID = delta.record.ProviderID
		record.ProviderType = delta.record.ProviderType
		record.MappedIP = delta.record.MappedIP
		record.RxBytes += delta.rxBytes
		record.TxBytes += delta.txBytes
		record.TotalBytes += delta.rxBytes + delta.txBytes
		if delta.record.RecordTime.After(record.RecordTime) {
			record.RecordTime = delta.record.RecordTime
		}
	}

	recordsByDay := make([]*monitoringModel.PmacctTrafficRecord, 0, len(dayMap))
	for _, record := range dayMap {
		recordsByDay = append(recordsByDay, record)
	}
	sort.Slice(recordsByDay, func(i, j int) bool {
		return recordsByDay[i].Timestamp.After(recordsByDay[j].Timestamp)
	})
	if len(recordsByDay) > days {
		recordsByDay = recordsByDay[:days]
	}
	return recordsByDay
}

type pmacctRecordDelta struct {
	record  monitoringModel.PmacctTrafficRecord
	rxBytes int64
	txBytes int64
}

func (s *Service) loadPmacctInstanceRecords(instanceID uint, start, end time.Time, bounded bool) ([]monitoringModel.PmacctTrafficRecord, error) {
	query := global.APP_DB.
		Model(&monitoringModel.PmacctTrafficRecord{}).
		Where("instance_id = ?", instanceID).
		Order("timestamp ASC")
	if bounded {
		query = query.Where("timestamp >= ? AND timestamp < ?", start, end)
	}

	var records []monitoringModel.PmacctTrafficRecord
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}
	if !bounded {
		return records, nil
	}

	var baseline monitoringModel.PmacctTrafficRecord
	baselineTx := global.APP_DB.
		Where("instance_id = ? AND timestamp < ?", instanceID, start).
		Order("timestamp DESC").
		Limit(1).
		Find(&baseline)
	if baselineTx.Error != nil {
		return nil, baselineTx.Error
	}
	if baselineTx.RowsAffected > 0 {
		records = append([]monitoringModel.PmacctTrafficRecord{baseline}, records...)
	}
	return records, nil
}

func computePmacctRecordDeltas(records []monitoringModel.PmacctTrafficRecord, start time.Time, bounded bool) []pmacctRecordDelta {
	sort.Slice(records, func(i, j int) bool {
		return pmacctRecordTimestamp(records[i]).Before(pmacctRecordTimestamp(records[j]))
	})

	deltas := make([]pmacctRecordDelta, 0, len(records))
	var previous monitoringModel.PmacctTrafficRecord
	hasPrevious := false
	for _, record := range records {
		ts := pmacctRecordTimestamp(record)
		if bounded && ts.Before(start) {
			previous = record
			hasPrevious = true
			continue
		}

		rxDelta := record.RxBytes
		txDelta := record.TxBytes
		if hasPrevious {
			rxDelta = pmacctCounterDelta(previous.RxBytes, record.RxBytes)
			txDelta = pmacctCounterDelta(previous.TxBytes, record.TxBytes)
		}

		deltas = append(deltas, pmacctRecordDelta{
			record:  record,
			rxBytes: rxDelta,
			txBytes: txDelta,
		})
		previous = record
		hasPrevious = true
	}
	return deltas
}

func pmacctCounterDelta(previous, current int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}

func pmacctAggregateWindow(year, month, day, hour int) (time.Time, time.Time, bool) {
	if year <= 0 {
		return time.Time{}, time.Time{}, false
	}

	if month <= 0 {
		start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.Local)
		return start, start.AddDate(1, 0, 0), true
	}

	if day <= 0 {
		start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
		return start, start.AddDate(0, 1, 0), true
	}

	if hour > 0 {
		start := time.Date(year, time.Month(month), day, hour, 0, 0, 0, time.Local)
		return start, start.Add(time.Hour), true
	}

	start := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
	return start, start.AddDate(0, 0, 1), true
}

func zeroPmacctAggregate(instanceID uint, year, month, day, hour int) *monitoringModel.PmacctTrafficRecord {
	timestamp := time.Now()
	if start, _, bounded := pmacctAggregateWindow(year, month, day, hour); bounded {
		timestamp = start
	}
	return &monitoringModel.PmacctTrafficRecord{
		InstanceID: instanceID,
		Year:       year,
		Month:      month,
		Day:        day,
		Hour:       hour,
		Timestamp:  timestamp,
		RecordTime: time.Now(),
	}
}

func pmacctRecordTimestamp(record monitoringModel.PmacctTrafficRecord) time.Time {
	if !record.Timestamp.IsZero() {
		return record.Timestamp
	}
	return record.RecordTime
}

func pmacctDayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
