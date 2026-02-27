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

// sshStartInstance 启动实例
func (d *DockerProvider) sshStartInstance(ctx context.Context, id string) error {
	// 先检查容器状态，如果是Exited状态则使用restart命令
	statusOutput, err := d.sshClient.Execute(fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id))
	if err != nil {
		global.APP_LOG.Error("检查Docker容器状态失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.Error(err))
		return fmt.Errorf("failed to check container status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	var startCmd string
	startCmd = fmt.Sprintf("docker restart %s", id)
	if strings.Contains(status, "exited") {
		global.APP_LOG.Debug("检测到容器为Exited状态，使用restart命令",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("status", status))
	} else if strings.Contains(status, "running") {
		global.APP_LOG.Debug("容器已在运行", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	global.APP_LOG.Debug("开始启动Docker实例",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", startCmd))

	output, err := d.sshClient.Execute(startCmd)
	if err != nil {
		global.APP_LOG.Error("Docker实例启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("command", startCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start container: %w", err)
	}

	// 等待容器真正启动 - 最多等待30秒
	maxWaitTime := 30 * time.Second
	checkInterval := 2 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待容器启动超时 (30秒)")
		}

		// 等待一段时间后再检查
		time.Sleep(checkInterval)

		// 检查容器状态
		statusOutput, err := d.sshClient.Execute(fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id))
		if err == nil {
			currentStatus := strings.ToLower(strings.TrimSpace(statusOutput))
			if currentStatus == "running" {
				// 容器已经启动，再等待额外的时间确保服务完全就绪
				time.Sleep(2 * time.Second)
				global.APP_LOG.Debug("Docker容器已成功启动并就绪",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.Duration("wait_time", time.Since(startTime)))
				return nil
			}
		}

		global.APP_LOG.Debug("等待容器启动",
			zap.String("id", id),
			zap.Duration("elapsed", time.Since(startTime)))
	}
}

// sshStopInstance 停止实例
func (d *DockerProvider) sshStopInstance(ctx context.Context, id string) error {
	stopCmd := fmt.Sprintf("docker stop %s", id)
	global.APP_LOG.Debug("开始停止Docker实例",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", stopCmd))
	output, err := d.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("Docker实例停止失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("command", stopCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// 等待并验证容器状态
	maxRetries := 10
	retryInterval := 1 * time.Second
	for i := 0; i < maxRetries; i++ {
		statusOutput, err := d.sshClient.Execute(fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id))
		if err != nil {
			global.APP_LOG.Warn("检查Docker容器停止状态失败",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.Int("retry", i+1),
				zap.Error(err))
			time.Sleep(retryInterval)
			continue
		}

		status := strings.ToLower(strings.TrimSpace(statusOutput))
		if strings.Contains(status, "exited") {
			global.APP_LOG.Debug("Docker实例停止成功并已确认状态",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.String("status", status))
			return nil
		}

		global.APP_LOG.Debug("等待Docker容器停止",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("current_status", status),
			zap.Int("retry", i+1))
		time.Sleep(retryInterval)
	}

	global.APP_LOG.Warn("Docker实例停止命令执行成功但状态验证超时",
		zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// sshRestartInstance 重启实例
func (d *DockerProvider) sshRestartInstance(ctx context.Context, id string) error {
	restartCmd := fmt.Sprintf("docker restart %s", id)
	global.APP_LOG.Debug("开始重启Docker实例",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", restartCmd))

	output, err := d.sshClient.Execute(restartCmd)
	if err != nil {
		global.APP_LOG.Error("Docker实例重启失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("command", restartCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to restart container: %w", err)
	}

	global.APP_LOG.Info("Docker实例重启成功", zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// sshDeleteInstance 删除实例 - 增强版，多重删除策略
func (d *DockerProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Debug("开始删除Docker实例",
		zap.String("id", utils.TruncateString(id, 32)))

	// 预清理：先尝试删除所有同名的已停止容器（Exited状态）
	cleanupCmd := fmt.Sprintf("docker ps -a --filter name=^%s$ --filter status=exited -q | xargs -r docker rm -f", id)
	global.APP_LOG.Debug("清理已停止的同名容器",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", cleanupCmd))

	cleanupOutput, cleanupErr := d.sshClient.Execute(cleanupCmd)
	if cleanupErr != nil {
		global.APP_LOG.Debug("清理已停止容器失败（可忽略）",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(cleanupOutput, 200)),
			zap.Error(cleanupErr))
	} else {
		global.APP_LOG.Debug("已清理已停止的同名容器",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(cleanupOutput, 200)))
	}

	// 定义多种删除策略，按优先级顺序执行
	deleteStrategies := []struct {
		name        string
		commands    []string
		description string
	}{
		{
			name: "graceful_stop_and_remove",
			commands: []string{
				fmt.Sprintf("docker stop %s", id),
				fmt.Sprintf("docker rm %s", id),
			},
			description: "优雅停止并删除容器",
		},
		{
			name: "force_remove_running",
			commands: []string{
				fmt.Sprintf("docker rm -f %s", id),
			},
			description: "强制删除正在运行的容器",
		},
		{
			name: "kill_and_remove",
			commands: []string{
				fmt.Sprintf("docker kill %s", id),
				fmt.Sprintf("docker rm %s", id),
			},
			description: "强制杀死进程并删除容器",
		},
		{
			name: "system_prune_targeted",
			commands: []string{
				fmt.Sprintf("docker rm -f %s", id),
				"docker system prune -f --volumes",
			},
			description: "删除容器并清理系统资源",
		},
	}

	maxRetries := 3
	retryDelay := 2 * time.Second

	// 尝试每种删除策略
	for strategyIndex, strategy := range deleteStrategies {
		global.APP_LOG.Debug("尝试删除策略",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("strategy", strategy.name),
			zap.String("description", strategy.description),
			zap.Int("strategyIndex", strategyIndex+1),
			zap.Int("totalStrategies", len(deleteStrategies)))

		// 对每种策略进行重试
		for retry := 1; retry <= maxRetries; retry++ {
			success := true
			var lastErr error

			// 执行策略中的所有命令
			for cmdIndex, cmd := range strategy.commands {
				global.APP_LOG.Debug("执行删除命令",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.String("strategy", strategy.name),
					zap.Int("retry", retry),
					zap.Int("cmdIndex", cmdIndex+1),
					zap.String("command", cmd))

				output, err := d.sshClient.Execute(cmd)
				if err != nil {
					// 某些错误是可以接受的
					if d.isAcceptableError(err, output) {
						global.APP_LOG.Debug("命令执行失败但错误可接受",
							zap.String("id", utils.TruncateString(id, 32)),
							zap.String("command", cmd),
							zap.String("output", utils.TruncateString(output, 200)),
							zap.Error(err))
						continue
					}

					global.APP_LOG.Warn("删除命令执行失败",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("strategy", strategy.name),
						zap.Int("retry", retry),
						zap.String("command", cmd),
						zap.String("output", utils.TruncateString(output, 200)),
						zap.Error(err))

					lastErr = err
					success = false
					break
				} else {
					global.APP_LOG.Debug("删除命令执行成功",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("command", cmd),
						zap.String("output", utils.TruncateString(output, 100)))
				}
			}

			// 如果所有命令都成功执行，验证容器是否真的被删除
			if success {
				if d.verifyContainerDeleted(ctx, id) {
					global.APP_LOG.Info("Docker实例删除成功",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("strategy", strategy.name),
						zap.Int("retry", retry))
					return nil
				} else {
					global.APP_LOG.Warn("删除命令执行成功但容器仍存在",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("strategy", strategy.name),
						zap.Int("retry", retry))
					success = false
				}
			}

			// 如果失败，等待后重试
			if !success && retry < maxRetries {
				global.APP_LOG.Debug("等待后重试删除",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.String("strategy", strategy.name),
					zap.Int("retry", retry),
					zap.Duration("delay", retryDelay))

				// 使用Timer避免time.After泄漏
				retryTimer := time.NewTimer(retryDelay)
				select {
				case <-ctx.Done():
					retryTimer.Stop()
					return ctx.Err()
				case <-retryTimer.C:
					// 继续重试
				}
			}

			// 如果成功或达到最大重试次数，跳出重试循环
			if success {
				break
			} else if retry == maxRetries {
				global.APP_LOG.Warn("删除策略达到最大重试次数",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.String("strategy", strategy.name),
					zap.Int("maxRetries", maxRetries),
					zap.Error(lastErr))
			}
		}

		// 在策略之间等待一下，让系统稳定
		if strategyIndex < len(deleteStrategies)-1 {
			time.Sleep(1 * time.Second)
		}
	}

	// 最后的强制清理：尝试删除所有同名的已停止容器
	global.APP_LOG.Debug("执行最终清理，删除所有同名已停止容器",
		zap.String("id", utils.TruncateString(id, 32)))

	finalCleanupCmd := fmt.Sprintf("docker ps -a --filter name=^%s$ -q | xargs -r docker rm -f", id)
	finalOutput, finalErr := d.sshClient.Execute(finalCleanupCmd)
	if finalErr != nil {
		global.APP_LOG.Debug("最终清理失败（可忽略）",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(finalOutput, 200)),
			zap.Error(finalErr))
	} else {
		global.APP_LOG.Debug("最终清理执行完成",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(finalOutput, 200)))
	}

	// 最后再次验证容器是否被删除
	if d.verifyContainerDeleted(ctx, id) {
		global.APP_LOG.Info("Docker实例最终确认删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	// 所有策略都失败了
	global.APP_LOG.Error("所有删除策略都失败，容器可能仍然存在",
		zap.String("id", utils.TruncateString(id, 32)))
	return fmt.Errorf("failed to delete container after trying all strategies: %s", id)
}

// isAcceptableError 检查是否是可以接受的错误（例如容器已经不存在）
func (d *DockerProvider) isAcceptableError(err error, output string) bool {
	errorStr := strings.ToLower(err.Error())
	outputStr := strings.ToLower(output)
	acceptableErrors := []string{
		"no such container",
		"not found",
		"already removed",
		"container not found",
		"no containers to remove",
		"is not running",
		"cannot stop container",
		"no such process",
	}
	for _, acceptableErr := range acceptableErrors {
		if strings.Contains(errorStr, acceptableErr) || strings.Contains(outputStr, acceptableErr) {
			return true
		}
	}
	return false
}

// verifyContainerDeleted 验证容器是否真的被删除（包括已停止的容器）
func (d *DockerProvider) verifyContainerDeleted(ctx context.Context, id string) bool {
	// 方法1：检查运行中的容器
	checkCmd := fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id)
	output, err := d.sshClient.Execute(checkCmd)

	if err != nil {
		// 如果命令失败，很可能是容器不存在了
		outputStr := strings.ToLower(output)
		if strings.Contains(outputStr, "no such object") ||
			strings.Contains(outputStr, "no such container") ||
			strings.Contains(outputStr, "not found") {
			// 继续检查是否有已停止的同名容器
			global.APP_LOG.Debug("inspect未找到容器，继续验证",
				zap.String("id", utils.TruncateString(id, 32)))
		} else {
			global.APP_LOG.Warn("验证容器删除时inspect出错",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.String("output", utils.TruncateString(output, 100)),
				zap.Error(err))
			return false
		}
	} else {
		// inspect成功，说明容器还在
		global.APP_LOG.Warn("容器仍然存在",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("status", strings.TrimSpace(output)))
		return false
	}

	// 方法2：通过docker ps -a检查所有状态的容器（包括已停止的）
	// 使用精确匹配的name filter
	listByNameCmd := fmt.Sprintf("docker ps -a --filter name=^%s$ --format '{{.Names}}:{{.Status}}'", id)
	listByNameOutput, listByNameErr := d.sshClient.Execute(listByNameCmd)

	if listByNameErr == nil {
		trimmedOutput := strings.TrimSpace(listByNameOutput)
		if trimmedOutput != "" {
			// 找到了同名容器，即使已停止也算删除失败
			global.APP_LOG.Warn("发现同名容器（可能已停止）",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.String("details", utils.TruncateString(trimmedOutput, 100)))
			return false
		}
	}

	// 方法3：用ID进行filter检查
	listCmd := fmt.Sprintf("docker ps -a --filter id=%s --format '{{.ID}}'", id)
	listOutput, listErr := d.sshClient.Execute(listCmd)

	if listErr == nil && strings.TrimSpace(listOutput) != "" {
		// 通过ID找到了容器
		global.APP_LOG.Warn("通过ID找到容器",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("foundId", utils.TruncateString(strings.TrimSpace(listOutput), 32)))
		return false
	}

	// 所有检查都通过，容器已被删除
	return true
}
