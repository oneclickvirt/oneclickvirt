package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ProviderHealthChecker 为现有service层提供的健康检查工具
type ProviderHealthChecker struct {
	manager *HealthManager
	logger  *zap.Logger
}

// NewProviderHealthChecker 创建provider健康检查工具
func NewProviderHealthChecker(logger *zap.Logger) *ProviderHealthChecker {
	return &ProviderHealthChecker{
		manager: NewHealthManager(logger),
		logger:  logger,
	}
}

// ProviderAuthConfig 认证配置接口，避免循环导入
type ProviderAuthConfig interface {
	GetType() string
	GetCertificate() CertificateInfo
	GetToken() TokenInfo
}

// CertificateInfo 证书信息接口
type CertificateInfo interface {
	GetCertPath() string
	GetKeyPath() string
	GetCertContent() string
	GetKeyContent() string
}

// TokenInfo Token信息接口
type TokenInfo interface {
	GetTokenID() string
	GetTokenSecret() string
}

// CheckProviderHealthWithAuthConfig 根据认证配置执行健康检查
// 返回: sshStatus, apiStatus, hostName, error
func (phc *ProviderHealthChecker) CheckProviderHealthWithAuthConfig(ctx context.Context, providerID uint, providerName, providerType, host, username, password, privateKey string, port int, authConfig ProviderAuthConfig) (string, string, string, error) {
	// 复制副本避免共享状态，立即创建所有参数的本地副本
	localProviderID := providerID
	localProviderName := providerName
	localProviderType := providerType
	localHost := host
	localUsername := username
	localPassword := password
	localPrivateKey := privateKey
	localPort := port

	// 添加入口日志，追踪参数
	if phc.logger != nil {
		phc.logger.Debug("CheckProviderHealthWithAuthConfig 调用",
			zap.Uint("providerID", localProviderID),
			zap.String("providerName", localProviderName),
			zap.String("providerType", localProviderType),
			zap.String("host", localHost),
			zap.Int("port", localPort),
			zap.String("username", localUsername))
	}

	config := HealthConfig{
		ProviderID:    localProviderID,
		ProviderName:  localProviderName,
		Host:          localHost,
		Port:          localPort,
		Username:      localUsername,
		Password:      localPassword,
		PrivateKey:    localPrivateKey,
		SSHEnabled:    true,
		APIEnabled:    true,
		SkipTLSVerify: true,
		Timeout:       30 * time.Second,
	}

	// 根据认证配置设置具体的认证信息
	switch localProviderType {
	case "lxd", "incus":
		cert := authConfig.GetCertificate()
		if cert != nil {
			config.APIPort = 8443
			config.APIScheme = "https"
			config.CertPath = cert.GetCertPath()
			config.KeyPath = cert.GetKeyPath()
			config.CertContent = cert.GetCertContent()
			config.KeyContent = cert.GetKeyContent()
		}
		config.ServiceChecks = []string{localProviderType}
	case "proxmox":
		token := authConfig.GetToken()
		if token != nil {
			config.APIPort = 8006
			config.APIScheme = "https"
			config.Token = token.GetTokenSecret()
			config.TokenID = token.GetTokenID()
		}
		config.ServiceChecks = []string{"pvestatd", "pvedaemon", "pveproxy"}
	case "docker", "orbstack":
		config.APIEnabled = false // docker默认不测API
		config.APIPort = 2375
		config.APIScheme = "http"
		config.ServiceChecks = []string{"docker"}
	}

	// 创建checker前再次记录配置，确保config.Host正确
	if phc.logger != nil {
		phc.logger.Debug("准备创建HealthChecker",
			zap.Uint("providerID", localProviderID),
			zap.String("providerName", localProviderName),
			zap.String("providerType", localProviderType),
			zap.String("config.Host", config.Host),
			zap.Int("config.Port", config.Port))
	}

	checker, err := phc.manager.CreateChecker(ProviderType(localProviderType), config)
	if err != nil {
		return "offline", "offline", "", fmt.Errorf("failed to create health checker: %w", err)
	}

	result, err := checker.CheckHealth(ctx)
	if err != nil {
		return "offline", "offline", "", err
	}

	// 确保释放资源
	switch c := checker.(type) {
	case *DockerHealthChecker:
		c.Close()
	case *LXDHealthChecker:
		c.Close()
	case *IncusHealthChecker:
		c.Close()
	case *ProxmoxHealthChecker:
		c.Close()
	}

	sshStatus := "unknown"
	apiStatus := "unknown"
	hostName := ""
	if result.SSHStatus != "" {
		sshStatus = result.SSHStatus
	}
	if result.APIStatus != "" {
		apiStatus = result.APIStatus
	}
	if result.HostName != "" {
		hostName = result.HostName
	}
	return sshStatus, apiStatus, hostName, nil
}

// CheckProviderHealthWithAuthConfig 根据认证配置执行健康检查

// CheckProviderHealthFromConfig 根据provider配置信息执行健康检查
func (phc *ProviderHealthChecker) CheckProviderHealthFromConfig(ctx context.Context, providerType, host, username, password string, port int) (string, string, error) {
	// 创建健康检查配置
	config := HealthConfig{
		Host:          host,
		Port:          port,
		Username:      username,
		Password:      password,
		SSHEnabled:    true,
		APIEnabled:    true,
		SkipTLSVerify: true, // 默认跳过TLS验证
		Timeout:       30 * time.Second,
	}
	switch providerType {
	case "docker", "orbstack":
		config.APIEnabled = false // docker默认不测API
		config.APIPort = 2375
		config.APIScheme = "http"
		config.ServiceChecks = []string{"docker"}
	case "lxd":
		config.APIPort = 8443
		config.APIScheme = "https"
		config.ServiceChecks = []string{"lxd"}
	case "incus":
		config.APIPort = 8443
		config.APIScheme = "https"
		config.ServiceChecks = []string{"incus"}
	case "proxmox":
		config.APIPort = 8006
		config.APIScheme = "https"
		config.ServiceChecks = []string{"pvestatd", "pvedaemon", "pveproxy"}
	}
	checker, err := phc.manager.CreateChecker(ProviderType(providerType), config)
	if err != nil {
		return "offline", "offline", fmt.Errorf("failed to create health checker: %w", err)
	}
	result, err := checker.CheckHealth(ctx)
	if err != nil {
		return "offline", "offline", err
	}
	switch c := checker.(type) {
	case *DockerHealthChecker:
		c.Close()
	case *LXDHealthChecker:
		c.Close()
	case *IncusHealthChecker:
		c.Close()
	case *ProxmoxHealthChecker:
		c.Close()
	}
	sshStatus := "unknown"
	apiStatus := "unknown"
	if result.SSHStatus != "" {
		sshStatus = result.SSHStatus
	}
	if result.APIStatus != "" {
		apiStatus = result.APIStatus
	}
	return sshStatus, apiStatus, nil
}

// CheckSSHConnection 单独检查SSH连接
func (phc *ProviderHealthChecker) CheckSSHConnection(ctx context.Context, providerID uint, providerName, host, username, password, privateKey string, port int) error {
	// 复制副本避免共享状态，立即创建所有参数的本地副本
	localProviderID := providerID
	localProviderName := providerName
	localHost := host
	localUsername := username
	localPassword := password
	localPrivateKey := privateKey
	localPort := port

	config := HealthConfig{
		ProviderID:   localProviderID,
		ProviderName: localProviderName,
		Host:         localHost,
		Port:         localPort,
		Username:     localUsername,
		Password:     localPassword,
		PrivateKey:   localPrivateKey,
		SSHEnabled:   true,
		APIEnabled:   false,
		Timeout:      30 * time.Second,
	}
	checker := NewDockerHealthChecker(config, phc.logger)
	defer checker.Close()
	result, err := checker.CheckHealth(ctx)
	if err != nil {
		return err
	}
	if result.SSHStatus == "offline" {
		return fmt.Errorf("SSH connection failed")
	}
	return nil
}

// CheckAPIConnection 单独检查API连接
func (phc *ProviderHealthChecker) CheckAPIConnection(ctx context.Context, providerType, host string, port int, token, tokenID string) error {
	config := HealthConfig{
		Host:          host,
		Port:          22, // 这里仍然使用默认值，因为API连接不需要SSH端口
		SSHEnabled:    false,
		APIEnabled:    true,
		APIPort:       port,
		SkipTLSVerify: true, // 默认跳过TLS验证
		Token:         token,
		TokenID:       tokenID,
		Timeout:       30 * time.Second,
	}
	switch providerType {
	case "docker", "orbstack":
		config.APIScheme = "http"
	case "lxd", "incus":
		config.APIScheme = "https"
	case "proxmox":
		config.APIScheme = "https"
	default:
		return fmt.Errorf("unsupported provider type: %s", providerType)
	}
	checker, err := phc.manager.CreateChecker(ProviderType(providerType), config)
	if err != nil {
		return fmt.Errorf("failed to create health checker: %w", err)
	}
	defer func() {
		switch c := checker.(type) {
		case *DockerHealthChecker:
			c.Close()
		case *LXDHealthChecker:
			c.Close()
		case *IncusHealthChecker:
			c.Close()
		case *ProxmoxHealthChecker:
			c.Close()
		}
	}()
	result, err := checker.CheckHealth(ctx)
	if err != nil {
		return err
	}
	if result.APIStatus == "offline" {
		// 尝试从结果中获取更详细的错误信息
		if len(result.Details) > 0 {
			if apiDetail, exists := result.Details[string(CheckTypeAPI)]; exists {
				if checkResult, ok := apiDetail.(CheckResult); ok && checkResult.Error != "" {
					return fmt.Errorf("API connection failed: %s", checkResult.Error)
				}
			}
		}
		// 如果找不到详细错误信息，检查 Errors 列表
		if len(result.Errors) > 0 {
			for _, errMsg := range result.Errors {
				if strings.Contains(errMsg, "api") || strings.Contains(errMsg, "API") {
					return fmt.Errorf("API connection failed: %s", errMsg)
				}
			}
			// 如果没有API相关错误，返回第一个错误
			return fmt.Errorf("API connection failed: %s", result.Errors[0])
		}
		return fmt.Errorf("API connection failed")
	}
	return nil
}
