package log

import (
	"bufio"
	"compress/gzip"
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

	for level, writer := range s.writers {
		if err := writer.Close(); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Error("关闭日志文件失败",
					zap.String("level", level),
					zap.Error(err))
			}
		}
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

	// 从配置文件读取是否压缩日志
	compressLogs := global.APP_CONFIG.Zap.CompressLogs

	return &config.DailyLogConfig{
		BaseDir:    storageService.GetLogsPath(),
		MaxSize:    int64(maxFileSize) * 1024 * 1024, // 转换为字节
		MaxBackups: maxBackups,                       // 从配置文件读取备份数量
		MaxAge:     retentionDays,                    // 从配置文件读取保留天数
		Compress:   compressLogs,                     // 从配置文件读取压缩设置
		LocalTime:  true,                             // 使用本地时间
	}
}

// RotatingFileWriter 可轮转的文件写入器
type RotatingFileWriter struct {
	config      *config.DailyLogConfig
	level       string
	file        *os.File
	size        int64
	mu          sync.Mutex
	currentDate string // 当前文件所属的日期
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

	now := time.Now()
	if !w.config.LocalTime {
		now = now.UTC()
	}
	todayStr := now.Format("2006-01-02")

	// 如果文件未打开，先打开
	if w.file == nil {
		if err := w.openNewFile(); err != nil {
			return 0, err
		}
		// 初始化时设置当前日期
		w.currentDate = todayStr
	}

	// 检查日期是否变化，如果变化则切换到新日期的目录
	if w.currentDate != todayStr {
		// 日期变化，切换到新日期目录
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	// 检查是否需要按大小轮转
	if w.size+int64(len(p)) > w.config.MaxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	// 写入数据
	n, err = w.file.Write(p)
	if err != nil {
		return n, err
	}

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

		// 关闭当前文件
		if err := w.file.Close(); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("关闭日志文件失败", zap.String("file", currentPath), zap.Error(err))
			}
		}
		w.file = nil
		w.size = 0

		// 如果当前文件是主日志文件（不带时间戳），需要重命名
		if !w.hasTimestamp(currentPath) {
			if err := w.renameWithTimestamp(currentPath); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("重命名日志文件失败",
						zap.String("file", currentPath),
						zap.Error(err))
				}
			}
		}
	}

	// 清理旧文件
	if err := w.cleanup(); err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("清理旧日志文件失败", zap.Error(err))
		}
	}

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

	// 汇总记录删除结果（仅在调试模式记录）
	if (deletedByCount > 0 || deletedByAge > 0) && global.APP_LOG != nil {
		global.APP_LOG.Debug("删除过期日志目录",
			zap.String("level", w.level),
			zap.Int("deletedByCount", deletedByCount),
			zap.Int("deletedByAge", deletedByAge),
			zap.Int("remaining", len(dateDirs)-deletedByCount-deletedByAge))
	}

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
				IsGzip:  ext == ".gz",
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
	IsGzip  bool      `json:"is_gzip"`
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

	// 如果是gzip压缩文件，需要解压读取
	if filepath.Ext(filename) == ".gz" {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return nil, fmt.Errorf("打开压缩日志文件失败: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	} else {
		reader = file
	}

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

// CompressOldLogs 压缩旧的日志文件
func (s *LogRotationService) CompressOldLogs() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	defaultDailyLogConfig := GetDefaultDailyLogConfig()

	if !defaultDailyLogConfig.Compress {
		return nil // 如果未启用压缩，直接返回
	}

	// 查找需要压缩的日志文件（昨天之前的文件或当天已轮转的文件）
	yesterday := time.Now().AddDate(0, 0, -1)
	compressedCount := 0
	errorCount := 0

	err := filepath.Walk(defaultDailyLogConfig.BaseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录和已压缩的文件
		if info.IsDir() || filepath.Ext(path) != ".log" {
			return nil
		}

		// 跳过当前正在使用的主日志文件（文件名格式为 level.log，不含时间戳）
		filename := filepath.Base(path)
		isMainLogFile := false
		if matched, _ := regexp.MatchString(`^\w+\.log$`, filename); matched {
			isMainLogFile = true
			// 这是主日志文件，只压缩昨天之前的
			if !info.ModTime().Before(yesterday) {
				return nil // 不压缩今天的主日志文件
			}
		}

		// 判断是否需要压缩
		shouldCompress := false
		if info.ModTime().Before(yesterday) {
			// 昨天之前的文件，一定压缩
			shouldCompress = true
		} else if !isMainLogFile {
			// 今天的文件，检查是否是已轮转的带时间戳文件
			if matched, _ := regexp.MatchString(`\w+-\d{8}-\d{6}.*\.log$`, filename); matched {
				// 这是带时间戳的已轮转文件
				// 为了安全，确保文件已经稳定（修改时间超过2分钟）
				if time.Since(info.ModTime()) > 2*time.Minute {
					shouldCompress = true
				}
			}
		}

		if shouldCompress {
			if err := s.compressFile(path); err != nil {
				errorCount++
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("压缩日志文件失败",
						zap.String("file", path),
						zap.Error(err))
				}
			} else {
				compressedCount++
			}
		}

		return nil
	})

	// 记录压缩结果
	if compressedCount > 0 || errorCount > 0 {
		if global.APP_LOG != nil {
			global.APP_LOG.Info("日志压缩任务完成",
				zap.Int("compressed", compressedCount),
				zap.Int("errors", errorCount))
		}
	}

	return err
}

// compressFile 压缩单个文件
func (s *LogRotationService) compressFile(filePath string) error {
	// 打开原文件
	src, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer src.Close()

	// 创建压缩文件
	dstPath := filePath + ".gz"
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("创建压缩文件失败: %w", err)
	}
	defer dst.Close()

	// 创建gzip写入器
	gzWriter := gzip.NewWriter(dst)
	defer gzWriter.Close()

	// 复制数据
	_, err = io.Copy(gzWriter, src)
	if err != nil {
		os.Remove(dstPath) // 清理失败的压缩文件
		return fmt.Errorf("压缩文件失败: %w", err)
	}

	// 关闭gzip写入器以确保数据被刷新
	if err := gzWriter.Close(); err != nil {
		os.Remove(dstPath)
		return fmt.Errorf("关闭压缩文件失败: %w", err)
	}

	// 删除原文件
	if err := os.Remove(filePath); err != nil {
		global.APP_LOG.Warn("删除原日志文件失败",
			zap.String("file", filePath),
			zap.Error(err))
	} else {
		// 压缩完成日志的级别
		global.APP_LOG.Debug("日志文件压缩完成",
			zap.String("source", filePath),
			zap.String("dest", dstPath))
	}

	return nil
}
