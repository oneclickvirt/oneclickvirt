package core

import (
	"context"
	"fmt"
	"oneclickvirt/service/log"
	"os"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Zap 获取 zap.Logger
func Zap() (logger *zap.Logger) {
	// 确保日志目录存在 - 使用./storage/logs目录
	logDir := "./storage/logs"
	if err := utils.EnsureDir(logDir); err != nil {
		// 在日志系统未完全初始化时，使用标准输出
		fmt.Printf("[SYSTEM] 日志目录创建失败 %v: %v，将使用控制台输出\n", logDir, err)
	}

	cores := GetZapCores()
	logger = zap.New(zapcore.NewTee(cores...))

	if global.APP_CONFIG.Zap.ShowLine {
		logger = logger.WithOptions(zap.AddCaller())
	}

	// 采样器清理协程的启动将在 InitializeSystem 中进行
	// 这里不启动，因为 global.APP_SHUTDOWN_CONTEXT 还未初始化

	return logger
}

// StartSamplerCleanup 启动采样器清理任务（导出供外部调用）
func StartSamplerCleanup(ctx context.Context) {
	go startSamplerCleanup(ctx)
}

// startSamplerCleanup 启动采样器清理任务
func startSamplerCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute) // 每30分钟清理一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if global.APP_LOG != nil {
				global.APP_LOG.Info("采样器清理任务已停止")
			}
			return
		case <-ticker.C:
			// 遍历所有采样核心并清理
			cleanupAllSamplers()
		}
	}
}

// cleanupAllSamplers 清理所有采样器
func cleanupAllSamplers() {
	// 需要导入sampling_core.go中的全局变量
	// 这里直接调用清理函数
	CleanupAllSamplingCores()
}

// GetZapCores 根据配置文件的Level获取 []zapcore.Core
func GetZapCores() []zapcore.Core {
	cores := make([]zapcore.Core, 0, 7)
	levels := global.APP_CONFIG.Zap.Levels()
	for _, level := range levels {
		core := GetZapCore(level)
		// 对于Debug和Info级别，使用采样核心来减少日志量
		if level <= zapcore.InfoLevel {
			core = NewSamplingCore(core)
		}
		cores = append(cores, core)
	}
	return cores
}

// GetZapCore 获取Encoder的 zapcore.Core
func GetZapCore(level zapcore.Level) (core zapcore.Core) {
	writer := GetWriteSyncer(level.String()) // 使用file-rotatelogs进行日志分割
	return zapcore.NewCore(GetEncoder(), writer, level)
}

// GetEncoder 获取zapcore.Encoder
func GetEncoder() zapcore.Encoder {
	var enc zapcore.Encoder
	if global.APP_CONFIG.Zap.Format == "json" {
		enc = zapcore.NewJSONEncoder(GetEncoderConfig())
	} else {
		enc = zapcore.NewConsoleEncoder(GetEncoderConfig())
	}

	// 包装为截断编码器
	return NewTruncateEncoder(enc)
}

// GetEncoderConfig 获取zapcore.EncoderConfig
func GetEncoderConfig() (config zapcore.EncoderConfig) {
	config = zapcore.EncoderConfig{
		MessageKey:     "message",
		LevelKey:       "level",
		TimeKey:        "time",
		NameKey:        "logger",
		CallerKey:      "caller",
		StacktraceKey:  global.APP_CONFIG.Zap.StacktraceKey,
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    global.APP_CONFIG.Zap.LevelEncoder(),
		EncodeTime:     CustomTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		// 使用短路径编码器减少日志长度
		EncodeCaller: zapcore.ShortCallerEncoder,
	}
	switch {
	case global.APP_CONFIG.Zap.EncodeLevel == "LowercaseLevelEncoder": // 小写编码器(默认)
		config.EncodeLevel = zapcore.LowercaseLevelEncoder
	case global.APP_CONFIG.Zap.EncodeLevel == "LowercaseColorLevelEncoder": // 小写编码器带颜色
		config.EncodeLevel = zapcore.LowercaseColorLevelEncoder
	case global.APP_CONFIG.Zap.EncodeLevel == "CapitalLevelEncoder": // 大写编码器
		config.EncodeLevel = zapcore.CapitalLevelEncoder
	case global.APP_CONFIG.Zap.EncodeLevel == "CapitalColorLevelEncoder": // 大写编码器带颜色
		config.EncodeLevel = zapcore.CapitalColorLevelEncoder
	default:
		config.EncodeLevel = zapcore.LowercaseLevelEncoder
	}
	return config
}

// GetWriteSyncer 获取zapcore.WriteSyncer
func GetWriteSyncer(level string) zapcore.WriteSyncer {
	// 获取日志配置
	config := log.GetDefaultDailyLogConfig()

	// 直接创建日志写入器
	cutter := log.NewRotatingFileWriter(level, config)

	// 如果需要同时输出到控制台
	if global.APP_CONFIG.Zap.LogInConsole {
		return zapcore.NewMultiWriteSyncer(
			zapcore.AddSync(os.Stdout),
			zapcore.AddSync(cutter),
		)
	}

	return zapcore.AddSync(cutter)
}

// CustomTimeEncoder 自定义日志输出时间格式
func CustomTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format(global.APP_CONFIG.Zap.Prefix + "2006/01/02 - 15:04:05.000"))
}
