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
		checkCmd := fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, shellSingleQuote(instanceID))
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
	healthCheckCmd := fmt.Sprintf("%s exec %s echo 'container_ready' 2>/dev/null", cliName, shellSingleQuote(instanceID))
	healthOutput, err := c.sshClient.Execute(healthCheckCmd)
	if err != nil || !strings.Contains(healthOutput, "container_ready") {
		time.Sleep(15 * time.Second)
		healthOutput, err = c.sshClient.Execute(healthCheckCmd)
		if err != nil || !strings.Contains(healthOutput, "container_ready") {
			return fmt.Errorf("容器 %s 未准备就绪，无法执行操作", instanceID)
		}
	}

	// SSH就绪检查
	sshReadinessCmd := fmt.Sprintf("%s exec %s sh -c %s 2>/dev/null", cliName, shellSingleQuote(instanceID), shellSingleQuote("command -v passwd >/dev/null 2>&1 && echo ssh_ready"))
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
	osCmd := fmt.Sprintf("%s exec %s cat /etc/os-release 2>/dev/null | grep -E '^ID=' | cut -d '=' -f 2 | tr -d '\"'", cliName, shellSingleQuote(instanceID))
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
	checkHostScriptCmd := fmt.Sprintf("test -f %s && test -x %s", shellSingleQuote(hostScriptPath), shellSingleQuote(hostScriptPath))
	_, hostScriptErr := c.sshClient.Execute(checkHostScriptCmd)

	if hostScriptErr == nil {
		checkScriptCmd := fmt.Sprintf("%s exec %s %s -c %s", cliName, shellSingleQuote(instanceID), shellSingleQuote(shellType), shellSingleQuote("[ -f /"+scriptName+" ]"))
		_, err = c.sshClient.Execute(checkScriptCmd)
		if err != nil {
			copyCmd := fmt.Sprintf("%s cp %s %s", cliName, shellSingleQuote(hostScriptPath), shellSingleQuote(instanceID+":/"+scriptName))
			_, err = c.sshClient.Execute(copyCmd)
			if err == nil {
				chmodCmd := fmt.Sprintf("%s exec %s %s -c %s", cliName, shellSingleQuote(instanceID), shellSingleQuote(shellType), shellSingleQuote("chmod +x /"+scriptName))
				c.sshClient.Execute(chmodCmd)
			}
		}

		// 使用临时脚本方式执行 SSH 配置脚本，避免 agent 模式下 WebSocket 超时
		sshInnerCmd := fmt.Sprintf("interactionless=true %s /%s %s", shellType, scriptName, shellSingleQuote(password))
		sshExecScript := utils.BuildTempScript(utils.TempScriptConfig{
			PrimaryCmd: fmt.Sprintf("%s exec %s %s -c %s",
				cliName, shellSingleQuote(instanceID), shellSingleQuote(shellType), shellSingleQuote(sshInnerCmd)),
			TimeoutSeconds: 60,
		})
		scriptOutput, scriptErr := c.sshClient.ExecuteViaTempScript(sshExecScript, nil, 180*time.Second)
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
