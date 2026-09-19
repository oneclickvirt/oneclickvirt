package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"oneclickvirt/utils"
)

// getNATProxyListenIP retains support for LTS daemons, which reject wildcard
// NAT listeners. IPv4-only PortIP does not prevent discovering a host IPv6.
func (i *IncusProvider) getNATProxyListenIP(ctx context.Context, ipv6 bool) (string, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if address, err := utils.ResolveNATProxyListenIP(resolveCtx, ipv6, i.config.PortIP, i.config.Host); err == nil {
		return address, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	i.mu.RLock()
	client := i.sshClient
	i.mu.RUnlock()
	if client != nil && client.HasExecutor() && !strings.EqualFold(strings.TrimSpace(i.config.ExecutionRule), "api_only") {
		// Do not reuse getHostIP here: it can return the same opposite-family
		// PortIP that resolution already rejected, bypassing host discovery.
		command := "ip -o -4 addr show scope global | awk '{print $4}'"
		if ipv6 {
			command = "ip -o -6 addr show scope global | awk '$0 !~ / tentative/ {print $4}'"
		}
		timeout := 10 * time.Second
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
			timeout = time.Until(deadline)
		}
		if timeout <= 0 {
			return "", ctx.Err()
		}
		output, err := client.ExecuteWithTimeout(command, timeout)
		if err == nil {
			candidates := strings.Fields(output)
			for index, candidate := range candidates {
				candidates[index] = strings.SplitN(candidate, "/", 2)[0]
			}
			if address, err := utils.ResolveNATProxyListenIP(ctx, ipv6, candidates...); err == nil {
				return address, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if i.apiClient != nil {
		discoveryCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(discoveryCtx, http.MethodGet, i.apiEndpoint("/1.0"), nil)
		if err != nil {
			return "", err
		}
		resp, err := i.apiClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("读取节点监听地址失败: status %d", resp.StatusCode)
		}
		var result struct {
			Metadata struct {
				Environment struct {
					Addresses []string `json:"addresses"`
				} `json:"environment"`
			} `json:"metadata"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}
		if address, err := utils.ResolveNATProxyListenIP(discoveryCtx, ipv6, result.Metadata.Environment.Addresses...); err == nil {
			return address, nil
		}
	}
	family := 4
	if ipv6 {
		family = 6
	}
	return "", fmt.Errorf("无法获取节点IPv%d监听地址；NAT device proxy不能使用通配监听地址，请配置节点端口映射IP或检查节点网络", family)
}
