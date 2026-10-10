package provider

import (
	"fmt"

	"oneclickvirt/utils"
)

// ValidateContainerCreatePath keeps shell-only options out of the image API
// path, which cannot copy an existing container or apply GPU devices.
func ValidateContainerCreatePath(config InstanceConfig, shellAvailable bool, platform string) (bool, error) {
	if config.CopyMode {
		if config.InstanceType != "container" {
			return false, fmt.Errorf("复制模式仅支持容器实例")
		}
		if !utils.IsValidLXDInstanceName(config.CopySourceName) {
			return false, fmt.Errorf("请选择有效的源容器")
		}
		if config.CopySourceName == config.Name {
			return false, fmt.Errorf("源容器和副本不能使用相同名称")
		}
	}
	if config.GpuEnabled && config.InstanceType != "container" {
		return false, fmt.Errorf("GPU直通仅支持容器实例")
	}
	requiresShell := config.CopyMode || config.GpuEnabled
	if requiresShell && !shellAvailable {
		return false, fmt.Errorf("%s容器复制和GPU直通需要 Agent 或 SSH 连接，请将节点执行方式改为自动或仅 SSH", platform)
	}
	return requiresShell, nil
}
