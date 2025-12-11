# Provider Package

## 目录结构

```
provider/
├── provider.go              # Provider统一接口定义和注册表
├── transport_cleanup.go     # HTTP Transport清理管理器
├── docker/                  # Docker容器提供商实现
├── health/                  # 健康检查模块
├── incus/                   # Incus容器提供商实现
├── lxd/                     # LXD容器提供商实现
├── portmapping/             # 端口映射模块
└── proxmox/                 # Proxmox虚拟化提供商实现
```

## 核心概念

### Provider接口

Provider是所有虚拟化平台的统一抽象接口，定义了实例管理、镜像管理、连接管理、健康检查等标准操作。

```go
type Provider interface {
    // 基础信息
    GetType() string
    GetName() string
    GetSupportedInstanceTypes() []string

    // 实例管理
    ListInstances(ctx context.Context) ([]Instance, error)
    CreateInstance(ctx context.Context, config InstanceConfig) error
    CreateInstanceWithProgress(ctx context.Context, config InstanceConfig, progressCallback ProgressCallback) error
    StartInstance(ctx context.Context, id string) error
    StopInstance(ctx context.Context, id string) error
    RestartInstance(ctx context.Context, id string) error
    DeleteInstance(ctx context.Context, id string) error
    GetInstance(ctx context.Context, id string) (*Instance, error)

    // 镜像管理
    ListImages(ctx context.Context) ([]Image, error)
    PullImage(ctx context.Context, image string) error
    DeleteImage(ctx context.Context, id string) error

    // 连接管理
    Connect(ctx context.Context, config NodeConfig) error
    Disconnect(ctx context.Context) error
    IsConnected() bool

    // 健康检查
    HealthCheck(ctx context.Context) (*health.HealthResult, error)
    GetHealthChecker() health.HealthChecker

    // 平台信息
    GetVersion() string

    // 密码管理
    SetInstancePassword(ctx context.Context, instanceID, password string) error
    ResetInstancePassword(ctx context.Context, instanceID string) (string, error)

    // SSH命令执行
    ExecuteSSHCommand(ctx context.Context, command string) (string, error)
}
```

### Provider注册机制

通过`RegisterProvider`函数将Provider实现注册到全局注册表，系统启动时通过`init()`函数自动注册。

```go
func init() {
    provider.RegisterProvider("docker", NewDockerProvider)
}
```

### 执行规则

Provider支持三种执行规则，控制操作的执行方式：

- `api_only`: 仅通过API执行操作
- `ssh_only`: 仅通过SSH执行操作
- `auto`: 优先使用API，失败时自动回退到SSH

## 已支持的Provider

### Docker

基于Docker容器技术的Provider实现。

- 类型标识: `docker`
- 支持实例类型: `container`
- 连接方式: SSH
- 执行方式: SSH命令行
- 特性:
  - 容器生命周期管理
  - 镜像拉取和删除
  - 容器网络信息获取
  - IPv6网络支持检测
  - 自动重连机制

### Incus

基于Incus容器/虚拟机技术的Provider实现。

- 类型标识: `incus`
- 支持实例类型: `container`, `vm`
- 连接方式: SSH + API (可选)
- 执行方式: 根据执行规则选择API或SSH
- 特性:
  - 容器和虚拟机管理
  - 证书认证的API访问
  - SSH命令行备用方案
  - IPv6网络配置
  - 端口映射支持
  - Transport资源自动清理

### LXD

基于LXD容器/虚拟机技术的Provider实现。

- 类型标识: `lxd`
- 支持实例类型: `container`, `vm`
- 连接方式: SSH + API (可选)
- 执行方式: 根据执行规则选择API或SSH
- 特性:
  - 容器和虚拟机管理
  - 证书认证的API访问
  - SSH命令行备用方案
  - IPv6网络配置
  - 端口映射支持
  - Transport资源自动清理

### Proxmox

基于Proxmox VE虚拟化平台的Provider实现。

- 类型标识: `proxmox`
- 支持实例类型: `vm`
- 连接方式: SSH + API
- 执行方式: 根据执行规则选择API或SSH
- 特性:
  - 虚拟机生命周期管理
  - Token认证的API访问
  - SSH命令行备用方案
  - 虚拟机配置管理
  - 网络和存储配置
  - Transport资源自动清理

## 子模块

### health/

健康检查模块，为所有Provider提供统一的健康检查能力。支持SSH连接检查、API服务检查和平台特定的服务状态检查。详见 [health/README.md](health/README.md)。

### portmapping/

端口映射模块，为不同Provider提供统一的端口映射管理接口。支持多种映射方法（native、iptables等），提供端口分配、映射创建和删除等功能。

## 新增Provider类型的指南

### 步骤1: 创建Provider目录

在`server/provider/`下创建新的目录，目录名为Provider类型（小写）。

```bash
mkdir server/provider/newprovider
```

### 步骤2: 实现Provider接口

创建主文件`newprovider.go`，实现Provider接口。

```go
package newprovider

import (
    "context"
    "oneclickvirt/provider"
    "oneclickvirt/provider/health"
)

type NewProvider struct {
    config        provider.NodeConfig
    connected     bool
    healthChecker health.HealthChecker
    version       string
}

func NewNewProvider() provider.Provider {
    return &NewProvider{}
}

func (n *NewProvider) GetType() string {
    return "newprovider"
}

func (n *NewProvider) GetName() string {
    return n.config.Name
}

func (n *NewProvider) GetSupportedInstanceTypes() []string {
    return []string{"container", "vm"} // 根据实际情况修改
}

// 实现其他接口方法...
```

### 步骤3: 实现连接管理

```go
func (n *NewProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
    n.config = config
    
    // 建立SSH连接
    sshConfig := utils.SSHConfig{
        Host:           config.Host,
        Port:           config.Port,
        Username:       config.Username,
        Password:       config.Password,
        PrivateKey:     config.PrivateKey,
        ConnectTimeout: 30 * time.Second,
        ExecuteTimeout: 300 * time.Second,
    }
    
    client, err := utils.NewSSHClient(sshConfig)
    if err != nil {
        return fmt.Errorf("failed to connect via SSH: %w", err)
    }
    
    n.sshClient = client
    n.connected = true
    
    // 初始化健康检查器
    healthConfig := health.HealthConfig{
        Host:       config.Host,
        Port:       config.Port,
        Username:   config.Username,
        Password:   config.Password,
        SSHEnabled: true,
        APIEnabled: false,
        Timeout:    30 * time.Second,
    }
    
    zapLogger, _ := zap.NewProduction()
    n.healthChecker = health.NewBaseHealthChecker(healthConfig, zapLogger)
    
    return nil
}

func (n *NewProvider) Disconnect(ctx context.Context) error {
    if n.sshClient != nil {
        n.sshClient.Close()
        n.connected = false
    }
    return nil
}

func (n *NewProvider) IsConnected() bool {
    return n.connected && n.sshClient != nil && n.sshClient.IsHealthy()
}
```

### 步骤4: 实现实例管理

```go
func (n *NewProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
    if !n.connected {
        return nil, fmt.Errorf("not connected")
    }
    
    // 执行命令获取实例列表
    output, err := n.sshClient.Execute("your-cli-command list")
    if err != nil {
        return nil, err
    }
    
    // 解析输出并构造Instance对象
    instances := []provider.Instance{}
    // 解析逻辑...
    
    return instances, nil
}

func (n *NewProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
    if !n.connected {
        return fmt.Errorf("not connected")
    }
    
    // 构造创建命令
    cmd := fmt.Sprintf("your-cli-command create %s", config.Name)
    
    // 执行命令
    _, err := n.sshClient.Execute(cmd)
    return err
}

// 实现其他实例管理方法...
```

### 步骤5: 实现健康检查

如果需要特定的健康检查逻辑，在`server/provider/health/`下创建对应的健康检查器。

```go
// health/newprovider.go
package health

type NewProviderHealthChecker struct {
    *BaseHealthChecker
}

func NewNewProviderHealthChecker(config HealthConfig, logger *zap.Logger) HealthChecker {
    base := NewBaseHealthChecker(config, logger)
    return &NewProviderHealthChecker{
        BaseHealthChecker: base,
    }
}

func (n *NewProviderHealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
    // 调用基础检查
    result, err := n.BaseHealthChecker.CheckHealth(ctx)
    if err != nil {
        return result, err
    }
    
    // 添加平台特定检查
    // ...
    
    return result, nil
}
```

### 步骤6: 注册Provider

在主文件末尾添加init函数进行注册。

```go
func init() {
    provider.RegisterProvider("newprovider", NewNewProvider)
}
```

### 步骤7: 端口映射支持（可选）

如需支持端口映射，在`server/provider/portmapping/`下创建对应的实现。

```go
// portmapping/newprovider/newprovider.go
package newprovider

type NewProviderPortMapping struct {
    // 字段定义
}

func NewNewProviderPortMapping(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
    return &NewProviderPortMapping{}
}

// 实现PortMappingProvider接口...

func init() {
    portmapping.RegisterProvider("newprovider", NewNewProviderPortMapping)
}
```

### 步骤8: 测试

创建单元测试和集成测试验证实现的正确性。

```go
// newprovider_test.go
package newprovider

import (
    "context"
    "testing"
)

func TestNewProvider_Connect(t *testing.T) {
    // 测试连接功能
}

func TestNewProvider_ListInstances(t *testing.T) {
    // 测试实例列表功能
}
```

## 注意事项

### 连接管理

- 实现SSH连接健康检查和自动重连机制
- 如使用API连接，注意Transport资源的清理
- 使用`transport_cleanup.go`中的管理器注册HTTP Transport

### 错误处理

- 区分连接错误和业务错误
- 提供清晰的错误信息
- 实现重试机制处理临时性故障

### 日志记录

- 使用`global.APP_LOG`记录关键操作
- 敏感信息使用`utils.TruncateString`截断
- 区分Debug、Info、Warn、Error日志级别

### 并发安全

- 使用`sync.RWMutex`保护共享状态
- 注意SSH客户端和API客户端的并发访问

### 执行规则

- 支持`api_only`、`ssh_only`、`auto`三种执行规则
- 实现API到SSH的自动回退逻辑
- 在回退前检查SSH连接健康状态

### 资源清理

- 实现`Disconnect`方法释放所有资源
- 使用Transport清理管理器管理HTTP连接
- 避免资源泄漏

## 工具函数

### Transport清理管理器

用于管理HTTP Transport资源，防止连接泄漏。

```go
// 注册Transport
provider.GetTransportCleanupManager().RegisterTransport(transport)

// 关联Provider ID
provider.GetTransportCleanupManager().RegisterTransportWithProvider(transport, providerID)

// 清理特定Provider的Transport
provider.GetTransportCleanupManager().CleanupProvider(providerID)
```

### SSH客户端

使用`utils.SSHClient`进行SSH连接管理。

```go
// 创建SSH客户端
client, err := utils.NewSSHClient(sshConfig)

// 执行命令
output, err := client.Execute(command)

// 带日志的执行
output, err := client.ExecuteWithLogging(command, "OPERATION_NAME")

// 检查健康状态
healthy := client.IsHealthy()

// 重连
err := client.Reconnect()
```

## 相关文档

- [health/README.md](health/README.md) - 健康检查模块文档
- [../model/provider/](../model/provider/) - Provider数据模型定义
- [../service/provider/](../service/provider/) - Provider服务层实现
