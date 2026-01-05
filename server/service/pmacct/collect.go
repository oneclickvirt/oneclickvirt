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
	"gorm.io/gorm"
)

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

	global.APP_LOG.Info("开始从 SQLite 采集流量数据",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("dbPath", dbPath))

	// 策略：固定查询最近30分钟（不依赖lastSync），MySQL端自动去重累加
	// lastSync仅用于记录上次同步时间，方便排查问题
	var lastSync time.Time
	if monitor.LastSync.IsZero() {
		lastSync = time.Now().Add(-30 * time.Minute)
		global.APP_LOG.Info("首次采集（固定查询最近30分钟）",
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
	global.APP_LOG.Info("SQLite查询参数，计算累积值",
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
	var dataList []trafficData

	for _, line := range lines {
		if line == "" {
			continue
		}

		// 解析数据行: year|month|day|hour|minute|timestamp|tx_bytes|rx_bytes
		parts := strings.Split(line, "|")
		if len(parts) != 8 {
			global.APP_LOG.Warn("跳过无效数据行",
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
			global.APP_LOG.Warn("解析时间戳失败",
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

	// 第二步：查询该instance最近一次有效数据的最大流量值（在事务外预查询）
	// 用于检测连接异常恢复后的场景
	type lastMaxTraffic struct {
		MaxRxBytes    int64
		MaxTxBytes    int64
		MaxTotalBytes int64
		LastTimestamp time.Time
	}
	var lastMax lastMaxTraffic

	// 查询最近一次有数据的记录（rx_bytes > 0 OR tx_bytes > 0）
	// 使用子查询确保兼容 MySQL 5.x/9.x 和 MariaDB 5.x/9.x
	err = global.APP_DB.Raw(`
		SELECT 
			COALESCE(MAX(rx_bytes), 0) as max_rx_bytes,
			COALESCE(MAX(tx_bytes), 0) as max_tx_bytes,
			COALESCE(MAX(total_bytes), 0) as max_total_bytes,
			COALESCE(MAX(timestamp), ?) as last_timestamp
		FROM pmacct_traffic_records
		WHERE instance_id = ? 
		  AND (rx_bytes > 0 OR tx_bytes > 0)
		  AND timestamp < ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, time.Unix(0, 0), instanceID, dataList[0].timestamp).Scan(&lastMax).Error

	if err != nil {
		global.APP_LOG.Warn("查询历史最大流量失败，继续执行",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		// 如果查询失败，设置为0，不影响后续逻辑
		lastMax = lastMaxTraffic{}
	}

	// 第三步：检测是否所有新数据都大于上一次的最大值（连续性检测）
	// 如果是连续的，说明是连接异常恢复，需要填补空白期
	//
	// 判断逻辑：
	// 1. 上一次有有效数据（MaxTotalBytes > 0）
	// 2. 所有新数据的rx和tx都 >= 上一次的最大值（严格要求两个维度都满足）
	// 3. 这样可以区分：
	//    - 监控重建：新数据从0或很小值开始 → 不满足条件 → 不填补
	//    - 连接异常恢复：新数据继续累积增长 → 满足条件 → 填补空白期
	isContinuous := false
	if lastMax.MaxTotalBytes > 0 {
		// 检查所有新数据是否都大于等于上一次的最大值（rx和tx都要满足）
		allGreaterOrEqual := true
		for _, data := range dataList {
			// 只要有一个维度小于上次最大值，就不是连续场景
			if data.rxBytes < lastMax.MaxRxBytes || data.txBytes < lastMax.MaxTxBytes {
				allGreaterOrEqual = false
				global.APP_LOG.Debug("新数据不满足连续性条件，可能是监控重建",
					zap.Uint("instanceID", instanceID),
					zap.Int64("newRx", data.rxBytes),
					zap.Int64("lastMaxRx", lastMax.MaxRxBytes),
					zap.Int64("newTx", data.txBytes),
					zap.Int64("lastMaxTx", lastMax.MaxTxBytes))
				break
			}
		}
		isContinuous = allGreaterOrEqual

		if isContinuous {
			global.APP_LOG.Info("检测到连接异常恢复场景（所有新数据>=上次最大值），将填补空白期数据",
				zap.Uint("instanceID", instanceID),
				zap.Int64("lastMaxRx", lastMax.MaxRxBytes),
				zap.Int64("lastMaxTx", lastMax.MaxTxBytes),
				zap.Time("lastTimestamp", lastMax.LastTimestamp),
				zap.Time("firstNewTimestamp", dataList[0].timestamp),
				zap.Int("newDataCount", len(dataList)))
		}
	}

	// 拆分事务：分批处理避免长时间锁表
	var imported int

	// 第一步：填补空白期数据（如果需要）
	if isContinuous && !lastMax.LastTimestamp.IsZero() {
		fillStart := lastMax.LastTimestamp.Add(time.Minute)
		fillEnd := dataList[0].timestamp.Add(-time.Minute)

		if fillStart.Before(fillEnd) || fillStart.Equal(fillEnd) {
			// 生成填补数据
			var fillRecords []monitoringModel.PmacctTrafficRecord
			for current := fillStart; current.Before(dataList[0].timestamp); current = current.Add(time.Minute) {
				fillRecords = append(fillRecords, monitoringModel.PmacctTrafficRecord{
					InstanceID:   instanceID,
					UserID:       instance.UserID,
					ProviderID:   instance.ProviderID,
					ProviderType: instance.Provider,
					MappedIP:     monitor.MappedIP,
					RxBytes:      lastMax.MaxRxBytes,
					TxBytes:      lastMax.MaxTxBytes,
					TotalBytes:   lastMax.MaxTotalBytes,
					Timestamp:    current,
					Year:         current.Year(),
					Month:        int(current.Month()),
					Day:          current.Day(),
					Hour:         current.Hour(),
					Minute:       current.Minute(),
				})
			}

			// 批量插入填补数据（每扵50条，独立事务）
			if len(fillRecords) > 0 {
				fillBatchSize := 50
				for i := 0; i < len(fillRecords); i += fillBatchSize {
					end := i + fillBatchSize
					if end > len(fillRecords) {
						end = len(fillRecords)
					}
					batch := fillRecords[i:end]

					// 每批使用独立的短事务
					err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
						values := make([]string, 0, len(batch))
						args := make([]interface{}, 0, len(batch)*15)

						for _, record := range batch {
							values = append(values, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
							args = append(args,
								record.InstanceID,
								record.UserID,
								record.ProviderID,
								record.ProviderType,
								record.MappedIP,
								record.RxBytes,
								record.TxBytes,
								record.TotalBytes,
								record.Timestamp,
								record.Year,
								record.Month,
								record.Day,
								record.Hour,
								record.Minute,
								providerCurrentTimeStr,
							)
						}

						insertSQL := fmt.Sprintf(`
							INSERT IGNORE INTO pmacct_traffic_records 
							(instance_id, user_id, provider_id, provider_type, mapped_ip, 
							 rx_bytes, tx_bytes, total_bytes, timestamp, 
							 year, month, day, hour, minute, record_time)
							VALUES %s
						`, strings.Join(values, ","))

						return tx.Exec(insertSQL, args...).Error
					})

					if err != nil {
						global.APP_LOG.Warn("填补空白期数据失败（继续执行）",
							zap.Uint("instanceID", instanceID),
							zap.Int("count", len(batch)),
							zap.Error(err))
					}
				}

				global.APP_LOG.Info("已填补空白期数据",
					zap.Uint("instanceID", instanceID),
					zap.Int("fillCount", len(fillRecords)),
					zap.Time("fillStart", fillStart),
					zap.Time("fillEnd", fillEnd.Add(time.Minute)))
			}
		}
	}

	// 第二步：批量插入新采集的数据（每批独立事务，避免长时间锁表）
	batchSize := 50
	for i := 0; i < len(recordsToCreate); i += batchSize {
		end := i + batchSize
		if end > len(recordsToCreate) {
			end = len(recordsToCreate)
		}
		batch := recordsToCreate[i:end]

		// 每批使用独立的短事务
		err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
			values := make([]string, 0, len(batch))
			args := make([]interface{}, 0, len(batch)*15)

			for _, record := range batch {
				values = append(values, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
				args = append(args,
					record.InstanceID,
					record.UserID,
					record.ProviderID,
					record.ProviderType,
					record.MappedIP,
					record.RxBytes,
					record.TxBytes,
					record.TotalBytes,
					record.Timestamp,
					record.Year,
					record.Month,
					record.Day,
					record.Hour,
					record.Minute,
					providerCurrentTimeStr,
				)
			}

			insertSQL := fmt.Sprintf(`
				INSERT INTO pmacct_traffic_records 
				(instance_id, user_id, provider_id, provider_type, mapped_ip, 
				 rx_bytes, tx_bytes, total_bytes, timestamp, 
				 year, month, day, hour, minute, record_time)
				VALUES %s
				ON DUPLICATE KEY UPDATE
					rx_bytes = IF(
						TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(rx_bytes) > pmacct_traffic_records.rx_bytes,
						VALUES(rx_bytes),
						pmacct_traffic_records.rx_bytes
					),
					tx_bytes = IF(
						TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(tx_bytes) > pmacct_traffic_records.tx_bytes,
						VALUES(tx_bytes),
						pmacct_traffic_records.tx_bytes
					),
					total_bytes = IF(
						TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(total_bytes) > pmacct_traffic_records.total_bytes,
						VALUES(total_bytes),
						pmacct_traffic_records.total_bytes
					),
					record_time = IF(
						TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(total_bytes) > pmacct_traffic_records.total_bytes,
						VALUES(record_time),
						pmacct_traffic_records.record_time
					)
			`, strings.Join(values, ","))

			return tx.Exec(insertSQL, args...).Error
		})

		if err != nil {
			global.APP_LOG.Error("批量创建流量记录失败",
				zap.Uint("instanceID", instanceID),
				zap.Int("count", len(batch)),
				zap.Error(err))
			return fmt.Errorf("failed to batch create records: %w", err)
		}
		imported += len(batch)
	}

	// 第三步：更新最后同步时间（独立小事务）
	if err := global.APP_DB.Exec(
		"UPDATE pmacct_monitors SET last_sync = ? WHERE instance_id = ?",
		providerCurrentTimeStr, instanceID).Error; err != nil {
		global.APP_LOG.Error("更新同步时间失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("failed to update last_sync: %w", err)
	}

	// 同步更新历史表（在主事务成功后执行，失败不影响采集）
	// pmacct_traffic_records存储的是累积值快照，历史表应存储时间段内的最大累积值
	// 前端/API查询时通过相邻时间点的差值计算实际使用量
	if imported > 0 {
		now := time.Now()
		year, month := now.Year(), int(now.Month())
		day, hour := now.Day(), now.Hour()

		// 更新实例流量历史表（小时级，存储该小时最新的累积值）
		// 先查询聚合结果
		var hourlyData struct {
			InstanceID uint
			ProviderID uint
			UserID     uint
			TrafficIn  int64
			TrafficOut int64
			TotalUsed  int64
		}

		// 注意：pmacct_traffic_records 表中是字节，需要转换为 MB 插入 instance_traffic_histories
		err := global.APP_DB.Table("pmacct_traffic_records").
			Select("instance_id, provider_id, user_id, MAX(rx_bytes)/1048576.0 as traffic_in, MAX(tx_bytes)/1048576.0 as traffic_out, MAX(total_bytes)/1048576.0 as total_used").
			Where("instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL", instanceID, year, month, day, hour).
			Group("instance_id, provider_id, user_id, year, month, day, hour").
			Scan(&hourlyData).Error

		if err == nil && hourlyData.InstanceID > 0 {
			// 使用GORM保存或更新
			var existing monitoringModel.InstanceTrafficHistory
			err = global.APP_DB.Where(
				"instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ?",
				instanceID, year, month, day, hour,
			).First(&existing).Error

			if err == nil {
				// 更新现有记录
				existing.ProviderID = hourlyData.ProviderID
				existing.UserID = hourlyData.UserID
				existing.TrafficIn = hourlyData.TrafficIn
				existing.TrafficOut = hourlyData.TrafficOut
				existing.TotalUsed = hourlyData.TotalUsed
				existing.RecordTime = now
				if err := global.APP_DB.Save(&existing).Error; err != nil {
					global.APP_LOG.Warn("更新实例流量历史失败",
						zap.Uint("instanceID", instanceID),
						zap.Error(err))
				}
			} else {
				// 插入新记录
				newRecord := monitoringModel.InstanceTrafficHistory{
					InstanceID: hourlyData.InstanceID,
					ProviderID: hourlyData.ProviderID,
					UserID:     hourlyData.UserID,
					TrafficIn:  hourlyData.TrafficIn,
					TrafficOut: hourlyData.TrafficOut,
					TotalUsed:  hourlyData.TotalUsed,
					Year:       year,
					Month:      month,
					Day:        day,
					Hour:       hour,
					RecordTime: now,
				}
				if err := global.APP_DB.Create(&newRecord).Error; err != nil {
					global.APP_LOG.Warn("插入实例流量历史失败",
						zap.Uint("instanceID", instanceID),
						zap.Error(err))
				}
			}
		}

		// 更新实例月度汇总（day=0, hour=0）
		// 支持pmacct每天4点自动重置，使用分段检测避免数据丢失
		// 先执行聚合查询
		var monthlyData struct {
			InstanceID uint
			ProviderID uint
			UserID     uint
			TrafficIn  int64
			TrafficOut int64
			TotalUsed  int64
		}

		// 注意：pmacct_traffic_records 表中是字节，需要转换为 MB 插入 instance_traffic_histories
		err = global.APP_DB.Raw(`
			SELECT 
				instance_id,
				provider_id,
				user_id,
				COALESCE(SUM(segment_max_rx), 0) / 1048576.0 as traffic_in,
				COALESCE(SUM(segment_max_tx), 0) / 1048576.0 as traffic_out,
				COALESCE(SUM(segment_max_total), 0) / 1048576.0 as total_used
			FROM (
				SELECT 
					instance_id, provider_id, user_id,
					segment_id,
					MAX(rx_bytes) as segment_max_rx,
					MAX(tx_bytes) as segment_max_tx,
					MAX(total_bytes) as segment_max_total
				FROM (
					SELECT 
						t1.instance_id,
						t1.provider_id,
						t1.user_id,
						t1.rx_bytes,
						t1.tx_bytes,
						t1.total_bytes,
						(
							SELECT COUNT(DISTINCT t2.id)
							FROM pmacct_traffic_records t2
							LEFT JOIN pmacct_traffic_records t3 ON t2.instance_id = t3.instance_id 
								AND t3.timestamp = (
									SELECT MAX(timestamp) 
									FROM pmacct_traffic_records 
									WHERE instance_id = t2.instance_id 
										AND timestamp < t2.timestamp
										AND year = t1.year AND month = t1.month
								)
							WHERE t2.instance_id = t1.instance_id
								AND t2.year = t1.year AND t2.month = t1.month
								AND t2.timestamp <= t1.timestamp
								AND t3.id IS NOT NULL
								AND (t2.rx_bytes < t3.rx_bytes OR t2.tx_bytes < t3.tx_bytes)
						) as segment_id
					FROM pmacct_traffic_records t1
					WHERE t1.instance_id = ? AND t1.year = ? AND t1.month = ? AND t1.deleted_at IS NULL
				) AS segments
				GROUP BY instance_id, provider_id, user_id, segment_id
			) AS segment_totals
			GROUP BY instance_id, provider_id, user_id
		`, instanceID, year, month).Scan(&monthlyData).Error

		if err == nil && monthlyData.InstanceID > 0 {
			// 使用GORM保存或更新月度汇总
			var existing monitoringModel.InstanceTrafficHistory
			err = global.APP_DB.Where(
				"instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ?",
				instanceID, year, month, 0, 0,
			).First(&existing).Error

			if err == nil {
				// 更新现有记录
				existing.ProviderID = monthlyData.ProviderID
				existing.UserID = monthlyData.UserID
				existing.TrafficIn = monthlyData.TrafficIn
				existing.TrafficOut = monthlyData.TrafficOut
				existing.TotalUsed = monthlyData.TotalUsed
				existing.RecordTime = now
				if err := global.APP_DB.Save(&existing).Error; err != nil {
					global.APP_LOG.Warn("更新实例月度汇总失败",
						zap.Uint("instanceID", instanceID),
						zap.Error(err))
				}
			} else {
				// 插入新记录
				newRecord := monitoringModel.InstanceTrafficHistory{
					InstanceID: monthlyData.InstanceID,
					ProviderID: monthlyData.ProviderID,
					UserID:     monthlyData.UserID,
					TrafficIn:  monthlyData.TrafficIn,
					TrafficOut: monthlyData.TrafficOut,
					TotalUsed:  monthlyData.TotalUsed,
					Year:       year,
					Month:      month,
					Day:        0,
					Hour:       0,
					RecordTime: now,
				}
				if err := global.APP_DB.Create(&newRecord).Error; err != nil {
					global.APP_LOG.Warn("插入实例月度汇总失败",
						zap.Uint("instanceID", instanceID),
						zap.Error(err))
				}
			}
		} else if err != nil {
			global.APP_LOG.Warn("查询月度汇总数据失败",
				zap.Uint("instanceID", instanceID),
				zap.Error(err))
		}

		// 更新Provider流量历史表（小时级，聚合所有实例）
		if err := global.APP_DB.Exec(`
			INSERT INTO provider_traffic_histories 
				(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT 
				provider_id,
				SUM(traffic_in) as traffic_in,      -- 所有实例的累积值之和
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, day, hour,
				? as record_time,
				? as created_at,
				? as updated_at
			FROM instance_traffic_histories
			WHERE provider_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
			GROUP BY provider_id, year, month, day, hour
			ON DUPLICATE KEY UPDATE
				traffic_in = VALUES(traffic_in),
				traffic_out = VALUES(traffic_out),
				total_used = VALUES(total_used),
				instance_count = VALUES(instance_count),
				record_time = VALUES(record_time),
				updated_at = VALUES(updated_at)
		`, now, now, now, instance.ProviderID, year, month, day, hour).Error; err != nil {
			global.APP_LOG.Warn("更新Provider流量历史失败",
				zap.Uint("providerID", instance.ProviderID),
				zap.Error(err))
		}

		// 更新Provider月度汇总（day=0, hour=0）
		if err := global.APP_DB.Exec(`
			INSERT INTO provider_traffic_histories 
				(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT 
				provider_id,
				SUM(traffic_in) as traffic_in,
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, 0 as day, 0 as hour,
				? as record_time,
				? as created_at,
				? as updated_at
			FROM instance_traffic_histories
			WHERE provider_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
			GROUP BY provider_id, year, month
			ON DUPLICATE KEY UPDATE
				traffic_in = VALUES(traffic_in),
				traffic_out = VALUES(traffic_out),
				total_used = VALUES(total_used),
				instance_count = VALUES(instance_count),
				record_time = VALUES(record_time),
				updated_at = VALUES(updated_at)
		`, now, now, now, instance.ProviderID, year, month).Error; err != nil {
			global.APP_LOG.Warn("更新Provider月度汇总失败",
				zap.Uint("providerID", instance.ProviderID),
				zap.Error(err))
		}

		// 更新用户流量历史表（小时级，聚合所有实例）
		if err := global.APP_DB.Exec(`
			INSERT INTO user_traffic_histories 
				(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT 
				user_id,
				SUM(traffic_in) as traffic_in,      -- 所有实例的累积值之和
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, day, hour,
				? as record_time,
				? as created_at,
				? as updated_at
			FROM instance_traffic_histories
			WHERE user_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
			GROUP BY user_id, year, month, day, hour
			ON DUPLICATE KEY UPDATE
				traffic_in = VALUES(traffic_in),
				traffic_out = VALUES(traffic_out),
				total_used = VALUES(total_used),
				instance_count = VALUES(instance_count),
				record_time = VALUES(record_time),
				updated_at = VALUES(updated_at)
		`, now, now, now, instance.UserID, year, month, day, hour).Error; err != nil {
			global.APP_LOG.Warn("更新用户流量历史失败",
				zap.Uint("userID", instance.UserID),
				zap.Error(err))
		}

		// 更新用户月度汇总（day=0, hour=0）
		if err := global.APP_DB.Exec(`
			INSERT INTO user_traffic_histories 
				(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT 
				user_id,
				SUM(traffic_in) as traffic_in,
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, 0 as day, 0 as hour,
				? as record_time,
				? as created_at,
				? as updated_at
			FROM instance_traffic_histories
			WHERE user_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
			GROUP BY user_id, year, month
			ON DUPLICATE KEY UPDATE
				traffic_in = VALUES(traffic_in),
				traffic_out = VALUES(traffic_out),
				total_used = VALUES(total_used),
				instance_count = VALUES(instance_count),
				record_time = VALUES(record_time),
				updated_at = VALUES(updated_at)
		`, now, now, now, instance.UserID, year, month).Error; err != nil {
			global.APP_LOG.Warn("更新用户月度汇总失败",
				zap.Uint("userID", instance.UserID),
				zap.Error(err))
		}
	}

	// 不进行增量清理SQLite数据，因为：
	// 1. flush到SQLite的数据是每分钟的增量，不是累积值
	// 2. 增量清理不会导致数据不准确
	// 3. 完整的清理由定期重置pmacct守护进程完成（见ResetPmacctDaemon）

	global.APP_LOG.Info("SQLite 流量数据采集完成",
		zap.Uint("instanceID", instanceID),
		zap.Int("records", imported),
		zap.String("deduplication", "MySQL自动去重累加"),
		zap.Time("lastSync", lastSync),
		zap.String("currentSync", providerCurrentTimeStr))

	return nil
}
