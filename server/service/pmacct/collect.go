package pmacct

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// trafficData 表示从SQLite解析的单条流量数据
type trafficData struct {
	year       int
	month      int
	day        int
	hour       int
	minute     int
	timestamp  time.Time
	txBytes    int64
	rxBytes    int64
	totalBytes int64
}

// CollectTrafficFromSQLite 从远程 pmacct SQLite 数据库采集流量数据并导入系统数据库
// 架构：Memory(1min) -> SQLite(local) -> MySQL(remote, dynamic interval)
// 参数：预加载的instance和monitor数据
// 策略：固定查询最近30分钟，MySQL自动去重累加
func (s *Service) CollectTrafficFromSQLite(instance *providerModel.Instance, monitor *monitoringModel.PmacctMonitor) error {
	instanceID := instance.ID

	// 获取provider记录（用于验证和缓存刷新）
	var providerRecord providerModel.Provider
	if err := global.APP_DB.First(&providerRecord, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("failed to find provider: %w", err)
	}

	// 获取provider实例（如果缓存不存在则刷新）
	providerInstance, exists := providerService.GetProviderService().GetProviderByID(instance.ProviderID)
	if !exists {
		// Provider缓存不存在，尝试重新加载
		global.APP_LOG.Warn("Provider缓存未找到，尝试重新加载",
			zap.Uint("providerID", instance.ProviderID),
			zap.Uint("instanceID", instanceID))

		// 重新从数据库加载provider并注册
		if err := s.refreshProviderCache(instance.ProviderID, &providerRecord); err != nil {
			return fmt.Errorf("failed to refresh provider cache: %w", err)
		}

		// 再次尝试获取
		providerInstance, exists = providerService.GetProviderService().GetProviderByID(instance.ProviderID)
		if !exists {
			return fmt.Errorf("provider ID %d still not found after refresh", instance.ProviderID)
		}
	}

	s.SetProviderID(instance.ProviderID)

	// SQLite数据库路径（每个实例独立）
	dbPath := fmt.Sprintf("/var/lib/pmacct/%s/traffic.db", instance.Name)

	global.APP_LOG.Debug("开始从 SQLite 采集流量数据",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("dbPath", dbPath))

	// 策略：固定查询最近30分钟（不依赖lastSync），MySQL端自动去重累加
	// lastSync仅用于记录上次同步时间，方便排查问题
	var lastSync time.Time
	if monitor.LastSync.IsZero() {
		lastSync = time.Now().Add(-30 * time.Minute)
		global.APP_LOG.Debug("首次采集（固定查询最近30分钟）",
			zap.Uint("instanceID", instanceID))
	} else {
		lastSync = monitor.LastSync
		global.APP_LOG.Debug("常规采集（固定查询最近30分钟）",
			zap.Uint("instanceID", instanceID),
			zap.Time("lastSync", lastSync))
	}

	// 检查 SQLite 文件是否存在
	checkCmd := fmt.Sprintf("test -f %s && echo 'exists' || echo 'not_found'", dbPath)
	ctx1, cancel1 := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel1()

	checkResult, err := providerInstance.ExecuteSSHCommand(ctx1, checkCmd)
	if err != nil || strings.TrimSpace(checkResult) != "exists" {
		global.APP_LOG.Warn("SQLite 文件不存在，跳过采集",
			zap.Uint("instanceID", instanceID),
			zap.String("dbPath", dbPath))
		return nil
	}

	// 确定查询用的IP
	queryIPv4 := instance.PrivateIP
	if queryIPv4 == "" {
		// 非NAT虚拟化，使用公网IP
		queryIPv4 = monitor.MappedIP
	}

	queryIPv6 := monitor.MappedIPv6 // IPv6直接使用公网IP

	// 如果两个IP都为空，无法查询
	if queryIPv4 == "" && queryIPv6 == "" {
		return fmt.Errorf("实例没有可用的IP地址：PrivateIP=%s, MappedIP=%s, MappedIPv6=%s",
			instance.PrivateIP, monitor.MappedIP, monitor.MappedIPv6)
	}

	// 构建IP列表和WHERE条件（避免空字符串导致的匹配错误）
	var ipList []string
	if queryIPv4 != "" {
		ipList = append(ipList, queryIPv4)
	}
	if queryIPv6 != "" {
		ipList = append(ipList, queryIPv6)
	}

	// 构建SQL IN子句
	ipInClause := "'" + strings.Join(ipList, "','") + "'"

	// 核心策略：直接查询每个时间点的累积值，不按时间分组
	// - pmacct的acct_v9表中每条记录的bytes字段是该记录的流量增量
	// - 需要按时间顺序累加这些增量，得到每个时间点的累积值
	// - MySQL存储每个时间点的累积值，前端通过差值计算实际流量
	// - 每天4点重置后，累积值从0重新开始
	global.APP_LOG.Debug("SQLite查询参数，计算累积值",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("queryIPv4", queryIPv4),
		zap.String("queryIPv6", queryIPv6),
		zap.String("ipInClause", ipInClause),
		zap.String("dbPath", dbPath),
		zap.String("strategy", "窗口函数累加计算累积值"))

	// 使用窗口函数计算累积值
	// 1. 先按5分钟时间段分组求和得到每个时段的流量增量
	// 2. 再使用SUM() OVER()窗口函数累加，得到累积值
	// stamp_inserted: pmacct写入时间
	// bytes: 每条记录的流量增量（不是累积值）
	// 添加LIMIT防止返回过多数据
	query := fmt.Sprintf(`sqlite3 %s "
WITH time_slots AS (
    SELECT 
        strftime('%%Y', stamp_inserted) as year,
        strftime('%%m', stamp_inserted) as month,
        strftime('%%d', stamp_inserted) as day,
        strftime('%%H', stamp_inserted) as hour,
        CAST((CAST(strftime('%%M', stamp_inserted) AS INTEGER) / 5) * 5 AS TEXT) as minute,
        strftime('%%Y-%%m-%%d %%H:', stamp_inserted) || printf('%%02d', (CAST(strftime('%%M', stamp_inserted) AS INTEGER) / 5) * 5) || ':00' as timestamp,
        SUM(CASE 
            WHEN COALESCE(src_host, ip_src) IN (%s)
             AND COALESCE(dst_host, ip_dst) NOT IN (%s)
            THEN bytes ELSE 0 
        END) as tx_increment,
        SUM(CASE 
            WHEN COALESCE(dst_host, ip_dst) IN (%s)
             AND COALESCE(src_host, ip_src) NOT IN (%s)
            THEN bytes ELSE 0 
        END) as rx_increment
    FROM acct_v9
    WHERE (
        COALESCE(src_host, ip_src) IN (%s)
        OR
        COALESCE(dst_host, ip_dst) IN (%s)
    )
    GROUP BY year, month, day, hour, minute, timestamp
)
SELECT 
    year,
    month,
    day,
    hour,
    minute,
    timestamp,
    SUM(tx_increment) OVER (ORDER BY timestamp) as tx_bytes,
    SUM(rx_increment) OVER (ORDER BY timestamp) as rx_bytes
FROM time_slots
ORDER BY timestamp
LIMIT 10000;
"`, dbPath,
		ipInClause, ipInClause,
		ipInClause, ipInClause,
		ipInClause, ipInClause)

	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	output, err := providerInstance.ExecuteSSHCommand(ctx, query)
	if err != nil {
		global.APP_LOG.Error("SQLite查询失败",
			zap.Uint("instanceID", instanceID),
			zap.String("dbPath", dbPath),
			zap.Error(err),
			zap.String("output", output))
		return fmt.Errorf("failed to query SQLite database: %w", err)
	}

	global.APP_LOG.Debug("SQLite查询结果",
		zap.Uint("instanceID", instanceID),
		zap.Int("outputLength", len(output)),
		zap.String("outputPreview", func() string {
			if len(output) > 200 {
				return output[:200] + "..."
			}
			return output
		}()))

	// 使用当前时间记录数据采集时间
	providerCurrentTimeStr := time.Now().Format("2006-01-02 15:04:05") // 解析查询结果
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		// SQLite查询无数据的情况有两种可能：
		// 1. 采集成功但流量为0（正常情况）
		// 2. 采集失败/连接异常（异常情况）
		//
		// 策略：不更新last_sync，让下次继续尝试采集
		// - 如果真的是流量为0，下次仍然会返回空数据，这是可以接受的
		// - 如果是连接异常，下次恢复后会采集到累积的流量数据，触发填补逻辑
		global.APP_LOG.Debug("SQLite查询无数据，跳过本次采集（不更新last_sync）",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.String("reason", "无法区分流量为0或采集失败，保守策略是不更新同步时间"))

		return nil
	}

	// 第一步：解析所有数据行（事务外）
	var dataList []trafficData

	for _, line := range lines {
		if line == "" {
			continue
		}

		// 解析数据行: year|month|day|hour|minute|timestamp|tx_bytes|rx_bytes
		parts := strings.Split(line, "|")
		if len(parts) != 8 {
			global.APP_LOG.Debug("跳过无效数据行",
				zap.String("line", line),
				zap.Int("parts", len(parts)))
			continue
		}

		year, _ := strconv.Atoi(parts[0])
		month, _ := strconv.Atoi(parts[1])
		day, _ := strconv.Atoi(parts[2])
		hour, _ := strconv.Atoi(parts[3])
		minute, _ := strconv.Atoi(parts[4])
		timestampStr := parts[5]
		txBytes, _ := strconv.ParseInt(parts[6], 10, 64)
		rxBytes, _ := strconv.ParseInt(parts[7], 10, 64)

		// 解析时间戳
		timestamp, err := time.Parse("2006-01-02 15:04:05", timestampStr)
		if err != nil {
			global.APP_LOG.Debug("解析时间戳失败",
				zap.String("timestamp", timestampStr),
				zap.Error(err))
			continue
		}

		dataList = append(dataList, trafficData{
			year:       year,
			month:      month,
			day:        day,
			hour:       hour,
			minute:     minute,
			timestamp:  timestamp,
			txBytes:    txBytes,
			rxBytes:    rxBytes,
			totalBytes: txBytes + rxBytes,
		})
	}

	if len(dataList) == 0 {
		// 解析后无有效数据（可能是格式错误），不更新lastSync
		// 保持与"查询无数据"时的行为一致
		global.APP_LOG.Debug("解析后无有效流量数据",
			zap.Uint("instanceID", instanceID),
			zap.Int("totalLines", len(lines)))
		return nil
	}

	// 准备批量插入数据（直接使用ON DUPLICATE KEY UPDATE去重）
	var recordsToCreate []monitoringModel.PmacctTrafficRecord
	for _, data := range dataList {
		recordsToCreate = append(recordsToCreate, monitoringModel.PmacctTrafficRecord{
			InstanceID:   instanceID,
			UserID:       instance.UserID,
			ProviderID:   instance.ProviderID,
			ProviderType: instance.Provider,
			MappedIP:     monitor.MappedIP,
			RxBytes:      data.rxBytes,
			TxBytes:      data.txBytes,
			TotalBytes:   data.totalBytes,
			Timestamp:    data.timestamp,
			Year:         data.year,
			Month:        data.month,
			Day:          data.day,
			Hour:         data.hour,
			Minute:       data.minute,
		})
	}

	// 查询该instance最近一次有效数据的最大流量值（在事务外预查询）
	// 用于检测连接异常恢复后的场景
	var lastMax lastMaxTraffic

	// 查询最近一次有数据的记录（rx_bytes > 0 OR tx_bytes > 0）
	// 使用子查询确保兼容 MySQL 5.x/9.x 和 MariaDB 5.x/9.x
	err = global.APP_DB.Raw(`
		SELECT 
			COALESCE(MAX(rx_bytes), 0) as max_rx_bytes,
			COALESCE(MAX(tx_bytes), 0) as max_tx_bytes,
			COALESCE(MAX(total_bytes), 0) as max_total_bytes,
			MAX(timestamp) as last_timestamp
		FROM pmacct_traffic_records
		WHERE instance_id = ? 
		  AND (rx_bytes > 0 OR tx_bytes > 0)
		  AND timestamp < ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, instanceID, dataList[0].timestamp).Scan(&lastMax).Error

	if err != nil {
		global.APP_LOG.Warn("查询历史最大流量失败，继续执行",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		lastMax = lastMaxTraffic{}
	}

	// 检测是否所有新数据都大于上一次的最大值（连续性检测）
	isContinuous := s.checkContinuity(instanceID, dataList, lastMax)

	// 填补空白期数据（如果需要）
	if isContinuous {
		s.fillGapRecords(instanceID, instance, monitor, lastMax, dataList[0].timestamp, providerCurrentTimeStr)
	}

	// 批量插入新采集的数据
	imported, err := s.batchUpsertRecords(instanceID, recordsToCreate, providerCurrentTimeStr)
	if err != nil {
		return err
	}

	// 更新最后同步时间
	if err := global.APP_DB.Exec(
		"UPDATE pmacct_monitors SET last_sync = ? WHERE instance_id = ?",
		providerCurrentTimeStr, instanceID).Error; err != nil {
		global.APP_LOG.Error("更新同步时间失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("failed to update last_sync: %w", err)
	}

	// 同步更新历史表
	if imported > 0 {
		s.syncTrafficHistories(instanceID, instance)
	}

	// 不进行增量清理SQLite数据，因为：
	// 1. flush到SQLite的数据是每分钟的增量，不是累积值
	// 2. 增量清理不会导致数据不准确
	// 3. 完整的清理由定期重置pmacct守护进程完成（见ResetPmacctDaemon）

	global.APP_LOG.Debug("SQLite 流量数据采集完成",
		zap.Uint("instanceID", instanceID),
		zap.Int("records", imported),
		zap.String("deduplication", "MySQL自动去重累加"),
		zap.Time("lastSync", lastSync),
		zap.String("currentSync", providerCurrentTimeStr))

	return nil
}

// checkContinuity 检测数据连续性，判断是否为连接异常恢复场景
func (s *Service) checkContinuity(instanceID uint, dataList []trafficData, lastMax lastMaxTraffic) bool {
	if lastMax.MaxTotalBytes <= 0 {
		return false
	}

	for _, data := range dataList {
		if data.rxBytes < lastMax.MaxRxBytes || data.txBytes < lastMax.MaxTxBytes {
			global.APP_LOG.Debug("新数据不满足连续性条件，可能是监控重建",
				zap.Uint("instanceID", instanceID),
				zap.Int64("newRx", data.rxBytes),
				zap.Int64("lastMaxRx", lastMax.MaxRxBytes),
				zap.Int64("newTx", data.txBytes),
				zap.Int64("lastMaxTx", lastMax.MaxTxBytes))
			return false
		}
	}

	var lastTimestampLog time.Time
	if lastMax.LastTimestamp != nil {
		lastTimestampLog = *lastMax.LastTimestamp
	}
	global.APP_LOG.Info("检测到连接异常恢复场景（所有新数据>=上次最大值），将填补空白期数据",
		zap.Uint("instanceID", instanceID),
		zap.Int64("lastMaxRx", lastMax.MaxRxBytes),
		zap.Int64("lastMaxTx", lastMax.MaxTxBytes),
		zap.Time("lastTimestamp", lastTimestampLog),
		zap.Time("firstNewTimestamp", dataList[0].timestamp),
		zap.Int("newDataCount", len(dataList)))

	return true
}
