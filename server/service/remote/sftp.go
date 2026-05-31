package remote

import (
	"fmt"
	"net"
	"path"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SSHAccessTarget struct {
	ProviderID     uint
	Host           string
	Port           int
	Username       string
	Password       string
	PrivateKey     string
	UseAgentTunnel bool
}

func providerPortHost(provider *providerModel.Provider) string {
	if provider == nil {
		return ""
	}
	if strings.TrimSpace(provider.PortIP) != "" {
		return utils.ExtractHost(provider.PortIP)
	}
	return utils.ExtractHost(provider.Endpoint)
}

func NormalizeRemotePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	normalized := path.Clean(trimmed)
	if normalized == "" {
		return "/"
	}
	return normalized
}

func ResolveControllerTargetHost(port *providerModel.Port, instance *providerModel.Instance) string {
	if port == nil || instance == nil {
		return ""
	}

	targetHost, shouldUpdate := agentService.ResolveControllerPortTarget(port.InternalHost, instance.PrivateIP)
	if shouldUpdate {
		global.APP_DB.Model(&providerModel.Port{}).
			Where("id = ?", port.ID).
			Update("internal_host", targetHost)
	}

	return targetHost
}

func ResolveInstanceSSHTarget(instance *providerModel.Instance) (*SSHAccessTarget, error) {
	if instance == nil {
		return nil, fmt.Errorf("instance is nil")
	}
	if strings.TrimSpace(instance.Username) == "" {
		return nil, fmt.Errorf("实例缺少 SSH 用户名")
	}
	if strings.TrimSpace(instance.Password) == "" {
		return nil, fmt.Errorf("实例缺少 SSH 密码，无法建立远程连接")
	}

	target := &SSHAccessTarget{
		ProviderID: instance.ProviderID,
		Username:   instance.Username,
		Password:   instance.Password,
	}

	var provider providerModel.Provider
	hasProvider := false
	if err := global.APP_DB.Select("id", "connection_type", "endpoint", "port_ip").
		Where("id = ?", instance.ProviderID).
		First(&provider).Error; err == nil {
		hasProvider = true
	}

	var sshPortMapping providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instance.ID).First(&sshPortMapping).Error; err == nil {
		if sshPortMapping.MappingType == "controller" {
			target.UseAgentTunnel = true
			target.Host = ResolveControllerTargetHost(&sshPortMapping, instance)
			target.Port = sshPortMapping.GuestPort
			if target.Port == 0 {
				target.Port = 22
			}
		} else {
			if hasProvider {
				target.Host = providerPortHost(&provider)
			}
			if strings.TrimSpace(target.Host) == "" {
				if instance.PublicIP != "" {
					target.Host = instance.PublicIP
				} else {
					target.Host = instance.PrivateIP
				}
			}
			target.Port = sshPortMapping.HostPort
		}
	} else {
		if instance.PublicIP != "" {
			target.Host = instance.PublicIP
		} else if instance.PrivateIP != "" {
			target.Host = instance.PrivateIP
			if hasProvider && provider.ConnectionType == "agent" {
				target.UseAgentTunnel = true
			}
		}
		target.Port = instance.SSHPort
		if target.Port == 0 {
			target.Port = 22
		}
	}

	if strings.TrimSpace(target.Host) == "" {
		return nil, fmt.Errorf("实例没有可用的 SSH 主机地址")
	}
	if target.Port <= 0 {
		target.Port = 22
	}

	return target, nil
}

func ResolveProviderSSHTarget(provider *providerModel.Provider) (*SSHAccessTarget, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	if strings.TrimSpace(provider.Username) == "" {
		return nil, fmt.Errorf("节点缺少 SSH 用户名")
	}
	if strings.TrimSpace(provider.Password) == "" && strings.TrimSpace(provider.SSHKey) == "" {
		return nil, fmt.Errorf("节点缺少 SSH 凭据（密码或密钥）")
	}

	target := &SSHAccessTarget{
		ProviderID: provider.ID,
		Username:   provider.Username,
		Password:   provider.Password,
		PrivateKey: provider.SSHKey,
	}

	if provider.ConnectionType == "agent" {
		target.UseAgentTunnel = true
		target.Host = "127.0.0.1"
		target.Port = provider.SSHPort
		if target.Port == 0 {
			target.Port = 22
		}
	} else {
		target.Host = providerPortHost(provider)
		target.Port = provider.SSHPort
		if target.Port == 0 {
			target.Port = 22
		}
	}

	if strings.TrimSpace(target.Host) == "" {
		return nil, fmt.Errorf("节点缺少可用的 SSH 主机地址")
	}

	return target, nil
}

func buildSSHClientConfig(target *SSHAccessTarget, timeout time.Duration) (*ssh.ClientConfig, error) {
	if target == nil {
		return nil, fmt.Errorf("target is nil")
	}

	authMethods := make([]ssh.AuthMethod, 0, 2)
	if strings.TrimSpace(target.PrivateKey) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(target.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("解析 SSH 私钥失败: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if strings.TrimSpace(target.Password) != "" {
		authMethods = append(authMethods, ssh.Password(target.Password))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("缺少可用的 SSH 凭据")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	return &ssh.ClientConfig{
		User:            target.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}, nil
}

func OpenSSHClient(target *SSHAccessTarget) (*ssh.Client, error) {
	if target == nil {
		return nil, fmt.Errorf("target is nil")
	}

	sshConfig, err := buildSSHClientConfig(target, 20*time.Second)
	if err != nil {
		return nil, err
	}

	if target.UseAgentTunnel {
		tunnelConn, err := agentService.OpenTunnelConn(target.ProviderID, target.Host, target.Port)
		if err != nil {
			return nil, fmt.Errorf("agent 隧道建立失败: %w", err)
		}

		sshConn, chans, reqs, err := ssh.NewClientConn(tunnelConn, fmt.Sprintf("%s:%d", target.Host, target.Port), sshConfig)
		if err != nil {
			tunnelConn.Close()
			return nil, fmt.Errorf("通过 agent 隧道建立 SSH 连接失败: %w", err)
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}

	address := net.JoinHostPort(target.Host, fmt.Sprintf("%d", target.Port))
	client, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("建立 SSH 连接失败: %w", err)
	}
	return client, nil
}

func OpenSFTPClient(target *SSHAccessTarget) (*sftp.Client, func(), error) {
	sshClient, err := OpenSSHClient(target)
	if err != nil {
		return nil, nil, err
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, nil, fmt.Errorf("创建 SFTP 客户端失败: %w", err)
	}

	cleanup := func() {
		_ = sftpClient.Close()
		_ = sshClient.Close()
	}

	return sftpClient, cleanup, nil
}
