package containerd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// sshSetInstancePassword 通过SSH设置容器密码
func (c *ContainerdProvider) sshSetInstancePassword(ctx context.Context, instanceID, password string) error {
	if err := c.ensureSSHScriptsAvailable(c.config.Country); err != nil {
		return fmt.Errorf("确保SSH脚本可用失败: %w", err)
	}

	var containerStatus string
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		checkCmd := fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, instanceID)
		output, err := c.sshClient.Execute(checkCmd)
		if err != nil {
			if i < maxRetries-1 {
				time.Sleep(5 * time.Second)
				continue
			}
			return fmt.Errorf("检查容器状态失败: %w", err)
		}

		containerStatus = strings.TrimSpace(output)
		if containerStatus == "running" {
			time.Sleep(10 * time.Second)
			break
		}

		if i < maxRetries-1 {
			time.Sleep(10 * time.Second)
		}
	}

	if containerStatus != "running" {
		return fmt.Errorf("容器 %s 状态为 %s，无法设置密码", instanceID, containerStatus)
	}

	// 健康检查
	healthCheckCmd := fmt.Sprintf("%s exec %s echo 'container_ready' 2>/dev/null", cliName, instanceID)
	healthOutput, err := c.sshClient.Execute(healthCheckCmd)
	if err != nil || !strings.Contains(healthOutput, "container_ready") {
		time.Sleep(15 * time.Second)
		healthOutput, err = c.sshClient.Execute(healthCheckCmd)
		if err != nil || !strings.Contains(healthOutput, "container_ready") {
			return fmt.Errorf("容器 %s 未准备就绪，无法执行操作", instanceID)
		}
	}

	// SSH就绪检查
	sshReadinessCmd := fmt.Sprintf("%s exec %s sh -c 'command -v passwd >/dev/null 2>&1 && echo ssh_ready' 2>/dev/null", cliName, instanceID)
	sshOutput, err := c.sshClient.Execute(sshReadinessCmd)
	if err != nil || !strings.Contains(sshOutput, "ssh_ready") {
		maxSSHRetries := 5
		for i := 0; i < maxSSHRetries; i++ {
			time.Sleep(10 * time.Second)
			sshOutput, err = c.sshClient.Execute(sshReadinessCmd)
			if err == nil && strings.Contains(sshOutput, "ssh_ready") {
				break
			}
			if i == maxSSHRetries-1 {
				return fmt.Errorf("容器 %s SSH服务未就绪，无法设置密码", instanceID)
			}
		}
	}

	// 检测OS类型
	osCmd := fmt.Sprintf("%s exec %s cat /etc/os-release 2>/dev/null | grep -E '^ID=' | cut -d '=' -f 2 | tr -d '\"'", cliName, instanceID)
	osOutput, err := c.sshClient.Execute(osCmd)
	osType := utils.CleanCommandOutput(osOutput)
	if err != nil || osType == "" {
		osType = "debian"
	}

	var scriptName, shellType string
	if osType == "alpine" {
		scriptName = "ssh_sh.sh"
		shellType = "sh"
	} else {
		scriptName = "ssh_bash.sh"
		shellType = "bash"
	}

	hostScriptPath := fmt.Sprintf("/usr/local/bin/%s", scriptName)
	checkHostScriptCmd := fmt.Sprintf("test -f %s && test -x %s", hostScriptPath, hostScriptPath)
	_, hostScriptErr := c.sshClient.Execute(checkHostScriptCmd)

	if hostScriptErr == nil {
		checkScriptCmd := fmt.Sprintf("%s exec %s %s -c '[ -f /%s ]'", cliName, instanceID, shellType, scriptName)
		_, err = c.sshClient.Execute(checkScriptCmd)
		if err != nil {
			copyCmd := fmt.Sprintf("%s cp \"%s\" \"%s:/%s\"", cliName, hostScriptPath, instanceID, scriptName)
			_, err = c.sshClient.Execute(copyCmd)
			if err == nil {
				chmodCmd := fmt.Sprintf("%s exec %s %s -c 'chmod +x /%s'", cliName, instanceID, shellType, scriptName)
				c.sshClient.Execute(chmodCmd)
			}
		}

		executeScriptCmd := fmt.Sprintf("%s exec %s %s -c 'interactionless=true %s /%s %s'", cliName, instanceID, shellType, shellType, scriptName, password)
		scriptOutput, scriptErr := c.sshClient.Execute(executeScriptCmd)
		if scriptErr != nil {
			global.APP_LOG.Warn("执行SSH配置脚本失败，将直接用chpasswd设置密码",
				zap.String("instanceID", instanceID),
				zap.String("output", utils.TruncateString(scriptOutput, 500)),
				zap.Error(scriptErr))
			time.Sleep(5 * time.Second)
		}
	}

	if err := c.setContainerPasswordWithRetry(instanceID, password, shellType); err != nil {
		return fmt.Errorf("使用chpasswd设置密码失败: %w", err)
	}

	return nil
}

// generateRandomPassword 生成随机密码
func (c *ContainerdProvider) generateRandomPassword() string {
	return utils.GenerateInstancePassword()
}
