package lxd

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// ensureLXDInstanceRunningForExec makes sure an LXD instance is in a state where
// lxc exec can run commands. LXD containers may be left FROZEN/STOPPED during
// post-create or quota operations; password resets must recover those states
// instead of failing with a generic temp-script exit status.
func (l *LXDProvider) ensureLXDInstanceRunningForExec(instanceName string) error {
	if l.sshClient == nil {
		return fmt.Errorf("SSH客户端不可用")
	}

	statusCmd := fmt.Sprintf(
		"lxc list --format csv -c n,s | awk -F, -v n=%s '$1==n {print $2}'",
		shellSingleQuote(instanceName),
	)
	var lastStatus string
	var lastErr error

	for attempt := 1; attempt <= 18; attempt++ {
		output, err := l.sshClient.Execute(statusCmd)
		if err != nil {
			lastErr = err
			global.APP_LOG.Warn("检查LXD实例状态失败，准备重试",
				zap.String("instanceName", utils.TruncateString(instanceName, 32)),
				zap.Int("attempt", attempt),
				zap.Error(err))
		} else {
			lastStatus = strings.TrimSpace(output)
			if lastStatus == "" {
				return fmt.Errorf("实例 %s 不存在", instanceName)
			}

			if strings.EqualFold(lastStatus, "RUNNING") {
				readyCmd := fmt.Sprintf("lxc exec %s -- /bin/sh -c %s 2>/dev/null", shellSingleQuote(instanceName), shellSingleQuote("echo LXD_EXEC_READY"))
				readyOutput, readyErr := l.sshClient.Execute(readyCmd)
				if readyErr == nil && strings.Contains(readyOutput, "LXD_EXEC_READY") {
					return nil
				}
				lastErr = readyErr
				global.APP_LOG.Warn("LXD实例已运行但exec尚未就绪",
					zap.String("instanceName", utils.TruncateString(instanceName, 32)),
					zap.Int("attempt", attempt),
					zap.String("output", utils.TruncateString(readyOutput, 300)),
					zap.Error(readyErr))
			} else {
				global.APP_LOG.Warn("LXD实例不是RUNNING状态，尝试恢复后再设置密码",
					zap.String("instanceName", utils.TruncateString(instanceName, 32)),
					zap.String("status", lastStatus),
					zap.Int("attempt", attempt))

				// For STOPPED and FROZEN instances, lxc start is the most compatible
				// recovery action across old/new LXD releases. restart is only used
				// if start fails on later attempts.
				startCmd := fmt.Sprintf("lxc start %s 2>/dev/null || true", shellSingleQuote(instanceName))
				if _, startErr := l.sshClient.Execute(startCmd); startErr != nil {
					lastErr = startErr
				}
			}
		}

		if attempt%6 == 0 {
			restartCmd := fmt.Sprintf("lxc restart %s 2>/dev/null || lxc start %s 2>/dev/null || true", shellSingleQuote(instanceName), shellSingleQuote(instanceName))
			if _, restartErr := l.sshClient.Execute(restartCmd); restartErr != nil {
				lastErr = restartErr
			}
		}

		time.Sleep(5 * time.Second)
	}

	if lastErr != nil {
		return fmt.Errorf("实例 %s 无法进入可执行状态，最后状态: %s，最后错误: %w", instanceName, lastStatus, lastErr)
	}
	return fmt.Errorf("实例 %s 无法进入可执行状态，最后状态: %s", instanceName, lastStatus)
}

func buildLXDPasswordInnerCommand(password string) string {
	quotedPassword := shellSingleQuote(password)
	return fmt.Sprintf("if command -v chpasswd >/dev/null 2>&1; then printf 'root:%%s\\n' %s | chpasswd; elif command -v passwd >/dev/null 2>&1; then printf '%%s\\n%%s\\n' %s %s | passwd root; else echo 'neither chpasswd nor passwd exists' >&2; exit 127; fi", quotedPassword, quotedPassword, quotedPassword)
}

func buildLXDPasswordCommandCandidates(instanceName, password, preferShell string) []string {
	shells := []string{}
	if preferShell != "" {
		shells = append(shells, preferShell)
	}
	for _, shell := range []string{"sh", "bash"} {
		seen := false
		for _, existing := range shells {
			if existing == shell {
				seen = true
				break
			}
		}
		if !seen {
			shells = append(shells, shell)
		}
	}

	innerCmd := buildLXDPasswordInnerCommand(password)
	commands := make([]string, 0, len(shells)+2)
	for _, shell := range shells {
		commands = append(commands, fmt.Sprintf("lxc exec %s -- %s -c %s", shellSingleQuote(instanceName), shellSingleQuote(shell), shellSingleQuote(innerCmd)))
	}

	// Keep the historical host-pipe method as a compatibility fallback for
	// images with unusual shells. Some older LXD builds handle stdin forwarding
	// differently, so this is deliberately not the primary path anymore.
	commands = append(commands, buildLXDChpasswdCommand(instanceName, password))
	return commands
}

func (l *LXDProvider) setLXDInstancePasswordWithRetry(instanceName, password, preferShell string) error {
	commands := buildLXDPasswordCommandCandidates(instanceName, password, preferShell)
	var lastErr error
	var lastOutput string

	for attempt := 1; attempt <= 4; attempt++ {
		if err := l.ensureLXDInstanceRunningForExec(instanceName); err != nil {
			lastErr = err
			global.APP_LOG.Warn("LXD实例暂不可执行，等待后重试设置密码",
				zap.String("instanceName", utils.TruncateString(instanceName, 32)),
				zap.Int("attempt", attempt),
				zap.Error(err))
		} else {
			for idx, cmd := range commands {
				script := utils.BuildTempScript(utils.TempScriptConfig{
					PrimaryCmd:     cmd,
					FallbackCmd:    cmd,
					TimeoutSeconds: 60,
					SuccessMarker:  "PASSWORD_OK",
				})
				output, err := l.sshClient.ExecuteViaTempScript(script, nil, 180*time.Second)
				if err == nil {
					global.APP_LOG.Info("LXD实例密码设置成功",
						zap.String("instanceName", utils.TruncateString(instanceName, 32)),
						zap.Int("attempt", attempt),
						zap.Int("method", idx+1))
					return nil
				}
				lastErr = err
				lastOutput = output
				global.APP_LOG.Warn("LXD实例密码设置命令失败，尝试备用方式",
					zap.String("instanceName", utils.TruncateString(instanceName, 32)),
					zap.Int("attempt", attempt),
					zap.Int("method", idx+1),
					zap.String("output", utils.TruncateString(output, 800)),
					zap.Error(err))
			}
		}

		if attempt < 4 {
			time.Sleep(time.Duration(attempt*5) * time.Second)
		}
	}

	if lastOutput != "" {
		return fmt.Errorf("所有LXD密码设置方式均失败: %w; 最后输出: %s", lastErr, utils.TruncateString(lastOutput, 1000))
	}
	if lastErr != nil {
		return fmt.Errorf("所有LXD密码设置方式均失败: %w", lastErr)
	}
	return fmt.Errorf("所有LXD密码设置方式均失败")
}
