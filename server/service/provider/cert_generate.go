package provider

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)


type CertService struct{}

type CertInfo struct {
	CertPath        string `json:"certPath"`
	KeyPath         string `json:"keyPath"`
	CACertPath      string `json:"caCertPath"`
	CertFingerprint string `json:"certFingerprint"`
	CertContent     string `json:"certContent,omitempty"`
	KeyContent      string `json:"keyContent,omitempty"`
}

type TokenInfo struct {
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
	Username    string `json:"username"`
	Command     string `json:"command"`
}

type ConfigStep struct {
	Description   string `json:"description"`
	Command       string `json:"command"`
	IgnoreFailure bool   `json:"ignoreFailure"`
	RetryCount    int    `json:"retryCount"`
	SleepBefore   int    `json:"sleepBefore"`
}

func (cs *CertService) GenerateClientCert(providerUUID, providerName string) (*CertInfo, error) {
	global.APP_LOG.Info("开始生成客户端证书",
		zap.String("providerUUID", providerUUID),
		zap.String("providerName", providerName))

	certsDir := "certs"
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		global.APP_LOG.Error("创建证书目录失败",
			zap.String("dir", certsDir),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, fmt.Errorf("创建证书目录失败: %w", err)
	}

	global.APP_LOG.Debug("开始生成RSA私钥")
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		global.APP_LOG.Error("生成私钥失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, fmt.Errorf("生成私钥失败: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("oneclickvirt-%s", providerUUID),
			Organization: []string{"OneClickVirt"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	global.APP_LOG.Debug("开始创建X.509证书")
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		global.APP_LOG.Error("生成证书失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, fmt.Errorf("生成证书失败: %w", err)
	}

	certPath := filepath.Join(certsDir, fmt.Sprintf("%s.crt", providerUUID))
	certFile, err := os.Create(certPath)
	if err != nil {
		global.APP_LOG.Error("创建证书文件失败",
			zap.String("certPath", certPath),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, fmt.Errorf("创建证书文件失败: %w", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		global.APP_LOG.Error("写入证书文件失败",
			zap.String("certPath", certPath),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, fmt.Errorf("写入证书文件失败: %w", err)
	}

	keyPath := filepath.Join(certsDir, fmt.Sprintf("%s.key", providerUUID))
	keyFile, err := os.Create(keyPath)
	if err != nil {
		return nil, fmt.Errorf("创建私钥文件失败: %w", err)
	}
	defer keyFile.Close()

	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("序列化私钥失败: %w", err)
	}

	if err := pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}); err != nil {
		return nil, fmt.Errorf("写入私钥文件失败: %w", err)
	}

	// 生成证书指纹（使用SHA256哈希的前64个字符，确保不超过数据库字段长度）
	hash := sha256.Sum256(certDER)
	fingerprint := fmt.Sprintf("%x", hash)[:64] // 取SHA256哈希的前64个字符

	global.APP_LOG.Info("生成客户端证书成功",
		zap.String("providerUUID", providerUUID),
		zap.String("providerName", providerName),
		zap.String("certPath", utils.TruncateString(certPath, 100)),
		zap.String("keyPath", utils.TruncateString(keyPath, 100)))

	return &CertInfo{
		CertPath:        certPath,
		KeyPath:         keyPath,
		CertFingerprint: fingerprint,
	}, nil
}

func (cs *CertService) GetCertificateContent(certPath string) (string, error) {
	content, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("读取证书文件失败: %w", err)
	}
	return string(content), nil
}

func (cs *CertService) CleanupCertificates(providerUUID string) error {
	global.APP_LOG.Info("开始清理证书文件", zap.String("providerUUID", providerUUID))

	certsDir := "certs"
	certPath := filepath.Join(certsDir, fmt.Sprintf("%s.crt", providerUUID))
	keyPath := filepath.Join(certsDir, fmt.Sprintf("%s.key", providerUUID))

	if err := os.Remove(certPath); err != nil && !os.IsNotExist(err) {
		global.APP_LOG.Warn("删除证书文件失败",
			zap.String("path", utils.TruncateString(certPath, 100)),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
	}
	if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
		global.APP_LOG.Warn("删除私钥文件失败",
			zap.String("path", utils.TruncateString(keyPath, 100)),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
	}
	global.APP_LOG.Info("清理证书文件完成", zap.String("providerUUID", providerUUID))
	return nil
}
func (cs *CertService) generateLXDScript(provider *provider.Provider, certContent string) string {
	return fmt.Sprintf(`#!/bin/bash
set -e

echo "=== OneClickVirt LXD 配置开始 ==="

LXC_CMD=""
if command -v lxc >/dev/null 2>&1; then
	LXC_CMD=$(which lxc)
elif [ -x "/usr/bin/lxc" ]; then
	LXC_CMD="/usr/bin/lxc"
elif [ -x "/snap/bin/lxc" ]; then
	LXC_CMD="/snap/bin/lxc"
else
	echo "❌ 未找到可用的lxc命令"
	exit 1
fi
echo "✅ 使用LXC命令: $LXC_CMD"

echo "检查LXD服务状态..."
if systemctl is-active lxd >/dev/null 2>&1 || systemctl is-active snap.lxd.daemon >/dev/null 2>&1; then
	echo "✅ LXD服务已运行"
else
	echo "启动LXD服务..."
	systemctl start lxd || systemctl start snap.lxd.daemon || true
	sleep 2
fi

echo "等待LXD服务就绪..."
for i in {1..10}; do
	if $LXC_CMD info >/dev/null 2>&1; then
		echo "✅ LXD服务已就绪"
		break
	fi
	echo "等待中... ($i/10)"
	sleep 2
done

if ! $LXC_CMD info >/dev/null 2>&1; then
	echo "❌ LXD服务启动失败"
	exit 1
fi

echo "清理旧证书..."
rm -f /var/lib/lxd/server.crt.d/oneclickvirt-*.crt || true
$LXC_CMD config trust list --format=json | jq -r '.[].fingerprint' | xargs -r -n1 $LXC_CMD config trust remove

echo "创建证书目录..."
mkdir -p /var/lib/lxd/server.crt.d/

echo "安装客户端证书..."
cat > /var/lib/lxd/server.crt.d/oneclickvirt-%s.crt << 'CERT_EOF'
%s
CERT_EOF

chmod 600 /var/lib/lxd/server.crt.d/oneclickvirt-%s.crt
echo "✅ 证书文件已创建"

echo "添加证书到信任列表..."
# 执行新版本命令格式
echo "执行 add-certificate 命令..."
$LXC_CMD config trust add-certificate /var/lib/lxd/server.crt.d/oneclickvirt-%s.crt || true
# 执行旧版本命令格式
echo "执行 add 命令..."
$LXC_CMD config trust add /var/lib/lxd/server.crt.d/oneclickvirt-%s.crt || true
# 检查证书是否成功添加到信任列表
echo "检查信任列表..."
if $LXC_CMD config trust list --format=json | jq -r '.[].name' | grep -q "oneclickvirt-%s.crt"; then
	echo "✅ 证书已成功添加到信任列表"
else
	echo "❌ 证书添加失败，请检查配置"
fi

echo "配置监听地址..."
current_addr=$($LXC_CMD config get core.https_address || true)
if [ -z "$current_addr" ]; then
	$LXC_CMD config set core.https_address 0.0.0.0:8443
	echo "✅ 已设置监听地址为 0.0.0.0:8443"
else
	echo "✅ 监听地址已设置: $current_addr"
fi

echo "重启LXD服务..."
systemctl restart lxd || systemctl restart snap.lxd.daemon
sleep 3

echo "等待服务重启完成..."
for i in {1..15}; do
	if $LXC_CMD info >/dev/null 2>&1; then
		echo "✅ LXD服务重启完成"
		break
	fi
	echo "等待重启... ($i/15)"
	sleep 2
done

if ! $LXC_CMD info >/dev/null 2>&1; then
	echo "❌ LXD服务重启后无法连接"
	exit 1
fi

echo "清理信任密码..."
$LXC_CMD config unset core.trust_password || true

echo "✅ Provider UUID: %s"
echo "✅ API 端点: https://%s:8443"
echo "=== LXD 配置完成 ==="
`, provider.UUID, certContent, provider.UUID, provider.UUID, provider.UUID, provider.UUID, provider.UUID, provider.Endpoint)
}

func (cs *CertService) generateIncusScript(provider *provider.Provider, certContent string) string {
	return fmt.Sprintf(`#!/bin/bash
set -e
echo "=== OneClickVirt Incus 配置开始 ==="
INCUS_CMD=""
if command -v incus >/dev/null 2>&1; then
 INCUS_CMD=$(which incus)
elif [ -x "/usr/bin/incus" ]; then
 INCUS_CMD="/usr/bin/incus"
elif [ -x "/snap/bin/incus" ]; then
 INCUS_CMD="/snap/bin/incus"
else
 echo "❌ 未找到可用的incus命令"
 exit 1
fi
echo "✅ 使用Incus命令: $INCUS_CMD"
echo "检查Incus服务状态..."
if systemctl is-active incus >/dev/null 2>&1 || systemctl is-active snap.incus.daemon >/dev/null 2>&1; then
 echo "✅ Incus服务已运行"
else
 echo "启动Incus服务..."
 systemctl start incus || systemctl start snap.incus.daemon || true
 sleep 2
fi
echo "等待Incus服务就绪..."
for i in {1..10}; do
 if $INCUS_CMD info >/dev/null 2>&1; then
 echo "✅ Incus服务已就绪"
 break
 fi
 echo "等待中... ($i/10)"
 sleep 2
done
if ! $INCUS_CMD info >/dev/null 2>&1; then
 echo "❌ Incus服务启动失败"
 exit 1
fi
echo "清理旧证书..."
rm -f /var/lib/incus/server.crt.d/oneclickvirt-*.crt || true
$INCUS_CMD config trust list --format=json | jq -r '.[].fingerprint' | xargs -r -n1 $INCUS_CMD config trust remove
echo "创建证书目录..."
mkdir -p /var/lib/incus/server.crt.d/
echo "安装客户端证书..."
cat > /var/lib/incus/server.crt.d/oneclickvirt-%s.crt << 'CERT_EOF'
%s
CERT_EOF
chmod 600 /var/lib/incus/server.crt.d/oneclickvirt-%s.crt
echo "✅ 证书文件已创建"
echo "添加证书到信任列表..."
# 执行新版本命令格式
echo "执行 add-certificate 命令..."
$INCUS_CMD config trust add-certificate /var/lib/incus/server.crt.d/oneclickvirt-%s.crt || true
# 执行旧版本命令格式
echo "执行 add 命令..."
$INCUS_CMD config trust add /var/lib/incus/server.crt.d/oneclickvirt-%s.crt || true
# 检查证书是否成功添加到信任列表
echo "检查信任列表..."
if $INCUS_CMD config trust list --format=json | jq -r '.[].name' | grep -q "oneclickvirt-%s.crt"; then
 echo "✅ 证书已成功添加到信任列表"
else
 echo "❌ 证书添加失败，请检查配置"
fi
echo "配置监听地址..."
current_addr=$($INCUS_CMD config get core.https_address || true)
if [ -z "$current_addr" ]; then
 $INCUS_CMD config set core.https_address 0.0.0.0:8443
 echo "✅ 已设置监听地址为 0.0.0.0:8443"
else
 echo "✅ 监听地址已设置: $current_addr"
fi
echo "重启Incus服务..."
systemctl restart incus || systemctl restart snap.incus.daemon
sleep 3
echo "等待服务重启完成..."
for i in {1..15}; do
 if $INCUS_CMD info >/dev/null 2>&1; then
 echo "✅ Incus服务重启完成"
 break
 fi
 echo "等待重启... ($i/15)"
 sleep 2
done
if ! $INCUS_CMD info >/dev/null 2>&1; then
 echo "❌ Incus服务重启后无法连接"
 exit 1
fi
echo "清理信任密码..."
$INCUS_CMD config unset core.trust_password || true
echo "✅ Provider UUID: %s"
echo "✅ API 端点: https://%s:8443"
echo "=== Incus 配置完成 ==="
`, provider.UUID, certContent, provider.UUID, provider.UUID, provider.UUID, provider.UUID, provider.UUID, provider.Endpoint)
}

func (cs *CertService) generateProxmoxScript(providerUUID, username, tokenId string) string {
	return fmt.Sprintf(`#!/bin/bash

echo "=== OneClickVirt Proxmox VE 配置开始 ==="

# 检查是否为Proxmox VE环境
if ! command -v pveum &> /dev/null; then
    echo "❌ 错误：当前系统不是Proxmox VE环境"
    exit 1
fi

echo "✅ Proxmox VE环境检查通过"

# 检查当前用户权限
if [ "$(id -u)" -ne 0 ]; then
    echo "⚠️ 当前用户不是root，尝试使用sudo执行"
    # 检查是否有sudo权限
    if ! sudo -n true 2>/dev/null; then
        echo "❌ 错误：当前用户没有sudo权限，请使用root用户或配置sudo权限"
        exit 1
    fi
    # 重新以sudo执行自己
    exec sudo bash "$0" "$@"
fi

if ! command -v pveum >/dev/null 2>&1; then
	echo "❌ 未找到pveum命令，请确认这是Proxmox VE服务器"
	exit 1
fi
apt install jq -y >/dev/null 2>&1 || true
echo "✅ Proxmox VE环境检查通过"

# 删除现有Token（可选，谨慎）
for user in $(pveum user list --output-format=json | jq -r '.[].userid'); do
  for token in $(pveum user token list $user --output-format=json | jq -r '.[].tokenid'); do
    pveum user token delete $user $token
  done
done

echo "检查用户是否存在..."
if pveum user list 2>/dev/null | grep -q "%s@pve$"; then
	echo "✅ 用户 %s@pve 已存在"
else
	echo "创建API用户..."
	if pveum user add %s@pve --comment "OneClickVirt API User" 2>/dev/null; then
		echo "✅ 用户 %s@pve 已创建"
	else
		echo "⚠️ 用户 %s@pve 可能已存在，继续执行..."
	fi
fi

echo "分配管理员权限..."
pveum aclmod / -user %s@pve -role Administrator 2>/dev/null || true
echo "✅ 管理员权限处理完成"

echo "检查Token是否存在..."
token_list_output=$(pveum user token list %s@pve --output-format=json 2>/dev/null || echo "[]")
if echo "$token_list_output" | jq -r '.[].tokenid' | grep -q "^%s$"; then
	echo "删除现有Token..."
	pveum user token remove %s@pve %s 2>/dev/null || true
	echo "✅ 旧Token处理完成"
else
	echo "✅ 没有发现现有Token"
fi

echo "创建新的API Token..."
# 使用JSON输出，保证token_secret正确
output=$(pveum user token add %s@pve %s --privsep=0 --output-format=json 2>/dev/null)
token_secret=$(echo "$output" | jq -r '.value')

if [ -z "$token_secret" ] || [ "$token_secret" == "null" ]; then
	echo "❌ 无法获取Token密钥"
	exit 1
fi

echo "✅ 成功获取Token密钥: ${token_secret:0:8}..."

echo "保存Token信息..."
cat > /tmp/oneclickvirt-proxmox-config << EOF
TOKEN_ID=%s@pve!%s
TOKEN_SECRET=$token_secret
ENDPOINT=https://$(hostname -I | awk '{print $1}'):8006
EOF

chmod 600 /tmp/oneclickvirt-proxmox-config
echo "✅ Token信息已保存到 /tmp/oneclickvirt-proxmox-config"

echo "配置信息："
cat /tmp/oneclickvirt-proxmox-config

echo "✅ Provider UUID: %s"
echo "✅ Token ID: %s@pve!%s"
echo "=== Proxmox VE 配置完成 ==="
`, username, username, username, username, username, username, username, tokenId, username, tokenId, username, tokenId, username, tokenId, providerUUID, username, tokenId)
}


// ProxmoxTokenInfo 存储 Proxmox Token 信息的结构
type ProxmoxTokenInfo struct {
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
	Username    string `json:"username"`
	Created     string `json:"created"`
}
