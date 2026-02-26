package incus

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// restartInstanceForNetwork 重启实例以获取网络配置
func (i *IncusProvider) restartInstanceForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启实例获取网络配置", zap.String("instanceName", instanceName))

	// 检查实例类型以决定重启策略
	instanceType, err := i.getInstanceType(instanceName)
	if err != nil {
		global.APP_LOG.Warn("无法检测实例类型，使用默认重启策略",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		instanceType = "container" // 默认按容器处理
	}

	// 根据实例类型选择不同的重启策略
	if instanceType == "virtual-machine" {
		return i.restartVMForNetwork(instanceName)
	} else {
		return i.restartContainerForNetwork(instanceName)
	}
}

// restartVMForNetwork 重启虚拟机以获取网络配置
func (i *IncusProvider) restartVMForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启虚拟机获取网络配置", zap.String("instanceName", instanceName))

	// 尝试优雅重启，给虚拟机足够的超时时间
	restartCmd := fmt.Sprintf("incus restart %s --timeout=120", instanceName)
	_, err := i.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("优雅重启虚拟机失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 如果优雅重启失败，尝试强制停止后重启
		return i.forceRestartVM(instanceName)
	}

	// 等待虚拟机完全启动并获取IP
	return i.waitForVMNetworkReady(instanceName)
}

// restartContainerForNetwork 重启容器以获取网络配置
func (i *IncusProvider) restartContainerForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启容器获取网络配置", zap.String("instanceName", instanceName))

	// 容器重启
	restartCmd := fmt.Sprintf("incus restart %s --timeout=60", instanceName)
	_, err := i.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("容器重启失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 强制重启容器
		return i.forceRestartContainer(instanceName)
	}

	// 等待容器启动并获取IP
	return i.waitForContainerNetworkReady(instanceName)
}

// forceRestartVM 强制重启虚拟机
func (i *IncusProvider) forceRestartVM(instanceName string) error {
	global.APP_LOG.Info("强制重启虚拟机", zap.String("instanceName", instanceName))

	// 强制停止虚拟机
	stopCmd := fmt.Sprintf("incus stop %s --force --timeout=60", instanceName)
	_, err := i.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止虚拟机失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止虚拟机失败: %w", err)
	}

	// 等待完全停止
	time.Sleep(10 * time.Second)

	// 启动虚拟机
	startCmd := fmt.Sprintf("incus start %s", instanceName)
	_, err = i.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动虚拟机失败: %w", err)
	}

	// 等待虚拟机网络就绪
	return i.waitForVMNetworkReady(instanceName)
}

// forceRestartContainer 强制重启容器
func (i *IncusProvider) forceRestartContainer(instanceName string) error {
	global.APP_LOG.Info("强制重启容器", zap.String("instanceName", instanceName))

	// 强制停止容器
	stopCmd := fmt.Sprintf("incus stop %s --force --timeout=30", instanceName)
	_, err := i.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止容器失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止容器失败: %w", err)
	}

	// 短暂等待
	time.Sleep(3 * time.Second)

	// 启动容器
	startCmd := fmt.Sprintf("incus start %s", instanceName)
	_, err = i.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动容器失败: %w", err)
	}

	// 等待容器网络就绪
	return i.waitForContainerNetworkReady(instanceName)
}

// waitForInstanceReady 等待实例就绪
func (i *IncusProvider) waitForInstanceReady(instanceName string) error {
	maxWait := 60 // 等待60秒
	waited := 0

	for waited < maxWait {
		cmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := i.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) == "RUNNING" {
			// 额外等待网络配置就绪
			time.Sleep(5 * time.Second)
			return nil
		}

		time.Sleep(3 * time.Second)
		waited += 3
		global.APP_LOG.Debug("等待实例就绪",
			zap.String("instanceName", instanceName),
			zap.Int("waited", waited))
	}

	return fmt.Errorf("等待实例就绪超时")
}
