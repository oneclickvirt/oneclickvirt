package lxd

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// waitForVMNetworkReady 等待虚拟机网络就绪
func (l *LXDProvider) waitForVMNetworkReady(instanceName string) error {
	global.APP_LOG.Info("等待虚拟机网络就绪", zap.String("instanceName", instanceName))

	maxRetries := 8 // 增加重试次数
	delay := 15     // 虚拟机需要更长的启动时间

	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Info("等待虚拟机启动并获取IP地址",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries),
			zap.Int("delay", delay))

		time.Sleep(time.Duration(delay) * time.Second)

		// 检查虚拟机状态
		statusCmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := l.sshClient.Execute(statusCmd)
		if err != nil {
			global.APP_LOG.Warn("检查虚拟机状态失败",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		status := strings.TrimSpace(output)
		if status != "RUNNING" {
			global.APP_LOG.Info("虚拟机尚未运行",
				zap.String("instanceName", instanceName),
				zap.String("status", status),
				zap.Int("attempt", attempt))
			continue
		}

		// 检查是否已获取到IP地址
		if _, err := l.getInstanceIP(instanceName); err == nil {
			global.APP_LOG.Info("虚拟机网络已就绪",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt))
			return nil
		}

		// 逐渐增加等待时间
		if attempt < maxRetries {
			delay = l.min(delay+5, 25)
		}
	}

	return fmt.Errorf("虚拟机网络就绪超时，已等待 %d 次", maxRetries)
}

// waitForContainerNetworkReady 等待容器网络就绪
func (l *LXDProvider) waitForContainerNetworkReady(instanceName string) error {
	global.APP_LOG.Info("等待容器网络就绪", zap.String("instanceName", instanceName))

	maxRetries := 10 // 容器启动较快
	delay := 5       // 容器启动时间较短

	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Info("等待容器启动并获取IP地址",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries),
			zap.Int("delay", delay))

		time.Sleep(time.Duration(delay) * time.Second)

		// 检查容器状态
		statusCmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := l.sshClient.Execute(statusCmd)
		if err != nil {
			global.APP_LOG.Warn("检查容器状态失败",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		status := strings.TrimSpace(output)
		if status != "RUNNING" {
			global.APP_LOG.Info("容器尚未运行",
				zap.String("instanceName", instanceName),
				zap.String("status", status),
				zap.Int("attempt", attempt))
			continue
		}

		// 检查是否已获取到IP地址
		if _, err := l.getInstanceIP(instanceName); err == nil {
			global.APP_LOG.Info("容器网络已就绪",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt))
			return nil
		}

		// 逐渐增加等待时间
		if attempt < maxRetries {
			delay = l.min(delay+2, 15) // 最大等待15秒
		}
	}

	return fmt.Errorf("容器网络就绪超时，已等待 %d 次", maxRetries)
}
