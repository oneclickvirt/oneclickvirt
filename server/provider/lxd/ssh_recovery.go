package lxd

import (
	"fmt"
	"strings"

	"oneclickvirt/utils"
)

func (l *LXDProvider) recoverGuestSSHService(instanceName string) (string, error) {
	command := fmt.Sprintf(
		"lxc exec %s -- sh -c %s",
		shellSingleQuote(instanceName),
		shellSingleQuote(utils.BuildGuestSSHRecoveryScript()),
	)
	output, err := l.sshClient.Execute(command)
	if err != nil {
		return output, fmt.Errorf("内置SSH服务恢复命令失败: %w", err)
	}
	if !strings.Contains(output, "ONECLICKVIRT_SSH_READY") {
		return output, fmt.Errorf("内置SSH服务恢复未返回就绪标记")
	}
	return output, nil
}
