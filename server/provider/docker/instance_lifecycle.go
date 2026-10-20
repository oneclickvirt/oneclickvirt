package docker

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

func (d *DockerProvider) rejectInfrastructureContainer(ctx context.Context, id string) error {
	if provider.IsRuntimeInfrastructureContainer(id) {
		return fmt.Errorf("refusing to manage runtime infrastructure container %s", id)
	}
	output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, fmt.Sprintf("%s inspect -f '{{.Name}}' %s 2>/dev/null", d.runtime.CLI, shellSingleQuote(id)))
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

// sshStartInstance starts without restarting a running guest.
func (d *DockerProvider) sshStartInstance(ctx context.Context, id string) error {
	if err := utils.EnsureRuntimeContainerRunning(ctx, d.sshClient, d.runtime.CLI, id); err != nil {
		return err
	}
	return d.restoreRoutedIPv6AfterStart(id, ctx)
}

// sshStopInstance confirms stopped state before returning success.
func (d *DockerProvider) sshStopInstance(ctx context.Context, id string) error {
	if err := d.rejectInfrastructureContainer(ctx, id); err != nil {
		return err
	}
	return utils.StopRuntimeContainer(ctx, d.sshClient, d.runtime.CLI, id)
}

// sshRestartInstance 重启实例
func (d *DockerProvider) sshRestartInstance(ctx context.Context, id string) error {
	restartCmd := fmt.Sprintf("%s restart %s", d.runtime.CLI, shellSingleQuote(id))
	global.APP_LOG.Debug("开始重启Docker实例",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", restartCmd))

	output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, restartCmd)
	if err != nil {
		global.APP_LOG.Error("Docker实例重启失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("command", restartCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to restart container: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}

	global.APP_LOG.Info("Docker实例重启成功", zap.String("id", utils.TruncateString(id, 32)))
	return d.restoreRoutedIPv6AfterStart(id, ctx)
}

func isDockerIptablesChainError(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "iptables") &&
		(strings.Contains(lower, "no chain") ||
			strings.Contains(lower, "no chain/target/match") ||
			strings.Contains(lower, "no such file or directory"))
}

// sshDeleteInstance 删除实例 - 增强版，多重删除策略
func (d *DockerProvider) sshDeleteInstance(ctx context.Context, id string) (deleteErr error) {
	cleanupName := id
	if d.runtime.ProviderType == "docker" {
		output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, fmt.Sprintf("%s inspect -f '{{.Name}}' %s 2>/dev/null", d.runtime.CLI, shellSingleQuote(id)))
		if err == nil && strings.TrimSpace(output) != "" {
			cleanupName = strings.TrimPrefix(strings.TrimSpace(output), "/")
		}
	}
	defer func() {
		if deleteErr == nil && d.runtime.ProviderType == "docker" {
			command := "if test -x " + shellSingleQuote(dockerManualIPv6Helper) + "; then " + shellSingleQuote(dockerManualIPv6Helper) + " --remove " + shellSingleQuote(cleanupName) + "; fi"
			if output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, command); err != nil {
				deleteErr = fmt.Errorf("容器已删除，但路由IPv6回收失败: output=%s: %w", utils.TruncateString(output, 1000), err)
			}
		}
	}()
	if err := d.rejectInfrastructureContainer(ctx, id); err != nil {
		return err
	}
	global.APP_LOG.Debug("开始删除Docker实例",
		zap.String("id", utils.TruncateString(id, 32)))

	// 预清理：先尝试删除所有同名的已停止容器（Exited状态）
	cleanupCmd := fmt.Sprintf("%s ps -a --filter %s --filter status=exited -q | xargs -r %s rm -f", d.runtime.CLI, containerNameFilter(id), d.runtime.CLI)
	global.APP_LOG.Debug("清理已停止的同名容器",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", cleanupCmd))

	cleanupOutput, cleanupErr := utils.ExecuteShellCommandContext(ctx, d.sshClient, cleanupCmd)
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
				fmt.Sprintf("%s stop %s", d.runtime.CLI, shellSingleQuote(id)),
				fmt.Sprintf("%s rm %s", d.runtime.CLI, shellSingleQuote(id)),
			},
			description: "优雅停止并删除容器",
		},
		{
			name: "force_remove_running",
			commands: []string{
				fmt.Sprintf("%s rm -f %s", d.runtime.CLI, shellSingleQuote(id)),
			},
			description: "强制删除正在运行的容器",
		},
		{
			name: "kill_and_remove",
			commands: []string{
				fmt.Sprintf("%s kill %s", d.runtime.CLI, shellSingleQuote(id)),
				fmt.Sprintf("%s rm %s", d.runtime.CLI, shellSingleQuote(id)),
			},
			description: "强制杀死进程并删除容器",
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

				output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, cmd)
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
			if err := utils.SleepContext(ctx, time.Second); err != nil {
				return err
			}
		}
	}

	// 最后的强制清理：尝试删除所有同名的已停止容器
	global.APP_LOG.Debug("执行最终清理，删除所有同名已停止容器",
		zap.String("id", utils.TruncateString(id, 32)))

	finalCleanupCmd := fmt.Sprintf("%s ps -a --filter %s -q | xargs -r %s rm -f", d.runtime.CLI, containerNameFilter(id), d.runtime.CLI)
	finalOutput, finalErr := utils.ExecuteShellCommandContext(ctx, d.sshClient, finalCleanupCmd)
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
	checkCmd := fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", d.runtime.CLI, shellSingleQuote(id))
	output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, checkCmd)

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
	listByNameCmd := fmt.Sprintf("%s ps -a --filter %s --format '{{.Names}}:{{.Status}}'", d.runtime.CLI, containerNameFilter(id))
	listByNameOutput, listByNameErr := utils.ExecuteShellCommandContext(ctx, d.sshClient, listByNameCmd)

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
	listCmd := fmt.Sprintf("%s ps -a --filter %s --format '{{.ID}}'", d.runtime.CLI, shellSingleQuote("id="+id))
	listOutput, listErr := utils.ExecuteShellCommandContext(ctx, d.sshClient, listCmd)

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
