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

// Zap 构建并返回应用级 zap.Logger。
//
// 流程：
//  1. 确保日志目录 ./storage/logs 存在；
//  2. 根据配置将每个日志级别分别写入独立文件，Debug/Info 限流采样降噪；
//  3. 若配置开启 Show-Line，日志自动携带调用者文件/行号。
//
// 注意：采样器清理协程将延迟到 InitializeSystem 中启动，
// 因为该函数执行时 global.APP_SHUTDOWN_CONTEXT 还未就绪。
func Zap() (logger *zap.Logger) {
	// 确保日志目录存在——此时日志系统尚未完全就绪，使用 stderr 输出更合适
	logDir := "./storage/logs"
	if err := utils.EnsureDir(logDir); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] 日志目录初始化失败 %q: %v，将仅使用控制台输出\n", logDir, err)
	}

	cores := GetZapCores()
	logger = zap.New(zapcore.NewTee(cores...))

	if global.GetAppConfig().Zap.ShowLine {
		logger = logger.WithOptions(zap.AddCaller())
	}

	// 采样器清理协程在 InitializeSystem 中通过 StartSamplerCleanup 启动
	return logger
}

// StartSamplerCleanup 启动采样器定期清理得协程。
// 必须在 global.APP_SHUTDOWN_CONTEXT 就绪后（InitializeSystem 中）调用。
func StartSamplerCleanup(ctx context.Context) {
	go startSamplerCleanup(ctx)
}

// startSamplerCleanup 櫳第采样器现有缓存，每 30 分钟清理一次长期未使用的采样动态表进入。
func startSamplerCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if global.APP_LOG != nil {
				global.APP_LOG.Info("采样器清理协程已停止")
			}
			return
		case <-ticker.C:
			cleanupAllSamplers()
		}
	}
}

// cleanupAllSamplers 代理调用 sampling_core.go 中的全局清理入口。
func cleanupAllSamplers() {
	CleanupAllSamplingCores()
}

// GetZapCores 根据配置中的起始级别，生成覆盖该级别岩上的所有级别的 Core 列表。
// Debug 和 Info 级别自动包裹采样层，降低高频重复日志的写入压力。
func GetZapCores() []zapcore.Core {
	cores := make([]zapcore.Core, 0, 7)
	zapCfg := global.GetAppConfig().Zap
	levels := zapCfg.Levels()
	for _, level := range levels {
		core := GetZapCore(level)
		// Debug/Info 级别使用采样核心，高频重复消息按时间窗口限流
		if level <= zapcore.InfoLevel {
			core = NewSamplingCore(core)
		}
		cores = append(cores, core)
	}
	return cores
}

// GetZapCore 为指定级别创建一个独立的 zapcore.Core，
// 使用分所写入对应级别的日志文件中。
func GetZapCore(level zapcore.Level) (core zapcore.Core) {
	writer := GetWriteSyncer(level.String()) // 按日滚动切片写入
	return zapcore.NewCore(GetEncoder(), writer, level)
}

// GetEncoder 根据配置返回 JSON 或 console 格式的编码器，
// 外层通过 TruncateEncoder 封装，不同内容过长时自动截断。
func GetEncoder() zapcore.Encoder {
	var enc zapcore.Encoder
	if global.GetAppConfig().Zap.Format == "json" {
		enc = zapcore.NewJSONEncoder(GetEncoderConfig())
	} else {
		enc = zapcore.NewConsoleEncoder(GetEncoderConfig())
	}
	return NewTruncateEncoder(enc)
}

// GetEncoderConfig 返回稳定的 zapcore.EncoderConfig。
// 时间格式由 CustomTimeEncoder 处理，caller 使用短路径减少轮序长度。
func GetEncoderConfig() (config zapcore.EncoderConfig) {
	zapCfg := global.GetAppConfig().Zap
	config = zapcore.EncoderConfig{
		MessageKey:     "message",
		LevelKey:       "level",
		TimeKey:        "time",
		NameKey:        "logger",
		CallerKey:      "caller",
		StacktraceKey:  zapCfg.StacktraceKey,
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapCfg.LevelEncoder(),
		EncodeTime:     CustomTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		// 使用短路径编码器减少日志长度
		EncodeCaller: zapcore.ShortCallerEncoder,
	}
	switch {
	case global.GetAppConfig().Zap.EncodeLevel == "LowercaseLevelEncoder": // 小写编码器(默认)
		config.EncodeLevel = zapcore.LowercaseLevelEncoder
	case global.GetAppConfig().Zap.EncodeLevel == "LowercaseColorLevelEncoder": // 小写编码器带颜色
		config.EncodeLevel = zapcore.LowercaseColorLevelEncoder
	case global.GetAppConfig().Zap.EncodeLevel == "CapitalLevelEncoder": // 大写编码器
		config.EncodeLevel = zapcore.CapitalLevelEncoder
	case global.GetAppConfig().Zap.EncodeLevel == "CapitalColorLevelEncoder": // 大写编码器带颜色
		config.EncodeLevel = zapcore.CapitalColorLevelEncoder
	default:
		config.EncodeLevel = zapcore.LowercaseLevelEncoder
	}
	return config
}

// GetWriteSyncer 返回指定级别的输出展汇器。
// 若配置了 LogInConsole，日志将同时写入文件和标准输出；否则仅写入文件。
func GetWriteSyncer(level string) zapcore.WriteSyncer {
	// 获取日志配置
	config := log.GetDefaultDailyLogConfig()

	// 直接创建日志写入器
	cutter := log.NewRotatingFileWriter(level, config)

	// 如果需要同时输出到控制台
	if global.GetAppConfig().Zap.LogInConsole {
		return zapcore.NewMultiWriteSyncer(
			zapcore.AddSync(os.Stdout),
			zapcore.AddSync(cutter),
		)
	}

	return zapcore.AddSync(cutter)
}

// CustomTimeEncoder 以 "[prefix]YYYY/MM/DD - HH:mm:ss.mmm" 格式写入时间字段。
func CustomTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format(global.GetAppConfig().Zap.Prefix + "2006/01/02 - 15:04:05.000"))
}
