package utils

import (
	"fmt"
	"os"
)

// PathExists 检查路径是否存在
func PathExists(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err == nil {
		if fi.IsDir() {
			return true, nil
		}
		return false, fmt.Errorf("存在同名文件")
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// EnsureDir 确保目录存在，如果不存在则创建（全局统一函数）
func EnsureDir(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", path, err)
		}
	} else if err != nil {
		return fmt.Errorf("检查目录 %s 失败: %w", path, err)
	}
	return nil
}

// EnsureDirs 确保多个目录存在（全局统一函数）
func EnsureDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := EnsureDir(dir); err != nil {
			return err
		}
	}
	return nil
}
