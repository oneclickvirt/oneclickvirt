package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"oneclickvirt/global"
	"oneclickvirt/model/system"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// StorageService 存储服务，负责管理所有存储目录
type StorageService struct{}

// GetDefaultStorageConfig 获取默认存储配置
func GetDefaultStorageConfig() *system.StorageConfig {
	return &system.StorageConfig{
		BaseDir: system.DefaultStorageDir,
		Dirs: []string{
			system.LogsDir,
			system.UploadsDir,
			system.ExportsDir,
			system.ConfigsDir,
			system.CertsDir,
			system.CacheDir,
			system.TempDir,
			system.AvatarsDir,
		},
	}
}

// InitializeStorage 初始化存储目录结构
func (s *StorageService) InitializeStorage() error {
	config := GetDefaultStorageConfig()

	global.APP_LOG.Info("开始初始化存储目录结构",
		zap.String("baseDir", config.BaseDir),
		zap.Int("dirsCount", len(config.Dirs)))

	// 创建基础目录（使用全局工具函数）
	if err := utils.EnsureDir(config.BaseDir); err != nil {
		return fmt.Errorf("创建基础存储目录失败: %w", err)
	}

	// 创建所有子目录 - 不再为每个目录单独记录日志
	failedDirs := make([]string, 0)
	for _, dir := range config.Dirs {
		fullPath := filepath.Join(config.BaseDir, dir)
		if err := utils.EnsureDir(fullPath); err != nil {
			failedDirs = append(failedDirs, fullPath)
			global.APP_LOG.Warn("创建存储子目录失败",
				zap.String("dir", fullPath),
				zap.Error(err))
		}
	}

	// 只记录一次总的结果日志
	if len(failedDirs) > 0 {
		global.APP_LOG.Warn("部分存储目录创建失败",
			zap.String("baseDir", config.BaseDir),
			zap.Int("failed", len(failedDirs)),
			zap.Int("total", len(config.Dirs)))
	} else {
		global.APP_LOG.Info("存储目录结构初始化完成",
			zap.String("baseDir", config.BaseDir),
			zap.Int("dirs", len(config.Dirs)))
	}

	return nil
}

// GetStoragePath 获取存储路径
func (s *StorageService) GetStoragePath(subPath string) string {
	return filepath.Join(system.DefaultStorageDir, subPath)
}

// GetLogsPath 获取日志存储路径
func (s *StorageService) GetLogsPath() string {
	return s.GetStoragePath(system.LogsDir)
}

// GetUploadsPath 获取上传文件存储路径
func (s *StorageService) GetUploadsPath() string {
	return s.GetStoragePath(system.UploadsDir)
}

// GetExportsPath 获取导出文件存储路径
func (s *StorageService) GetExportsPath() string {
	return s.GetStoragePath(system.ExportsDir)
}

// GetConfigsPath 获取配置文件存储路径
func (s *StorageService) GetConfigsPath() string {
	return s.GetStoragePath(system.ConfigsDir)
}

// GetCertsPath 获取证书文件存储路径
func (s *StorageService) GetCertsPath() string {
	return s.GetStoragePath(system.CertsDir)
}

// GetCachePath 获取缓存文件存储路径
func (s *StorageService) GetCachePath() string {
	return s.GetStoragePath(system.CacheDir)
}

// GetTempPath 获取临时文件存储路径
func (s *StorageService) GetTempPath() string {
	return s.GetStoragePath(system.TempDir)
}

// GetAvatarsPath 获取头像文件存储路径
func (s *StorageService) GetAvatarsPath() string {
	return s.GetStoragePath(system.AvatarsDir)
}

// CleanupTempFiles 清理临时文件
func (s *StorageService) CleanupTempFiles() error {
	tempPath := s.GetTempPath()

	// 检查临时目录是否存在
	if _, err := os.Stat(tempPath); os.IsNotExist(err) {
		global.APP_LOG.Debug("临时目录不存在，跳过清理", zap.String("path", tempPath))
		return nil
	}

	// 删除临时目录中的所有文件
	entries, err := os.ReadDir(tempPath)
	if err != nil {
		return fmt.Errorf("读取临时目录失败: %w", err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(tempPath, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			global.APP_LOG.Warn("删除临时文件失败",
				zap.String("file", entryPath),
				zap.Error(err))
		}
	}

	global.APP_LOG.Debug("临时文件清理完成", zap.String("path", tempPath))
	return nil
}

// GetStorageInfo 获取存储信息
func (s *StorageService) GetStorageInfo() map[string]interface{} {
	config := GetDefaultStorageConfig()
	info := make(map[string]interface{})

	info["baseDir"] = config.BaseDir
	info["directories"] = make(map[string]interface{})

	for _, dir := range config.Dirs {
		fullPath := filepath.Join(config.BaseDir, dir)
		dirInfo := make(map[string]interface{})

		if stat, err := os.Stat(fullPath); err == nil {
			dirInfo["exists"] = true
			dirInfo["isDir"] = stat.IsDir()
			dirInfo["mode"] = stat.Mode().String()
			dirInfo["modTime"] = stat.ModTime()
		} else {
			dirInfo["exists"] = false
			dirInfo["error"] = err.Error()
		}

		info["directories"].(map[string]interface{})[dir] = dirInfo
	}

	return info
}

// GetStorageService 获取存储服务实例
func GetStorageService() *StorageService {
	return &StorageService{}
}
