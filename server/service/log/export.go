package log

import (
	"bytes"
	"context"
	"encoding/csv"
	"oneclickvirt/service/database"
	"strconv"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	"oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type ExportService struct{}

// ExportUsers 导出用户数据
func (s *ExportService) ExportUsers(req auth.ExportUsersRequest) ([]byte, string, error) {
	var users []user.User
	dbService := database.GetDatabaseService()

	global.APP_LOG.Info("开始导出用户数据",
		zap.Int("userIdCount", len(req.UserIDs)),
		zap.String("format", req.Format),
		zap.Strings("fields", req.Fields))

	// 使用数据库抽象层执行查询
	err := dbService.ExecuteQuery(context.Background(), func() error {
		// 构建查询
		db := global.APP_DB.Model(&user.User{})

		// 如果指定了用户ID，只导出指定用户
		if len(req.UserIDs) > 0 {
			db = db.Where("id IN ?", req.UserIDs)
		}

		// 状态过滤
		if req.Status != nil {
			db = db.Where("status = ?", *req.Status)
		}

		// 时间范围过滤
		if req.StartTime != "" {
			db = db.Where("created_at >= ?", req.StartTime)
		}
		if req.EndTime != "" {
			db = db.Where("created_at <= ?", req.EndTime)
		}

		// 查询用户数据
		return db.Preload("Roles").Find(&users).Error
	})

	if err != nil {
		global.APP_LOG.Error("查询用户数据失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, "", common.NewError(common.CodeDatabaseError, "查询用户数据失败")
	}

	global.APP_LOG.Debug("查询用户数据成功", zap.Int("userCount", len(users)))

	// 根据格式导出
	switch req.Format {
	case "csv":
		data, err := s.exportUsersToCSV(users, req.Fields)
		if err != nil {
			global.APP_LOG.Error("导出CSV失败",
				zap.String("error", utils.TruncateString(err.Error(), 200)))
			return nil, "", err
		}
		global.APP_LOG.Info("用户数据导出成功",
			zap.String("format", "csv"),
			zap.Int("userCount", len(users)),
			zap.Int("dataSize", len(data)))
		return data, "users.csv", err
	default:
		global.APP_LOG.Warn("不支持的导出格式", zap.String("format", req.Format))
		return nil, "", common.NewError(common.CodeInvalidParam, "目前只支持CSV格式导出")
	}
}

// exportUsersToCSV 导出用户数据为CSV
func (s *ExportService) exportUsersToCSV(users []user.User, fields []string) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	global.APP_LOG.Debug("开始生成CSV文件",
		zap.Int("userCount", len(users)),
		zap.Strings("fields", fields))

	// 写入表头
	headers := s.getUserHeaders(fields)
	if err := writer.Write(headers); err != nil {
		global.APP_LOG.Error("写入CSV表头失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, err
	}

	// 写入数据
	for i, user := range users {
		row := s.getUserRow(user, fields)
		if err := writer.Write(row); err != nil {
			global.APP_LOG.Error("写入用户数据失败",
				zap.Int("userIndex", i),
				zap.Uint("userId", user.ID),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		global.APP_LOG.Error("CSV写入器错误",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, err
	}

	global.APP_LOG.Debug("CSV文件生成成功",
		zap.Int("userCount", len(users)),
		zap.Int("dataSize", buf.Len()))

	return buf.Bytes(), nil
}

// getUserHeaders 获取用户数据表头
func (s *ExportService) getUserHeaders(fields []string) []string {
	headerMap := map[string]string{
		"id":         "ID",
		"username":   "用户名",
		"nickname":   "昵称",
		"email":      "邮箱",
		"phone":      "手机号",
		"telegram":   "Telegram",
		"qq":         "QQ",
		"status":     "状态",
		"level":      "等级",
		"usedQuota":  "已用配额",
		"totalQuota": "总配额",
		"userType":   "用户类型",
		"roles":      "角色",
		"createdAt":  "创建时间",
		"updatedAt":  "更新时间",
	}

	var headers []string
	for _, field := range fields {
		if header, exists := headerMap[field]; exists {
			headers = append(headers, header)
		}
	}
	return headers
}

// getUserRow 获取用户数据行
func (s *ExportService) getUserRow(user user.User, fields []string) []string {
	var row []string

	for _, field := range fields {
		var value string

		switch field {
		case "id":
			value = strconv.FormatUint(uint64(user.ID), 10)
		case "username":
			value = user.Username
		case "nickname":
			value = user.Nickname
		case "email":
			value = user.Email
		case "phone":
			value = user.Phone
		case "telegram":
			value = user.Telegram
		case "qq":
			value = user.QQ
		case "status":
			statusMap := map[int]string{0: "禁用", 1: "启用"}
			value = statusMap[user.Status]
		case "level":
			value = strconv.Itoa(user.Level)
		case "usedQuota":
			value = strconv.Itoa(user.UsedQuota)
		case "totalQuota":
			value = strconv.Itoa(user.TotalQuota)
		case "userType":
			value = user.UserType
		case "roles":
			value = "N/A"
		case "createdAt":
			value = user.CreatedAt.Format("2006-01-02 15:04:05")
		case "updatedAt":
			value = user.UpdatedAt.Format("2006-01-02 15:04:05")
		}

		row = append(row, value)
	}

	return row
}

// ExportOperationLogs 导出操作日志
func (s *ExportService) ExportOperationLogs(req auth.ExportOperationLogsRequest) ([]byte, string, error) {
	var logs []adminModel.AuditLog
	dbService := database.GetDatabaseService()

	global.APP_LOG.Info("开始导出操作日志",
		zap.String("startTime", req.StartTime),
		zap.String("endTime", req.EndTime),
		zap.String("format", req.Format))

	// 使用数据库抽象层执行查询
	err := dbService.ExecuteQuery(context.Background(), func() error {
		// 构建查询
		db := global.APP_DB.Model(&adminModel.AuditLog{})

		// 时间范围过滤（必需）
		db = db.Where("created_at >= ? AND created_at <= ?", req.StartTime, req.EndTime)

		// 用户过滤
		if req.UserID != nil {
			db = db.Where("user_id = ?", *req.UserID)
		}

		// 操作类型过滤
		if req.Action != "" {
			db = db.Where("path LIKE ?", "%"+req.Action+"%")
		}

		// 资源过滤
		if req.Resource != "" {
			db = db.Where("path LIKE ?", "%"+req.Resource+"%")
		}

		// 查询操作日志
		return db.Order("created_at DESC").Find(&logs).Error
	})

	if err != nil {
		global.APP_LOG.Error("查询操作日志失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, "", common.NewError(common.CodeDatabaseError, "查询操作日志失败")
	}

	global.APP_LOG.Debug("查询操作日志成功", zap.Int("logCount", len(logs)))

	// 根据格式导出
	switch req.Format {
	case "csv":
		data, err := s.exportLogsToCSV(logs)
		if err != nil {
			global.APP_LOG.Error("导出日志CSV失败",
				zap.String("error", utils.TruncateString(err.Error(), 200)))
			return nil, "", err
		}
		global.APP_LOG.Info("操作日志导出成功",
			zap.String("format", "csv"),
			zap.Int("logCount", len(logs)),
			zap.Int("dataSize", len(data)))
		return data, "operation_logs.csv", err
	default:
		global.APP_LOG.Warn("不支持的日志导出格式", zap.String("format", req.Format))
		return nil, "", common.NewError(common.CodeInvalidParam, "目前只支持CSV格式导出")
	}
}

// exportLogsToCSV 导出日志为CSV
func (s *ExportService) exportLogsToCSV(logs []adminModel.AuditLog) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	global.APP_LOG.Debug("开始生成日志CSV文件", zap.Int("logCount", len(logs)))

	// 写入表头
	headers := []string{"ID", "用户ID", "用户名", "方法", "路径", "状态码", "耗时(ms)", "客户端IP", "用户代理", "创建时间"}
	if err := writer.Write(headers); err != nil {
		global.APP_LOG.Error("写入日志CSV表头失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, err
	}

	// 写入数据
	for i, log := range logs {
		var userIDStr string
		if log.UserID != nil {
			userIDStr = strconv.FormatUint(uint64(*log.UserID), 10)
		} else {
			userIDStr = "0"
		}

		row := []string{
			strconv.FormatUint(uint64(log.ID), 10),
			userIDStr,
			log.Username,
			log.Method,
			utils.TruncateString(log.Path, 100),
			strconv.Itoa(log.StatusCode),
			strconv.FormatInt(log.Latency, 10),
			log.ClientIP,
			utils.TruncateString(log.UserAgent, 200),
			log.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if err := writer.Write(row); err != nil {
			global.APP_LOG.Error("写入日志数据失败",
				zap.Int("logIndex", i),
				zap.Uint("logId", log.ID),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		global.APP_LOG.Error("日志CSV写入器错误",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, err
	}

	global.APP_LOG.Debug("日志CSV文件生成成功",
		zap.Int("logCount", len(logs)),
		zap.Int("dataSize", buf.Len()))

	return buf.Bytes(), nil
}
