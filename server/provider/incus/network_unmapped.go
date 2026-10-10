package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// The create path has already started the guest. Without node port mappings,
// no proxy devices need a stop/start cycle, including for copied GPU containers.
func (i *IncusProvider) configureUnmappedInstanceNetwork(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig) error {
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := "incus query " + shellSingleQuote("/1.0/instances/"+url.PathEscape(config.Name)+"/state")
	instanceIP := ""
	for instanceIP == "" {
		output, err := utils.ExecuteShellCommandContext(readyCtx, i.sshClient, command)
		if err != nil {
			return fmt.Errorf("读取实例网络状态失败: %w", err)
		}
		var state map[string]interface{}
		if err := json.Unmarshal([]byte(utils.StripTerminalCSI(utils.CleanCommandOutput(output))), &state); err != nil {
			return fmt.Errorf("实例网络状态响应无效: %w", err)
		}
		if metadata, ok := state["metadata"].(map[string]interface{}); ok {
			state = metadata
		}
		status, _ := state["status"].(string)
		if !strings.EqualFold(status, "Running") {
			return fmt.Errorf("实例未运行，无法配置网络: %s", status)
		}
		instanceIP = i.apiInstanceIPv4(state)
		if instanceIP == "" {
			if err := utils.SleepContext(readyCtx, 2*time.Second); err != nil {
				return fmt.Errorf("等待实例IPv4地址失败: %w", err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := i.configureNetworkLimits(config.Name, networkConfig); err != nil {
		global.APP_LOG.Warn("配置网络限速失败", zap.String("instanceName", config.Name), zap.Error(err))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep the tunnel target stable without changing the template or its profile.
	if err := i.setIPAddressBinding(config.Name, instanceIP); err != nil {
		global.APP_LOG.Warn("设置IP地址绑定失败", zap.String("instanceName", config.Name), zap.Error(err))
	}
	return ctx.Err()
}
