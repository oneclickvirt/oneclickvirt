package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// SetInstancePassword 设置实例密码
func (d *DockerProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !d.connected {
		return fmt.Errorf("provider not connected")
	}

	// 对于Docker容器，通过SSH方式设置密码
	return d.sshSetInstancePassword(ctx, instanceID, password)
}

// ResetInstancePassword 重置实例密码
func (d *DockerProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	if !d.connected {
		return "", fmt.Errorf("provider not connected")
	}

	// 生成随机密码
	newPassword := d.generateRandomPassword()

	// 设置新密码
	err := d.sshSetInstancePassword(ctx, instanceID, newPassword)
	if err != nil {
		return "", err
	}

	return newPassword, nil
}

// sshSetInstancePassword 通过SSH设置容器密码
func (d *DockerProvider) sshSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// 确保SSH脚本文件可用
	if err := d.ensureSSHScriptsAvailable(d.config.Country); err != nil {
		global.APP_LOG.Error("确保SSH脚本可用失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("确保SSH脚本可用失败: %w", err)
	}

	// 验证容器是否存在且运行中，支持重试等待容器启动
	var containerStatus string
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		checkCmd := fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", instanceID)
		output, err := d.sshClient.Execute(checkCmd)
		if err != nil {
			global.APP_LOG.Error("检查容器状态失败",
				zap.String("instanceID", instanceID),
				zap.Int("attempt", i+1),
				zap.Error(err))
			if i < maxRetries-1 {
				time.Sleep(5 * time.Second) // 统一等待时间为5秒
				continue
			}
			return fmt.Errorf("检查容器状态失败: %w", err)
		}

		containerStatus = strings.TrimSpace(output)
		if containerStatus == "running" {
			// 容器运行中，再等待额外时间确保容器内部服务完全启动
			global.APP_LOG.Info("容器已启动，等待内部服务完全初始化",
				zap.String("instanceID", utils.TruncateString(instanceID, 12)),
				zap.Int("waitSeconds", 10))
			time.Sleep(10 * time.Second)
			break
		}

		global.APP_LOG.Info("容器状态检查",
			zap.String("instanceID", instanceID),
			zap.String("status", containerStatus),
			zap.Int("attempt", i+1))

		if i < maxRetries-1 {
			waitSeconds := 10 // 等待时间
			global.APP_LOG.Info("等待容器启动",
				zap.String("instanceID", instanceID),
				zap.Int("waitSeconds", waitSeconds))
			time.Sleep(time.Duration(waitSeconds) * time.Second)
		}
	}

	if containerStatus != "running" {
		global.APP_LOG.Error("容器最终状态检查失败",
			zap.String("instanceID", instanceID),
			zap.String("status", containerStatus))
		return fmt.Errorf("容器 %s 状态为 %s，无法设置密码", instanceID, containerStatus)
	}

	// 额外检查容器是否真正可用（测试基础命令）
	healthCheckCmd := fmt.Sprintf("docker exec %s echo 'container_ready' 2>/dev/null", instanceID)
	healthOutput, err := d.sshClient.Execute(healthCheckCmd)
	if err != nil || !strings.Contains(healthOutput, "container_ready") {
		global.APP_LOG.Warn("容器健康检查失败，再等待一段时间",
			zap.String("instanceID", utils.TruncateString(instanceID, 12)),
			zap.Error(err))
		time.Sleep(15 * time.Second)

		// 重新尝试健康检查
		healthOutput, err = d.sshClient.Execute(healthCheckCmd)
		if err != nil || !strings.Contains(healthOutput, "container_ready") {
			global.APP_LOG.Error("容器健康检查仍然失败",
				zap.String("instanceID", instanceID),
				zap.String("output", utils.TruncateString(healthOutput, 200)),
				zap.Error(err))
			return fmt.Errorf("容器 %s 未准备就绪，无法执行操作", instanceID)
		}
	}

	// 检查SSH相关进程和服务是否可用（更具体的就绪检查）
	sshReadinessCmd := fmt.Sprintf("docker exec %s sh -c 'command -v passwd >/dev/null 2>&1 && echo ssh_ready' 2>/dev/null", instanceID)
	sshOutput, err := d.sshClient.Execute(sshReadinessCmd)
	if err != nil || !strings.Contains(sshOutput, "ssh_ready") {
		global.APP_LOG.Warn("SSH服务未就绪，等待初始化",
			zap.String("instanceID", utils.TruncateString(instanceID, 12)),
			zap.Error(err))

		// 等待SSH服务就绪，最多重试5次
		maxSSHRetries := 5
		for i := 0; i < maxSSHRetries; i++ {
			time.Sleep(10 * time.Second)
			sshOutput, err = d.sshClient.Execute(sshReadinessCmd)
			if err == nil && strings.Contains(sshOutput, "ssh_ready") {
				global.APP_LOG.Info("SSH服务已就绪",
					zap.String("instanceID", utils.TruncateString(instanceID, 12)),
					zap.Int("waitAttempts", i+1))
				break
			}

			if i == maxSSHRetries-1 {
				global.APP_LOG.Error("SSH服务最终未就绪",
					zap.String("instanceID", instanceID),
					zap.String("output", utils.TruncateString(sshOutput, 200)),
					zap.Error(err))
				return fmt.Errorf("容器 %s SSH服务未就绪，无法设置密码", instanceID)
			}

			global.APP_LOG.Info("等待SSH服务就绪",
				zap.String("instanceID", utils.TruncateString(instanceID, 12)),
				zap.Int("attempt", i+1),
				zap.Int("maxRetries", maxSSHRetries))
		}
	}

	global.APP_LOG.Info("容器状态和SSH服务检查通过",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)))

	// 检测容器操作系统类型
	osCmd := fmt.Sprintf("docker exec %s cat /etc/os-release 2>/dev/null | grep -E '^ID=' | cut -d '=' -f 2 | tr -d '\"'", instanceID)
	osOutput, err := d.sshClient.Execute(osCmd)
	osType := utils.CleanCommandOutput(osOutput)
	if err != nil || osType == "" {
		// 如果无法检测，默认为非Alpine系统
		osType = "debian"
		global.APP_LOG.Warn("无法检测容器操作系统类型，默认使用debian",
			zap.String("instanceID", instanceID))
	}

	global.APP_LOG.Info("检测到容器操作系统类型",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("osType", osType))

	// 根据操作系统类型选择合适的SSH脚本
	var scriptName string
	var shellType string
	if osType == "alpine" {
		scriptName = "ssh_sh.sh"
		shellType = "sh"
	} else {
		scriptName = "ssh_bash.sh"
		shellType = "bash"
	}

	// 检查宿主机上的SSH脚本是否存在
	hostScriptPath := fmt.Sprintf("/usr/local/bin/%s", scriptName)
	checkHostScriptCmd := fmt.Sprintf("test -f %s && test -x %s", hostScriptPath, hostScriptPath)
	_, err = d.sshClient.Execute(checkHostScriptCmd)

	if err != nil {
		global.APP_LOG.Error("宿主机上SSH脚本不存在或无执行权限",
			zap.String("instanceID", instanceID),
			zap.String("scriptPath", hostScriptPath),
			zap.Error(err))
		return fmt.Errorf("宿主机上SSH脚本不存在或无执行权限: %s", hostScriptPath)
	}

	// 检查容器内是否已存在SSH脚本
	checkScriptCmd := fmt.Sprintf("docker exec %s %s -c '[ -f /%s ]'", instanceID, shellType, scriptName)
	_, err = d.sshClient.Execute(checkScriptCmd)

	if err != nil {
		// 脚本不存在，需要复制
		global.APP_LOG.Info("容器内SSH脚本不存在，正在从宿主机复制",
			zap.String("instanceID", utils.TruncateString(instanceID, 12)),
			zap.String("scriptName", scriptName))

		// 复制脚本到容器内
		copyCmd := fmt.Sprintf("docker cp \"%s\" \"%s:/%s\"", hostScriptPath, instanceID, scriptName)
		_, err = d.sshClient.Execute(copyCmd)
		if err != nil {
			global.APP_LOG.Error("复制SSH脚本到容器失败",
				zap.String("instanceID", instanceID),
				zap.String("scriptName", scriptName),
				zap.Error(err))
			return fmt.Errorf("复制SSH脚本到容器失败: %w", err)
		}

		// 给脚本添加执行权限
		chmodCmd := fmt.Sprintf("docker exec %s %s -c 'chmod +x /%s'", instanceID, shellType, scriptName)
		_, err = d.sshClient.Execute(chmodCmd)
		if err != nil {
			global.APP_LOG.Error("设置SSH脚本执行权限失败",
				zap.String("instanceID", instanceID),
				zap.String("scriptName", scriptName),
				zap.Error(err))
		}

		global.APP_LOG.Info("SSH脚本复制到容器成功",
			zap.String("instanceID", utils.TruncateString(instanceID, 12)),
			zap.String("scriptName", scriptName))
	} else {
		global.APP_LOG.Info("容器内SSH脚本已存在",
			zap.String("instanceID", utils.TruncateString(instanceID, 12)),
			zap.String("scriptName", scriptName))
	}

	// 设置interactionless环境变量，避免交互式操作
	envCmd := fmt.Sprintf("docker exec %s %s -c 'export interactionless=true'", instanceID, shellType)
	d.sshClient.Execute(envCmd)

	// 执行SSH配置脚本
	executeScriptCmd := fmt.Sprintf("docker exec %s %s -c 'interactionless=true %s /%s %s'", instanceID, shellType, shellType, scriptName, password)
	scriptOutput, err := d.sshClient.Execute(executeScriptCmd)
	if err != nil {
		global.APP_LOG.Error("执行SSH配置脚本失败",
			zap.String("instanceID", instanceID),
			zap.String("scriptName", scriptName),
			zap.String("output", utils.TruncateString(scriptOutput, 500)),
			zap.Error(err))
		return fmt.Errorf("执行SSH配置脚本失败: %w", err)
	}

	// 额外使用chpasswd命令确保密码设置
	setPasswordCmd := fmt.Sprintf("docker exec %s %s -c 'echo \"root:%s\" | chpasswd'", instanceID, shellType, password)
	_, err = d.sshClient.Execute(setPasswordCmd)
	if err != nil {
		global.APP_LOG.Error("使用chpasswd设置密码失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("使用chpasswd设置密码失败: %w", err)
	}

	global.APP_LOG.Info("容器SSH密码设置成功",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("osType", osType),
		zap.String("scriptName", scriptName))

	return nil
}

// generateRandomPassword 生成随机密码（仅包含数字和大小写英文字母，长度不低于8位）
func (d *DockerProvider) generateRandomPassword() string {
	return utils.GenerateInstancePassword()
}
