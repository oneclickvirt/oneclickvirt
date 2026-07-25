package incus

import (
	"fmt"
	"strings"

	"oneclickvirt/utils"
)

func (i *IncusProvider) recoverGuestSSHService(instanceName string) (string, error) {
	command := fmt.Sprintf(
		"incus exec %s -- sh -c %s",
		shellSingleQuote(instanceName),
		shellSingleQuote(utils.BuildGuestSSHRecoveryScript()),
	)
	output, err := i.sshClient.Execute(command)
	if err != nil {
		return output, fmt.Errorf("内置SSH服务恢复命令失败: %w", err)
	}
	if !strings.Contains(output, "ONECLICKVIRT_SSH_READY") {
		return output, fmt.Errorf("内置SSH服务恢复未返回就绪标记")
	}
	return output, nil
}
