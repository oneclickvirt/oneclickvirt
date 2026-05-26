package provider

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (cs *CertService) AutoConfigureProvider(provider *provider.Provider) error {
	switch provider.Type {
	case "lxd":
		return cs.autoConfigureLXD(provider)
	case "incus":
		return cs.autoConfigureIncus(provider)
	case "proxmox":
		return cs.autoConfigureProxmox(provider)
	default:
		return fmt.Errorf("不支持的Provider类型: %s", provider.Type)
	}
}

func (cs *CertService) AutoConfigureProviderWithStream(provider *provider.Provider, outputChan chan<- string) error {
	return cs.AutoConfigureProviderWithStreamContext(context.Background(), provider, outputChan)
}

func (cs *CertService) AutoConfigureProviderWithStreamContext(ctx context.Context, provider *provider.Provider, outputChan chan<- string) error {
	// 检查context是否已取消
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	switch provider.Type {
	case "lxd":
		return cs.autoConfigureLXDWithStreamContext(ctx, provider, outputChan)
	case "incus":
		return cs.autoConfigureIncusWithStreamContext(ctx, provider, outputChan)
	case "proxmox":
		return cs.autoConfigureProxmoxWithStreamContext(ctx, provider, outputChan)
	default:
		return fmt.Errorf("不支持的Provider类型: %s", provider.Type)
	}
}

func (cs *CertService) autoConfigureLXD(provider *provider.Provider) error {
	global.APP_LOG.Info("开始 LXD 自动配置", zap.String("provider", provider.Name))

	// 1. 生成客户端证书
	certInfo, err := cs.GenerateClientCert(provider.UUID, provider.Name)
	if err != nil {
		return fmt.Errorf("生成客户端证书失败: %w", err)
	}

	// 2. 读取证书内容
	certContent, err := cs.GetCertificateContent(certInfo.CertPath)
	if err != nil {
		return fmt.Errorf("读取证书内容失败: %w", err)
	}

	// 3. 执行配置脚本
	if err := cs.executeScriptViaSFTP(provider, cs.generateLXDScript(provider, certContent), "lxd_config.sh"); err != nil {
		return err
	}

	// 4. 读取私钥内容
	keyContent, err := cs.GetCertificateContent(certInfo.KeyPath)
	if err != nil {
		return fmt.Errorf("读取私钥内容失败: %w", err)
	}

	// 5. 构建API端点
	endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(utils.ExtractHost(provider.Endpoint), "8443"))

	// 6. 创建认证配置
	configService := &ProviderConfigService{}
	authConfig := configService.CreateAuthConfigFromCertInfo(provider, &CertInfo{
		CertPath:        certInfo.CertPath,
		KeyPath:         certInfo.KeyPath,
		CertFingerprint: certInfo.CertFingerprint,
		CertContent:     certContent,
		KeyContent:      keyContent,
	}, endpoint)

	// 7. 保存配置到数据库和文件
	return configService.SaveProviderConfig(provider, authConfig)
}

func (cs *CertService) autoConfigureIncus(provider *provider.Provider) error {
	global.APP_LOG.Info("开始 Incus 自动配置", zap.String("provider", provider.Name))

	// 1. 生成客户端证书
	certInfo, err := cs.GenerateClientCert(provider.UUID, provider.Name)
	if err != nil {
		return fmt.Errorf("生成客户端证书失败: %w", err)
	}

	// 2. 读取证书内容
	certContent, err := cs.GetCertificateContent(certInfo.CertPath)
	if err != nil {
		return fmt.Errorf("读取证书内容失败: %w", err)
	}

	// 3. 执行配置脚本
	if err := cs.executeScriptViaSFTP(provider, cs.generateIncusScript(provider, certContent), "incus_config.sh"); err != nil {
		return err
	}

	// 4. 读取私钥内容
	keyContent, err := cs.GetCertificateContent(certInfo.KeyPath)
	if err != nil {
		return fmt.Errorf("读取私钥内容失败: %w", err)
	}

	// 5. 构建API端点
	endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(utils.ExtractHost(provider.Endpoint), "8443"))

	// 6. 创建认证配置
	configService := &ProviderConfigService{}
	authConfig := configService.CreateAuthConfigFromCertInfo(provider, &CertInfo{
		CertPath:        certInfo.CertPath,
		KeyPath:         certInfo.KeyPath,
		CertFingerprint: certInfo.CertFingerprint,
		CertContent:     certContent,
		KeyContent:      keyContent,
	}, endpoint)

	// 7. 保存配置到数据库和文件
	return configService.SaveProviderConfig(provider, authConfig)
}

func (cs *CertService) autoConfigureProxmox(provider *provider.Provider) error {
	global.APP_LOG.Info("开始 Proxmox VE 自动配置", zap.String("provider", provider.Name))

	// 1. 执行配置脚本
	username := "oneclickvirt"
	tokenId := fmt.Sprintf("oneclickvirt-token-%s", provider.UUID[:8])
	if err := cs.executeScriptViaSFTP(provider, cs.generateProxmoxScript(provider.UUID, username, tokenId), "proxmox_config.sh"); err != nil {
		return err
	}

	// 2. 获取Token信息
	tokenInfo, err := cs.getProxmoxTokenFromRemote(provider, username, tokenId)
	if err != nil {
		global.APP_LOG.Warn("无法获取Proxmox Token信息，但配置可能已成功",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil
	}

	// 3. 构建API端点
	endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(utils.ExtractHost(provider.Endpoint), "8006"))

	// 4. 创建认证配置
	configService := &ProviderConfigService{}
	authConfig := configService.CreateAuthConfigFromTokenInfo(provider, tokenInfo, endpoint)

	// 5. 保存配置到数据库和文件
	return configService.SaveProviderConfig(provider, authConfig)
}

func (cs *CertService) autoConfigureLXDWithStream(provider *provider.Provider, outputChan chan<- string) error {
	outputChan <- "第1步: 生成客户端证书"
	certInfo, err := cs.GenerateClientCert(provider.UUID, provider.Name)
	if err != nil {
		outputChan <- fmt.Sprintf("❌ 生成客户端证书失败: %s", err.Error())
		return fmt.Errorf("生成客户端证书失败: %w", err)
	}
	outputChan <- "✅ 客户端证书生成成功"

	outputChan <- "第2步: 读取证书内容"
	certContent, err := cs.GetCertificateContent(certInfo.CertPath)
	if err != nil {
		outputChan <- fmt.Sprintf("❌ 读取证书内容失败: %s", err.Error())
		return fmt.Errorf("读取证书内容失败: %w", err)
	}
	outputChan <- "✅ 证书内容读取成功"

	outputChan <- "第3步: 执行LXD配置脚本"
	if err := cs.executeScriptViaSFTPWithStream(provider, cs.generateLXDScript(provider, certContent), "lxd_config.sh", outputChan); err != nil {
		return err
	}

	outputChan <- "第4步: 读取私钥内容"
	keyContent, err := cs.GetCertificateContent(certInfo.KeyPath)
	if err != nil {
		outputChan <- fmt.Sprintf("❌ 读取私钥内容失败: %s", err.Error())
		return fmt.Errorf("读取私钥内容失败: %w", err)
	}
	outputChan <- "✅ 私钥内容读取成功"

	outputChan <- "第5步: 保存配置到数据库和文件"
	endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(utils.ExtractHost(provider.Endpoint), "8443"))
	configService := &ProviderConfigService{}
	authConfig := configService.CreateAuthConfigFromCertInfo(provider, &CertInfo{
		CertPath:        certInfo.CertPath,
		KeyPath:         certInfo.KeyPath,
		CertFingerprint: certInfo.CertFingerprint,
		CertContent:     certContent,
		KeyContent:      keyContent,
	}, endpoint)

	if err := configService.SaveProviderConfig(provider, authConfig); err != nil {
		outputChan <- fmt.Sprintf("❌ 保存配置失败: %s", err.Error())
		return err
	}
	outputChan <- "✅ 配置保存成功"

	return nil
}

func (cs *CertService) autoConfigureIncusWithStream(provider *provider.Provider, outputChan chan<- string) error {
	outputChan <- "第1步: 生成客户端证书"
	certInfo, err := cs.GenerateClientCert(provider.UUID, provider.Name)
	if err != nil {
		outputChan <- fmt.Sprintf("❌ 生成客户端证书失败: %s", err.Error())
		return fmt.Errorf("生成客户端证书失败: %w", err)
	}
	outputChan <- "✅ 客户端证书生成成功"

	outputChan <- "第2步: 读取证书内容"
	certContent, err := cs.GetCertificateContent(certInfo.CertPath)
	if err != nil {
		outputChan <- fmt.Sprintf("❌ 读取证书内容失败: %s", err.Error())
		return fmt.Errorf("读取证书内容失败: %w", err)
	}
	outputChan <- "✅ 证书内容读取成功"

	outputChan <- "第3步: 执行Incus配置脚本"
	if err := cs.executeScriptViaSFTPWithStream(provider, cs.generateIncusScript(provider, certContent), "incus_config.sh", outputChan); err != nil {
		return err
	}

	outputChan <- "第4步: 读取私钥内容"
	keyContent, err := cs.GetCertificateContent(certInfo.KeyPath)
	if err != nil {
		outputChan <- fmt.Sprintf("❌ 读取私钥内容失败: %s", err.Error())
		return fmt.Errorf("读取私钥内容失败: %w", err)
	}
	outputChan <- "✅ 私钥内容读取成功"

	outputChan <- "第5步: 保存配置到数据库和文件"
	endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(utils.ExtractHost(provider.Endpoint), "8443"))
	configService := &ProviderConfigService{}
	authConfig := configService.CreateAuthConfigFromCertInfo(provider, &CertInfo{
		CertPath:        certInfo.CertPath,
		KeyPath:         certInfo.KeyPath,
		CertFingerprint: certInfo.CertFingerprint,
		CertContent:     certContent,
		KeyContent:      keyContent,
	}, endpoint)

	if err := configService.SaveProviderConfig(provider, authConfig); err != nil {
		outputChan <- fmt.Sprintf("❌ 保存配置失败: %s", err.Error())
		return err
	}
	outputChan <- "✅ 配置保存成功"

	return nil
}

func (cs *CertService) autoConfigureProxmoxWithStream(provider *provider.Provider, outputChan chan<- string) error {
	outputChan <- "第1步: 准备Proxmox配置"
	username := "oneclickvirt"
	tokenId := fmt.Sprintf("oneclickvirt-token-%s", provider.UUID[:8])
	outputChan <- fmt.Sprintf("用户名: %s", username)
	outputChan <- fmt.Sprintf("Token ID: %s", tokenId)

	outputChan <- "第2步: 执行Proxmox配置脚本"
	if err := cs.executeScriptViaSFTPWithStream(provider, cs.generateProxmoxScript(provider.UUID, username, tokenId), "proxmox_config.sh", outputChan); err != nil {
		return err
	}

	outputChan <- "第3步: 获取生成的Token信息"
	tokenInfo, err := cs.getProxmoxTokenFromRemote(provider, username, tokenId)
	if err != nil {
		outputChan <- fmt.Sprintf("⚠️ 无法获取Token信息，但配置可能已成功: %s", err.Error())
		return nil
	}
	outputChan <- fmt.Sprintf("✅ Token信息获取成功: %s", tokenInfo.TokenID)

	outputChan <- "第4步: 保存配置到数据库和文件"
	endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(utils.ExtractHost(provider.Endpoint), "8006"))
	configService := &ProviderConfigService{}
	authConfig := configService.CreateAuthConfigFromTokenInfo(provider, tokenInfo, endpoint)

	if err := configService.SaveProviderConfig(provider, authConfig); err != nil {
		outputChan <- fmt.Sprintf("❌ 保存配置失败: %s", err.Error())
		return err
	}
	outputChan <- "✅ 配置保存成功"

	outputChan <- "✅ Proxmox VE 自动配置完成"
	return nil
}

func (cs *CertService) executeScriptViaSFTP(provider *provider.Provider, script, filename string) error {
	host, port := utils.ParseEndpoint(provider.Endpoint, provider.SSHPort)
	sshConfig := utils.SSHConfig{
		Host:           host,
		Port:           port,
		Username:       provider.Username,
		Password:       provider.Password,
		PrivateKey:     provider.SSHKey,
		ConnectTimeout: 10 * time.Second,
		ExecuteTimeout: 300 * time.Second,
	}

	sshClient, err := utils.NewSSHClient(sshConfig)
	if err != nil {
		return fmt.Errorf("SSH连接失败: %w", err)
	}
	defer sshClient.Close()

	remotePath := fmt.Sprintf("/tmp/%s", filename)

	if err := sshClient.UploadContent(script, remotePath, 0755); err != nil {
		return fmt.Errorf("上传脚本失败: %w", err)
	}

	// 使用带日志记录的执行方法处理复杂命令
	executeCommand := fmt.Sprintf("chmod +x %s && %s", remotePath, remotePath)
	_, err = sshClient.ExecuteWithLogging(executeCommand, "CERT_SCRIPT")
	if err != nil {
		return fmt.Errorf("执行脚本失败: %w", err)
	}

	// 清理临时文件
	sshClient.Execute(fmt.Sprintf("rm -f %s", remotePath))
	return nil
}

func (cs *CertService) executeScriptViaSFTPWithStream(provider *provider.Provider, script, filename string, outputChan chan<- string) error {
	host, port := utils.ParseEndpoint(provider.Endpoint, provider.SSHPort)
	sshConfig := utils.SSHConfig{
		Host:           host,
		Port:           port,
		Username:       provider.Username,
		Password:       provider.Password,
		PrivateKey:     provider.SSHKey,
		ConnectTimeout: 10 * time.Second,
		ExecuteTimeout: 300 * time.Second,
	}

	sshClient, err := utils.NewSSHClient(sshConfig)
	if err != nil {
		outputChan <- fmt.Sprintf("❌ SSH连接失败: %s", err.Error())
		return fmt.Errorf("SSH连接失败: %w", err)
	}
	defer sshClient.Close()

	remotePath := fmt.Sprintf("/tmp/%s", filename)

	outputChan <- "上传配置脚本..."
	// 先尝试直接上传，如果权限被拒绝，则尝试上传到用户目录再移动
	err = sshClient.UploadContent(script, remotePath, 0755)
	if err != nil && strings.Contains(err.Error(), "permission denied") {
		// 如果直接上传/tmp失败，尝试上传到用户home目录
		userRemotePath := fmt.Sprintf("~/%s", filename)
		outputChan <- fmt.Sprintf("⚠️ /tmp目录权限不足，尝试上传到用户目录: %s", userRemotePath)

		if err := sshClient.UploadContent(script, userRemotePath, 0755); err != nil {
			outputChan <- fmt.Sprintf("❌ 上传脚本失败: %s", err.Error())
			return fmt.Errorf("上传脚本失败: %w", err)
		}

		// 使用sudo移动到/tmp
		moveCmd := fmt.Sprintf("sudo mv %s %s && sudo chmod 755 %s", userRemotePath, remotePath, remotePath)
		if _, err := sshClient.Execute(moveCmd); err != nil {
			outputChan <- fmt.Sprintf("❌ 移动脚本到/tmp失败: %s", err.Error())
			// 如果移动失败，直接使用用户目录的脚本
			remotePath = userRemotePath
		} else {
			outputChan <- "✅ 脚本已移动到/tmp目录"
		}
	} else if err != nil {
		outputChan <- fmt.Sprintf("❌ 上传脚本失败: %s", err.Error())
		return fmt.Errorf("上传脚本失败: %w", err)
	}
	outputChan <- "✅ 脚本上传成功"

	outputChan <- "执行配置脚本..."
	executeCommand := fmt.Sprintf("chmod +x %s && %s", remotePath, remotePath)
	output, err := sshClient.ExecuteWithLogging(executeCommand, "CERT_SCRIPT_STREAM")

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			outputChan <- line
		}
	}

	if err != nil {
		outputChan <- fmt.Sprintf("❌ 脚本执行失败: %s", err.Error())
		return fmt.Errorf("执行脚本失败: %w", err)
	}

	outputChan <- "✅ 配置脚本执行完成"
	sshClient.Execute(fmt.Sprintf("rm -f %s", remotePath))
	return nil
}

func (cs *CertService) getProxmoxTokenFromRemote(provider *provider.Provider, username, tokenId string) (*TokenInfo, error) {
	host, port := utils.ParseEndpoint(provider.Endpoint, provider.SSHPort)
	sshConfig := utils.SSHConfig{
		Host:           host,
		Port:           port,
		Username:       provider.Username,
		Password:       provider.Password,
		PrivateKey:     provider.SSHKey,
		ConnectTimeout: 12 * time.Second,
		ExecuteTimeout: 60 * time.Second,
	}

	sshClient, err := utils.NewSSHClient(sshConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH连接失败: %w", err)
	}
	defer sshClient.Close()

	output, err := sshClient.Execute("cat /tmp/oneclickvirt-proxmox-config 2>/dev/null || echo 'FILE_NOT_FOUND'")
	if err != nil || strings.Contains(output, "FILE_NOT_FOUND") {
		return nil, fmt.Errorf("无法读取配置文件")
	}

	lines := strings.Split(output, "\n")
	var tokenID, tokenSecret string

	for _, line := range lines {
		if strings.HasPrefix(line, "TOKEN_ID=") {
			tokenID = strings.TrimPrefix(line, "TOKEN_ID=")
		}
		if strings.HasPrefix(line, "TOKEN_SECRET=") {
			tokenSecret = strings.TrimPrefix(line, "TOKEN_SECRET=")
		}
	}

	if tokenID == "" || tokenSecret == "" {
		return nil, fmt.Errorf("无法解析Token信息")
	}

	return &TokenInfo{
		TokenID:     tokenID,
		TokenSecret: tokenSecret,
		Username:    username,
	}, nil
}
