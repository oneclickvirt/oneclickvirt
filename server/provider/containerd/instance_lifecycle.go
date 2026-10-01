package containerd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (c *ContainerdProvider) rejectInfrastructureContainer(ctx context.Context, id string) error {
	if provider.IsRuntimeInfrastructureContainer(id) {
		return fmt.Errorf("refusing to manage runtime infrastructure container %s", id)
	}
	output, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, fmt.Sprintf("%s inspect -f '{{.Name}}' %s 2>/dev/null", cliName, shellSingleQuote(id)))
	if err != nil {
		return nil
	}
	for _, name := range strings.Split(output, "\n") {
		if provider.IsRuntimeInfrastructureContainer(name) {
			return fmt.Errorf("refusing to manage runtime infrastructure container %s", id)
		}
	}
	return nil
}

// sshStartInstance 启动实例
func (c *ContainerdProvider) sshStartInstance(ctx context.Context, id string) error {
	statusOutput, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to check container status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "running") {
		return c.restoreRoutedIPv6AfterStart(id)
	}

	startCmd := fmt.Sprintf("%s restart %s", cliName, shellSingleQuote(id))
	output, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, startCmd)
	if err != nil {
		global.APP_LOG.Error("Containerd实例启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start container: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}

	maxWaitTime := 30 * time.Second
	checkInterval := 2 * time.Second
	startTime := time.Now()

	for {
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待容器启动超时 (30秒)")
		}
		if err := utils.SleepContext(ctx, checkInterval); err != nil {
			return fmt.Errorf("waiting for container '%s' to start cancelled: %w", id, err)
		}
		statusOutput, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, shellSingleQuote(id)))
		if err == nil {
			currentStatus := strings.ToLower(strings.TrimSpace(statusOutput))
			if currentStatus == "running" {
				if err := utils.SleepContext(ctx, 2*time.Second); err != nil {
					return fmt.Errorf("waiting for container '%s' readiness cancelled: %w", id, err)
				}
				return c.restoreRoutedIPv6AfterStart(id)
			}
		}
	}
}

// sshStopInstance 停止实例
func (c *ContainerdProvider) sshStopInstance(ctx context.Context, id string) error {
	if err := c.rejectInfrastructureContainer(ctx, id); err != nil {
		return err
	}
	stopCmd := fmt.Sprintf("%s stop %s", cliName, shellSingleQuote(id))
	output, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, stopCmd)
	if err != nil {
		global.APP_LOG.Error("Containerd实例停止失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to stop container: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}

	maxRetries := 10
	retryInterval := 1 * time.Second
	for i := 0; i < maxRetries; i++ {
		statusOutput, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, shellSingleQuote(id)))
		if err != nil {
			if err := utils.SleepContext(ctx, retryInterval); err != nil {
				return err
			}
			continue
		}
		status := strings.ToLower(strings.TrimSpace(statusOutput))
		if strings.Contains(status, "exited") {
			return nil
		}
		if err := utils.SleepContext(ctx, retryInterval); err != nil {
			return err
		}
	}
	return nil
}

// sshRestartInstance 重启实例
func (c *ContainerdProvider) sshRestartInstance(ctx context.Context, id string) error {
	restartCmd := fmt.Sprintf("%s restart %s", cliName, shellSingleQuote(id))
	output, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, restartCmd)
	if err != nil {
		global.APP_LOG.Error("Containerd实例重启失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to restart container: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}
	global.APP_LOG.Info("Containerd实例重启成功", zap.String("id", utils.TruncateString(id, 32)))
	return c.restoreRoutedIPv6AfterStart(id)
}

// sshDeleteInstance 删除实例 - 多重删除策略
func (c *ContainerdProvider) sshDeleteInstance(ctx context.Context, id string) error {
	if err := c.rejectInfrastructureContainer(ctx, id); err != nil {
		return err
	}
	global.APP_LOG.Debug("开始删除Containerd实例", zap.String("id", utils.TruncateString(id, 32)))

	cleanupCmd := fmt.Sprintf("%s ps -a --filter %s --filter status=exited -q | xargs -r %s rm -f", cliName, containerNameFilter(id), cliName)
	if _, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, cleanupCmd); err != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	deleteStrategies := []struct {
		name     string
		commands []string
	}{
		{
			name: "graceful_stop_and_remove",
			commands: []string{
				fmt.Sprintf("%s stop %s", cliName, shellSingleQuote(id)),
				fmt.Sprintf("%s rm %s", cliName, shellSingleQuote(id)),
			},
		},
		{
			name: "force_remove",
			commands: []string{
				fmt.Sprintf("%s rm -f %s", cliName, shellSingleQuote(id)),
			},
		},
		{
			name: "kill_and_remove",
			commands: []string{
				fmt.Sprintf("%s kill %s", cliName, shellSingleQuote(id)),
				fmt.Sprintf("%s rm %s", cliName, shellSingleQuote(id)),
			},
		},
	}

	maxRetries := 3
	retryDelay := 2 * time.Second

	for strategyIndex, strategy := range deleteStrategies {
		for retry := 1; retry <= maxRetries; retry++ {
			success := true

			for _, cmd := range strategy.commands {
				output, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, cmd)
				if err != nil {
					if c.isAcceptableError(err, output) {
						continue
					}
					success = false
					break
				}
			}

			if success {
				if c.verifyContainerDeleted(ctx, id) {
					global.APP_LOG.Info("Containerd实例删除成功",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("strategy", strategy.name))
					return nil
				}
				success = false
			}

			if !success && retry < maxRetries {
				retryTimer := time.NewTimer(retryDelay)
				select {
				case <-ctx.Done():
					retryTimer.Stop()
					return ctx.Err()
				case <-retryTimer.C:
				}
			}
		}

		if strategyIndex < len(deleteStrategies)-1 {
			if err := utils.SleepContext(ctx, time.Second); err != nil {
				return err
			}
		}
	}

	finalCleanupCmd := fmt.Sprintf("%s ps -a --filter %s -q | xargs -r %s rm -f", cliName, containerNameFilter(id), cliName)
	if _, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, finalCleanupCmd); err != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	if c.verifyContainerDeleted(ctx, id) {
		return nil
	}

	return fmt.Errorf("failed to delete container after trying all strategies: %s", id)
}

// isAcceptableError 检查是否是可以接受的错误
func (c *ContainerdProvider) isAcceptableError(err error, output string) bool {
	errorStr := strings.ToLower(err.Error())
	outputStr := strings.ToLower(output)
	acceptableErrors := []string{
		"no such container", "not found", "already removed",
		"container not found", "no containers to remove",
		"is not running", "cannot stop container", "no such process",
	}
	for _, acceptableErr := range acceptableErrors {
		if strings.Contains(errorStr, acceptableErr) || strings.Contains(outputStr, acceptableErr) {
			return true
		}
	}
	return false
}

// verifyContainerDeleted 验证容器是否真的被删除
func (c *ContainerdProvider) verifyContainerDeleted(ctx context.Context, id string) bool {
	checkCmd := fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, shellSingleQuote(id))
	output, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, checkCmd)
	if err != nil {
		outputStr := strings.ToLower(output)
		if strings.Contains(outputStr, "no such object") ||
			strings.Contains(outputStr, "no such container") ||
			strings.Contains(outputStr, "not found") {
			// continue to verify
		} else {
			return false
		}
	} else {
		return false
	}

	listByNameCmd := fmt.Sprintf("%s ps -a --filter %s --format '{{.Names}}:{{.Status}}'", cliName, containerNameFilter(id))
	listByNameOutput, listByNameErr := utils.ExecuteShellCommandContext(ctx, c.sshClient, listByNameCmd)
	if listByNameErr == nil && strings.TrimSpace(listByNameOutput) != "" {
		return false
	}

	listCmd := fmt.Sprintf("%s ps -a --filter %s --format '{{.ID}}'", cliName, shellSingleQuote("id="+id))
	listOutput, listErr := utils.ExecuteShellCommandContext(ctx, c.sshClient, listCmd)
	if listErr == nil && strings.TrimSpace(listOutput) != "" {
		return false
	}

	return true
}
