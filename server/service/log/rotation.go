package log

import (
	"bufio"
	"fmt"
	"io"
	"oneclickvirt/service/storage"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LogRotationService 日志轮转服务
type LogRotationService struct {
	mu      sync.RWMutex
	writers map[string]*RotatingFileWriter // 跟踪所有创建的writer
}

var (
	logRotationService     *LogRotationService
	logRotationServiceOnce sync.Once
)

// GetLogRotationService 获取日志轮转服务单例
func GetLogRotationService() *LogRotationService {
	logRotationServiceOnce.Do(func() {
		logRotationService = &LogRotationService{
			writers: make(map[string]*RotatingFileWriter),
		}
	})
	return logRotationService
}

// Stop 关闭所有日志文件
func (s *LogRotationService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, writer := range s.writers {
		// 忽略关闭错误，避免日志调用导致递归死锁
		_ = writer.Close()
	}
	s.writers = make(map[string]*RotatingFileWriter)
}

// GetDefaultDailyLogConfig 获取默认日志分日期配置
func GetDefaultDailyLogConfig() *config.DailyLogConfig {
	storageService := storage.GetStorageService()

	// 从配置文件读取日志保留天数，如果配置为0或负数，则使用默认值7天
	retentionDays := global.APP_CONFIG.Zap.RetentionDay
	if retentionDays <= 0 {
		retentionDays = 7
	}

	// 从配置文件读取日志文件大小，如果配置为0或负数，则使用默认值10MB
	maxFileSize := global.APP_CONFIG.Zap.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = 10
	}

	// 从配置文件读取最大备份数量，如果配置为0或负数，则使用默认值30
	maxBackups := global.APP_CONFIG.Zap.MaxBackups
	if maxBackups <= 0 {
		maxBackups = 30
	}

	return &config.DailyLogConfig{
		BaseDir:    storageService.GetLogsPath(),
		MaxSize:    int64(maxFileSize) * 1024 * 1024, // 转换为字节
		MaxBackups: maxBackups,                       // 从配置文件读取备份数量
		MaxAge:     retentionDays,                    // 从配置文件读取保留天数
		LocalTime:  true,                             // 使用本地时间
	}
}

// RotatingFileWriter 可轮转的文件写入器
type RotatingFileWriter struct {
	config       *config.DailyLogConfig
	level        string
	file         *os.File
	size         int64
	mu           sync.Mutex
	currentDate  string // 当前文件所属的日期
	failCount    int    // 连续失败计数
	lastFailTime time.Time
}

// NewRotatingFileWriter 创建新的可轮转文件写入器
func NewRotatingFileWriter(level string, config *config.DailyLogConfig) *RotatingFileWriter {
	return &RotatingFileWriter{
		config: config,
		level:  level,
	}
}

// Write 实现 io.Writer 接口
func (w *RotatingFileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 如果连续失败太多次，跳过复杂的轮转逻辑，直接尝试写入
	// 避免在文件系统异常时无限循环
	if w.failCount > 5 && time.Since(w.lastFailTime) < 10*time.Second {
		// 降级模式：尝试直接写入，如果失败就丢弃日志
		if w.file != nil {
			n, err = w.file.Write(p)
			if err == nil {
				w.failCount = 0 // 成功后重置失败计数
				w.size += int64(n)
				return n, nil
			}
		}
		// 降级模式下也失败，丢弃日志但不返回错误，避免阻塞应用
		return len(p), nil
	}

	now := time.Now()
	if !w.config.LocalTime {
		now = now.UTC()
	}
	todayStr := now.Format("2006-01-02")

	// 如果文件未打开，先打开
	if w.file == nil {
		if err := w.openNewFile(); err != nil {
			w.failCount++
			w.lastFailTime = now
			// 返回成功但实际丢弃日志，避免阻塞应用
			return len(p), nil
		}
	}

	// 检查日期是否变化，如果变化则切换到新日期的目录
	if w.currentDate != todayStr {
		// 日期变化，切换到新日期目录
		if err := w.rotate(); err != nil {
			w.failCount++
			w.lastFailTime = now
			// 轮转失败，尝试继续使用当前文件
			// 不返回错误，避免阻塞应用
		}
	}

	// 检查是否需要按大小轮转
	if w.size+int64(len(p)) > w.config.MaxSize {
		if err := w.rotate(); err != nil {
			// 轮转失败，继续使用当前文件，只是可能超出大小限制
			// 不返回错误，避免阻塞应用
		}
	}

	// 写入数据
	n, err = w.file.Write(p)
	if err != nil {
		w.failCount++
		w.lastFailTime = now
		// 写入失败，关闭文件并标记为需要重新打开
		_ = w.file.Close()
		w.file = nil
		w.size = 0
		// 返回成功但实际丢弃日志，避免阻塞应用
		return len(p), nil
	}

	// 写入成功，重置失败计数
	w.failCount = 0
	w.size += int64(n)
	return n, nil
}

// openNewFile 打开新的日志文件
func (w *RotatingFileWriter) openNewFile() error {
	// 确保目录存在
	if err := os.MkdirAll(w.config.BaseDir, 0755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 获取当前日志文件路径
	filename := w.getCurrentLogFilename()

	// 打开文件
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	// 获取当前文件大小
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("获取文件信息失败: %w", err)
	}

	w.file = file
	w.size = info.Size()

	// 更新当前日期
	now := time.Now()
	if !w.config.LocalTime {
		now = now.UTC()
	}
	w.currentDate = now.Format("2006-01-02")

	return nil
}

// getCurrentLogFilename 获取当前日志文件名
// 如果当前日志文件未达到MaxSize，返回现有文件名
// 否则返回带时间戳的新文件名
func (w *RotatingFileWriter) getCurrentLogFilename() string {
	now := time.Now()
	if !w.config.LocalTime {
		now = now.UTC()
	}

	// 创建按日期分组的目录结构：storage/logs/2025-01-07/
	dateStr := now.Format("2006-01-02")
	dateDir := filepath.Join(w.config.BaseDir, dateStr)

	// 确保日期目录存在
	if err := os.MkdirAll(dateDir, 0755); err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Error("创建日期日志目录失败",
				zap.String("dir", dateDir),
				zap.Error(err))
		}
		// 如果创建失败，回退到基础目录
		return filepath.Join(w.config.BaseDir, fmt.Sprintf("%s.log", w.level))
	}

	// 主日志文件名
	baseLogFile := filepath.Join(dateDir, fmt.Sprintf("%s.log", w.level))

	// 检查主日志文件是否存在以及大小
	if info, err := os.Stat(baseLogFile); err == nil {
		// 文件存在，检查大小
		if info.Size() < w.config.MaxSize {
			// 文件未达到最大大小，继续使用当前文件
			return baseLogFile
		}
		// 文件已达到最大大小，需要生成新文件名
		// 继续往下执行，生成带时间戳的新文件名
	} else if os.IsNotExist(err) {
		// 文件不存在，使用基础文件名
		return baseLogFile
	}

	// 文件已达到最大大小或出现其他错误，生成带时间戳的新文件名
	// 格式：level-20060102-150405.log
	timeStr := now.Format("20060102-150405")
	return filepath.Join(dateDir, fmt.Sprintf("%s-%s.log", w.level, timeStr))
}

// rotate 轮转日志文件
func (w *RotatingFileWriter) rotate() error {
	// 如果需要轮转，先重命名当前文件
	if w.file != nil {
		currentPath := w.file.Name()

		// 关闭当前文件（忽略错误，避免日志调用导致递归死锁）
		_ = w.file.Close()
		w.file = nil
		w.size = 0

		// 如果当前文件是主日志文件（不带时间戳），需要重命名
		// 重命名失败不影响继续创建新文件
		if !w.hasTimestamp(currentPath) {
			_ = w.renameWithTimestamp(currentPath)
		}
	}

	// 清理旧文件（在后台异步执行，避免阻塞日志写入）
	go func() {
		_ = w.cleanup()
	}()

	// 打开新文件
	return w.openNewFile()
}

// hasTimestamp 检查文件名是否包含时间戳
func (w *RotatingFileWriter) hasTimestamp(filePath string) bool {
	filename := filepath.Base(filePath)
	// 匹配格式: level-20060102-150405.log
	matched, _ := regexp.MatchString(`\w+-\d{8}-\d{6}\.log`, filename)
	return matched
}

// renameWithTimestamp 将文件重命名为带时间戳的格式
func (w *RotatingFileWriter) renameWithTimestamp(oldPath string) error {
	// 获取文件的修改时间
	info, err := os.Stat(oldPath)
	if err != nil {
		return err
	}

	modTime := info.ModTime()
	if !w.config.LocalTime {
		modTime = modTime.UTC()
	}

	// 生成新文件名: level-20060102-150405.log
	timeStr := modTime.Format("20060102-150405")
	dirPath := filepath.Dir(oldPath)
	newPath := filepath.Join(dirPath, fmt.Sprintf("%s-%s.log", w.level, timeStr))

	// 如果新文件名已存在，添加递增数字
	if _, err := os.Stat(newPath); err == nil {
		found := false
		for i := 1; i < 1000; i++ {
			newPath = filepath.Join(dirPath, fmt.Sprintf("%s-%s-%d.log", w.level, timeStr, i))
			if _, err := os.Stat(newPath); os.IsNotExist(err) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("无法找到可用的文件名，已尝试1000次")
		}
	}

	// 重命名文件
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("重命名文件失败 %s -> %s: %w", oldPath, newPath, err)
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Debug("日志文件已重命名",
			zap.String("from", oldPath),
			zap.String("to", newPath))
	}

	return nil
}

// cleanup 清理旧的日志文件
func (w *RotatingFileWriter) cleanup() error {
	// 获取所有日期目录
	entries, err := os.ReadDir(w.config.BaseDir)
	if err != nil {
		return err
	}

	var dateDirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			// 检查是否是日期格式的目录名
			if matched, _ := filepath.Match("????-??-??", entry.Name()); matched {
				dateDirs = append(dateDirs, entry.Name())
			}
		}
	}

	// 按日期排序（最新的在前）
	sort.Slice(dateDirs, func(i, j int) bool {
		return dateDirs[i] > dateDirs[j]
	})

	// 删除超过保留数量的目录
	deletedByCount := 0
	if len(dateDirs) > w.config.MaxBackups {
		for _, dateDir := range dateDirs[w.config.MaxBackups:] {
			dirPath := filepath.Join(w.config.BaseDir, dateDir)
			os.RemoveAll(dirPath)
			deletedByCount++
		}
	}

	// 删除超过保留时间的目录
	cutoff := time.Now().AddDate(0, 0, -w.config.MaxAge)
	cutoffDateStr := cutoff.Format("2006-01-02")

	deletedByAge := 0
	for _, dateDir := range dateDirs {
		if dateDir < cutoffDateStr {
			dirPath := filepath.Join(w.config.BaseDir, dateDir)
			os.RemoveAll(dirPath)
			deletedByAge++
		}
	}

	// 删除结果不记录日志，避免递归调用
	// 可以通过检查文件系统来验证清理结果
	_ = deletedByCount
	_ = deletedByAge

	return nil
} // Close 关闭文件写入器
func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		w.size = 0
		return err
	}
	return nil
}

// CreateDailyLogWriter 创建按日期分存储的日志写入器
func (s *LogRotationService) CreateDailyLogWriter(level string, config *config.DailyLogConfig) zapcore.WriteSyncer {
	rotatingWriter := NewRotatingFileWriter(level, config)

	// 注册writer到管理器
	s.mu.Lock()
	s.writers[level] = rotatingWriter
	s.mu.Unlock()

	// 如果需要同时输出到控制台
	if global.APP_CONFIG.Zap.LogInConsole {
		return zapcore.NewMultiWriteSyncer(
			zapcore.AddSync(os.Stdout),
			zapcore.AddSync(rotatingWriter),
		)
	}

	return zapcore.AddSync(rotatingWriter)
}

// CreateDailyLoggerCore 创建支持日志轮转的logger core
func (s *LogRotationService) CreateDailyLoggerCore(level zapcore.Level, config *config.DailyLogConfig) zapcore.Core {
	writer := s.CreateDailyLogWriter(level.String(), config)
	encoder := s.getEncoder()
	return zapcore.NewCore(encoder, writer, level)
}

// CreateDailyLogger 创建支持按日期分存储的logger
func (s *LogRotationService) CreateDailyLogger() *zap.Logger {
	dailyLogConfig := GetDefaultDailyLogConfig()

	// 创建不同级别的日志核心
	cores := make([]zapcore.Core, 0, 7)
	levels := global.APP_CONFIG.Zap.Levels()

	for _, level := range levels {
		core := s.CreateDailyLoggerCore(level, dailyLogConfig)
		cores = append(cores, core)
	}

	logger := zap.New(zapcore.NewTee(cores...))

	if global.APP_CONFIG.Zap.ShowLine {
		logger = logger.WithOptions(zap.AddCaller())
	}

	return logger
}

// getEncoder 获取日志编码器
func (s *LogRotationService) getEncoder() zapcore.Encoder {
	encoderConfig := zapcore.EncoderConfig{
		MessageKey:     "message",
		LevelKey:       "level",
		TimeKey:        "time",
		NameKey:        "logger",
		CallerKey:      "caller",
		StacktraceKey:  global.APP_CONFIG.Zap.StacktraceKey,
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    global.APP_CONFIG.Zap.LevelEncoder(),
		EncodeTime:     s.customTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	if global.APP_CONFIG.Zap.Format == "json" {
		return zapcore.NewJSONEncoder(encoderConfig)
	}
	return zapcore.NewConsoleEncoder(encoderConfig)
}

// customTimeEncoder 自定义时间编码器，包含日期信息
func (s *LogRotationService) customTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format(global.APP_CONFIG.Zap.Prefix + "2006/01/02 - 15:04:05.000"))
}

// CleanupOldLogs 清理旧日志文件
func (s *LogRotationService) CleanupOldLogs() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	logConfig := GetDefaultDailyLogConfig()
	cutoffTime := time.Now().AddDate(0, 0, -logConfig.MaxAge)
	cutoffDateStr := cutoffTime.Format("2006-01-02")

	// 获取所有日期目录
	entries, err := os.ReadDir(logConfig.BaseDir)
	if err != nil {
		global.APP_LOG.Error("读取日志目录失败", zap.Error(err))
		return err
	}

	// 统计清理结果
	cleanedCount := 0
	errorCount := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// 检查是否是日期格式的目录名
		dirName := entry.Name()
		if matched, _ := filepath.Match("????-??-??", dirName); !matched {
			continue
		}

		// 检查目录日期是否过期
		if dirName < cutoffDateStr {
			dirPath := filepath.Join(logConfig.BaseDir, dirName)

			if err := os.RemoveAll(dirPath); err != nil {
				errorCount++
				// 只记录第一个错误的详细信息
				if errorCount == 1 {
					global.APP_LOG.Warn("删除过期日志目录失败",
						zap.String("dir", dirPath),
						zap.Error(err))
				}
			} else {
				cleanedCount++
			}
		}
	}

	// 汇总记录清理结果
	if cleanedCount > 0 || errorCount > 0 {
		global.APP_LOG.Info("清理过期日志目录完成",
			zap.Int("cleaned", cleanedCount),
			zap.Int("errors", errorCount),
			zap.String("cutoffDate", cutoffDateStr))
	}

	return nil
} // GetLogFiles 获取日志文件列表（按日期分文件夹结构）
func (s *LogRotationService) GetLogFiles() ([]LogFileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dailyLogConfig := GetDefaultDailyLogConfig()

	var logFiles []LogFileInfo

	// 遍历日志基础目录下的所有子目录
	baseDirEntries, err := os.ReadDir(dailyLogConfig.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return logFiles, nil // 目录不存在，返回空列表
		}
		return nil, fmt.Errorf("读取日志目录失败: %w", err)
	}

	for _, entry := range baseDirEntries {
		if !entry.IsDir() {
			continue
		}

		// 检查目录名是否为日期格式 (YYYY-MM-DD)
		dirName := entry.Name()
		if matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}$`, dirName); !matched {
			continue
		}

		// 遍历日期目录下的所有日志文件
		dateDirPath := filepath.Join(dailyLogConfig.BaseDir, dirName)
		logFileEntries, err := os.ReadDir(dateDirPath)
		if err != nil {
			global.APP_LOG.Warn("读取日期目录失败",
				zap.String("dir", dateDirPath),
				zap.Error(err))
			continue
		}

		for _, logEntry := range logFileEntries {
			if logEntry.IsDir() {
				continue
			}

			// 只处理日志文件
			fileName := logEntry.Name()
			ext := filepath.Ext(fileName)
			if ext != ".log" && ext != ".gz" {
				continue
			}

			// 获取文件详细信息
			fullPath := filepath.Join(dateDirPath, fileName)
			fileInfo, err := os.Stat(fullPath)
			if err != nil {
				global.APP_LOG.Warn("获取日志文件信息失败",
					zap.String("file", fullPath),
					zap.Error(err))
				continue
			}

			// 构建相对路径（包含日期目录）
			relPath := filepath.Join(dirName, fileName)

			logFile := LogFileInfo{
				Name:    fileName,
				Path:    relPath,
				Size:    fileInfo.Size(),
				ModTime: fileInfo.ModTime(),
				Date:    dirName, // 日期信息
			}

			logFiles = append(logFiles, logFile)
		}
	}

	// 按修改时间倒序排列（最新的在前）
	sort.Slice(logFiles, func(i, j int) bool {
		return logFiles[i].ModTime.After(logFiles[j].ModTime)
	})

	return logFiles, nil
}

// LogFileInfo 日志文件信息
type LogFileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Date    string    `json:"date"` // 日期字段，格式为 YYYY-MM-DD
}

// ReadLogFile 读取日志文件内容
func (s *LogRotationService) ReadLogFile(filename string, lines int) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logConfig := GetDefaultDailyLogConfig()
	filePath := filepath.Join(logConfig.BaseDir, filename)

	// 安全检查：确保文件在日志目录内
	absLogDir, err := filepath.Abs(logConfig.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("获取日志目录绝对路径失败: %w", err)
	}

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("获取文件绝对路径失败: %w", err)
	}

	relPath, err := filepath.Rel(absLogDir, absFilePath)
	if err != nil || strings.Contains(relPath, "..") {
		return nil, fmt.Errorf("无效的文件路径")
	}

	// 检查文件是否存在
	if _, err := os.Stat(absFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("日志文件不存在: %s", filename)
	}

	var reader io.Reader
	file, err := os.Open(absFilePath)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}
	defer file.Close()

	reader = file

	// 逐行读取文件
	var allLines []string
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取日志文件失败: %w", err)
	}

	// 返回最后N行
	if lines > 0 && len(allLines) > lines {
		return allLines[len(allLines)-lines:], nil
	}

	return allLines, nil
}
