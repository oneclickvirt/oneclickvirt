package lxd

import (
	"fmt"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// restartInstanceForNetwork 重启实例以获取网络配置
func (l *LXDProvider) restartInstanceForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启实例获取网络配置", zap.String("instanceName", instanceName))

	// 检查实例类型以决定重启策略
	instanceType, err := l.getInstanceType(instanceName)
	if err != nil {
		global.APP_LOG.Warn("无法检测实例类型，使用默认重启策略",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		instanceType = "container" // 默认按容器处理
	}

	// 根据实例类型选择不同的重启策略
	if instanceType == "virtual-machine" {
		return l.restartVMForNetwork(instanceName)
	} else {
		return l.restartContainerForNetwork(instanceName)
	}
}

// restartVMForNetwork 重启虚拟机以获取网络配置
func (l *LXDProvider) restartVMForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启虚拟机获取网络配置", zap.String("instanceName", instanceName))

	// 尝试优雅重启，给虚拟机足够的超时时间
	restartCmd := fmt.Sprintf("lxc restart %s --timeout=120", instanceName)
	_, err := l.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("优雅重启虚拟机失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 如果优雅重启失败，尝试强制停止后重启
		return l.forceRestartVM(instanceName)
	}

	// 等待虚拟机完全启动并获取IP
	return l.waitForVMNetworkReady(instanceName)
}

// restartContainerForNetwork 重启容器以获取网络配置
func (l *LXDProvider) restartContainerForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启容器获取网络配置", zap.String("instanceName", instanceName))
	restartCmd := fmt.Sprintf("lxc restart %s --timeout=60", instanceName)
	_, err := l.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("容器重启失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 强制重启容器
		return l.forceRestartContainer(instanceName)
	}

	// 等待容器启动并获取IP
	return l.waitForContainerNetworkReady(instanceName)
}

// forceRestartVM 强制重启虚拟机
func (l *LXDProvider) forceRestartVM(instanceName string) error {
	global.APP_LOG.Info("强制重启虚拟机", zap.String("instanceName", instanceName))

	// 强制停止虚拟机
	stopCmd := fmt.Sprintf("lxc stop %s --force --timeout=60", instanceName)
	_, err := l.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止虚拟机失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止虚拟机失败: %w", err)
	}

	// 等待完全停止
	time.Sleep(10 * time.Second)

	// 启动虚拟机
	startCmd := fmt.Sprintf("lxc start %s", instanceName)
	_, err = l.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动虚拟机失败: %w", err)
	}

	// 等待虚拟机网络就绪
	return l.waitForVMNetworkReady(instanceName)
}

// forceRestartContainer 强制重启容器
func (l *LXDProvider) forceRestartContainer(instanceName string) error {
	global.APP_LOG.Info("强制重启容器", zap.String("instanceName", instanceName))

	// 强制停止容器
	stopCmd := fmt.Sprintf("lxc stop %s --force --timeout=30", instanceName)
	_, err := l.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止容器失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止容器失败: %w", err)
	}

	// 短暂等待
	time.Sleep(3 * time.Second)

	// 启动容器
	startCmd := fmt.Sprintf("lxc start %s", instanceName)
	_, err = l.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动容器失败: %w", err)
	}

	// 等待容器网络就绪
	return l.waitForContainerNetworkReady(instanceName)
}
