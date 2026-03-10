package podman

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// sshStartInstance 启动实例
func (p *PodmanProvider) sshStartInstance(ctx context.Context, id string) error {
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, id))
	if err != nil {
		return fmt.Errorf("failed to check container status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "running") {
		return nil
	}

	startCmd := fmt.Sprintf("%s restart %s", cliName, id)
	output, err := p.sshClient.Execute(startCmd)
	if err != nil {
		global.APP_LOG.Error("Podman实例启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start container: %w", err)
	}

	maxWaitTime := 30 * time.Second
	checkInterval := 2 * time.Second
	startTime := time.Now()

	for {
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待容器启动超时 (30秒)")
		}
		time.Sleep(checkInterval)
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, id))
		if err == nil {
			currentStatus := strings.ToLower(strings.TrimSpace(statusOutput))
			if currentStatus == "running" {
				time.Sleep(2 * time.Second)
				return nil
			}
		}
	}
}

// sshStopInstance 停止实例
func (p *PodmanProvider) sshStopInstance(ctx context.Context, id string) error {
	stopCmd := fmt.Sprintf("%s stop %s", cliName, id)
	output, err := p.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("Podman实例停止失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to stop container: %w", err)
	}

	maxRetries := 10
	retryInterval := 1 * time.Second
	for i := 0; i < maxRetries; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, id))
		if err != nil {
			time.Sleep(retryInterval)
			continue
		}
		status := strings.ToLower(strings.TrimSpace(statusOutput))
		if strings.Contains(status, "exited") {
			return nil
		}
		time.Sleep(retryInterval)
	}
	return nil
}

// sshRestartInstance 重启实例
func (p *PodmanProvider) sshRestartInstance(ctx context.Context, id string) error {
	restartCmd := fmt.Sprintf("%s restart %s", cliName, id)
	output, err := p.sshClient.Execute(restartCmd)
	if err != nil {
		global.APP_LOG.Error("Podman实例重启失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to restart container: %w", err)
	}
	global.APP_LOG.Info("Podman实例重启成功", zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// sshDeleteInstance 删除实例 - 多重删除策略
func (p *PodmanProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Debug("开始删除Podman实例", zap.String("id", utils.TruncateString(id, 32)))

	cleanupCmd := fmt.Sprintf("%s ps -a --filter name=^%s$ --filter status=exited -q | xargs -r %s rm -f", cliName, id, cliName)
	p.sshClient.Execute(cleanupCmd)

	deleteStrategies := []struct {
		name     string
		commands []string
	}{
		{
			name: "graceful_stop_and_remove",
			commands: []string{
				fmt.Sprintf("%s stop %s", cliName, id),
				fmt.Sprintf("%s rm %s", cliName, id),
			},
		},
		{
			name: "force_remove",
			commands: []string{
				fmt.Sprintf("%s rm -f %s", cliName, id),
			},
		},
		{
			name: "kill_and_remove",
			commands: []string{
				fmt.Sprintf("%s kill %s", cliName, id),
				fmt.Sprintf("%s rm %s", cliName, id),
			},
		},
	}

	maxRetries := 3
	retryDelay := 2 * time.Second

	for strategyIndex, strategy := range deleteStrategies {
		for retry := 1; retry <= maxRetries; retry++ {
			success := true

			for _, cmd := range strategy.commands {
				output, err := p.sshClient.Execute(cmd)
				if err != nil {
					if p.isAcceptableError(err, output) {
						continue
					}
					success = false
					break
				}
			}

			if success {
				if p.verifyContainerDeleted(ctx, id) {
					global.APP_LOG.Info("Podman实例删除成功",
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
			time.Sleep(1 * time.Second)
		}
	}

	finalCleanupCmd := fmt.Sprintf("%s ps -a --filter name=^%s$ -q | xargs -r %s rm -f", cliName, id, cliName)
	p.sshClient.Execute(finalCleanupCmd)

	if p.verifyContainerDeleted(ctx, id) {
		return nil
	}

	return fmt.Errorf("failed to delete container after trying all strategies: %s", id)
}

// isAcceptableError 检查是否是可以接受的错误
func (p *PodmanProvider) isAcceptableError(err error, output string) bool {
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
func (p *PodmanProvider) verifyContainerDeleted(ctx context.Context, id string) bool {
	checkCmd := fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, id)
	output, err := p.sshClient.Execute(checkCmd)
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

	listByNameCmd := fmt.Sprintf("%s ps -a --filter name=^%s$ --format '{{.Names}}:{{.Status}}'", cliName, id)
	listByNameOutput, listByNameErr := p.sshClient.Execute(listByNameCmd)
	if listByNameErr == nil && strings.TrimSpace(listByNameOutput) != "" {
		return false
	}

	listCmd := fmt.Sprintf("%s ps -a --filter id=%s --format '{{.ID}}'", cliName, id)
	listOutput, listErr := p.sshClient.Execute(listCmd)
	if listErr == nil && strings.TrimSpace(listOutput) != "" {
		return false
	}

	return true
}
