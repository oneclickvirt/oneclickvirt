# Provider Package

## 概述

Provider Package 是 OneClickVirt 项目的核心组件,提供了对多种虚拟化平台的统一管理接口。通过抽象化的设计,支持 Docker、LXD、Incus 和 Proxmox 等主流虚拟化技术。

## 架构设计

```
server/provider/
├── provider.go          # Provider 接口定义和注册表
├── docker/              # Docker Provider 实现
├── lxd/                 # LXD Provider 实现
├── incus/               # Incus Provider 实现
├── proxmox/             # Proxmox Provider 实现
├── portmapping/         # 端口映射管理
└── health/              # 统一健康检查系统
```

## 核心接口

### Provider 接口

所有虚拟化平台都实现统一的 Provider 接口：

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
    
    // 密码管理
    SetInstancePassword(ctx context.Context, instanceID, password string) error
    ResetInstancePassword(ctx context.Context, instanceID string) (string, error)
    
    // SSH命令执行
    ExecuteSSHCommand(ctx context.Context, command string) (string, error)
}
```

## 支持的平台

### [Docker Provider](./docker/README.md)

- **类型**: 容器化平台
- **实例类型**: container
- **管理方式**: SSH
- **适用场景**: 轻量级容器部署

### [LXD Provider](./lxd/README.md)

- **类型**: 系统容器和虚拟机
- **实例类型**: container, vm
- **管理方式**: API, SSH
- **适用场景**: 高性能容器和虚拟机

### [Incus Provider](./incus/README.md)

- **类型**: 系统容器和虚拟机（LXD 分支）
- **实例类型**: container, vm
- **管理方式**: API, SSH
- **适用场景**: LXD 的社区驱动版本

### [Proxmox Provider](./proxmox/README.md)

- **类型**: 企业级虚拟化平台
- **实例类型**: container, vm
- **管理方式**: API, SSH
- **适用场景**: 企业级虚拟化部署

## 健康检查系统

统一的[健康检查系统](./health/README.md)提供：

- SSH 连接状态检查
- API 服务状态检查
- 平台特定的服务状态检查
- 详细的健康报告

## 使用方式

### 1. 创建 Provider 实例

```go
import "oneclickvirt/server/provider"

// 根据类型创建对应的 Provider
var p provider.Provider
switch providerType {
case "docker":
    p = docker.NewDockerProvider()
case "lxd":
    p = lxd.NewLXDProvider()
case "incus":
    p = incus.NewIncusProvider()
case "proxmox":
    p = proxmox.NewProxmoxProvider()
}
```

### 2. 配置连接参数

```go
config := provider.NodeConfig{
    Name:     "node-name",
    Host:     "192.168.1.100",
    Port:     22,
    Username: "root",
    Password: "password",
    Type:     "docker", // docker, lxd, incus, proxmox
}
```

### 3. 连接和使用

```go
// 连接到节点
err := p.Connect(ctx, config)
if err != nil {
    return err
}
defer p.Disconnect(ctx)

// 检查健康状态
healthResult, err := p.HealthCheck(ctx)

// 创建实例
instanceConfig := provider.InstanceConfig{
    Name:         "test-instance",
    Image:        "ubuntu:20.04",
    CPU:          2,
    Memory:       2048,
    Disk:         20480,
    InstanceType: "container",
    Password:     "secure-password",
}

err = p.CreateInstance(ctx, instanceConfig)
```

## 扩展新平台

要添加对新虚拟化平台的支持：

1. **实现 Provider 接口**

   ```go
   type NewProvider struct {
       // 平台特定的字段
   }
   
   func (n *NewProvider) GetType() string {
       return "new-platform"
   }
   
   // 实现其他接口方法...
   ```

2. **创建子包目录**

   ```
   provider/new-platform/
   ├── README.md
   ├── new-platform.go
   ├── instance.go
   ├── image.go
   └── password.go
   ```

3. **实现健康检查**

   ```go
   // 在 health/ 包中添加对应的健康检查器
   type NewPlatformHealthChecker struct {
       *BaseHealthChecker
       // 平台特定字段
   }
   
   func (c *NewPlatformHealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
       // 实现健康检查逻辑
   }
   ```

4. **注册 Provider**

   ```go
   // 在 provider 包的 init 函数中注册
   func init() {
       RegisterProvider("new-platform", NewNewPlatformProvider)
   }
   ```

## 配置文件格式

各平台的配置格式示例：

```yaml
providers:
  - name: "docker-node-1"
    type: "docker"
    host: "192.168.1.100"
    port: 22
    username: "root"
    password: "password"
    
  - name: "lxd-cluster-1"
    type: "lxd"
    host: "192.168.1.101"
    port: 22
    username: "root"
    password: "password"
    client_cert: "/path/to/client.crt"
    client_key: "/path/to/client.key"
    server_cert: "/path/to/server.crt"
    
  - name: "proxmox-node-1"
    type: "proxmox"
    host: "192.168.1.102"
    port: 22
    username: "root"
    password: "password"
    api_token: "user@pam!token=uuid"
    api_endpoint: "https://192.168.1.102:8006/api2/json"
```

## 核心数据结构

### NodeConfig

Provider 节点配置信息。

```go
type NodeConfig struct {
    Name              string
    Host              string
    Port              int
    Username          string
    Password          string
    CertPath          string
    KeyPath           string
    Token             string
    TokenID           string
    SSHConnectTimeout int
    SSHExecuteTimeout int
}
```

### InstanceConfig

实例创建配置。

```go
type InstanceConfig struct {
    Name         string
    Image        string
    InstanceType string // "container" 或 "vm"
    CPU          int    // CPU核心数
    Memory       int    // 内存大小（MB）
    Disk         int    // 磁盘大小（MB）
    Password     string // root密码
    SSHKey       string // SSH公钥
}
```

### Instance

实例信息（类型别名，实际定义在 model/provider 包中）。

```go
type Instance = provider.ProviderInstance
```

### Image

镜像信息（类型别名，实际定义在 model/provider 包中）。

```go
type Image = provider.ProviderImage
```

## Provider 注册与获取

Provider 使用单例注册表模式管理，每个 provider 类型在各自的包中自动注册。

### 注册 Provider

```go
// 在各 provider 包的 init 函数中自动注册
// 例如在 docker/docker.go 中：
func init() {
    provider.RegisterProvider("docker", NewDockerProvider)
}
```

### 获取 Provider 实例

```go
// 获取单个 Provider（单例模式）
dockerProvider, err := provider.GetProvider("docker")
if err != nil {
    return err
}

// 列出所有已注册的 Provider 类型
providerNames := provider.ListProviders()

// 获取所有已创建的 Provider 实例
allProviders := provider.GetAllProviders()
```

## 使用示例

### 连接 Provider

```go
config := provider.NodeConfig{
    Name:     "my-node",
    Host:     "192.168.1.100",
    Port:     22,
    Username: "root",
    Password: "password",
}

p, err := provider.GetProvider("docker")
if err != nil {
    return err
}

err = p.Connect(context.Background(), config)
if err != nil {
    return err
}
defer p.Disconnect(context.Background())
```

### 创建实例

```go
instanceConfig := provider.InstanceConfig{
    Name:         "test-container",
    Image:        "ubuntu:22.04",
    InstanceType: "container",
    CPU:          2,
    Memory:       2048,
    Disk:         20480,
    Password:     "secure-password",
}

err := p.CreateInstance(context.Background(), instanceConfig)
if err != nil {
    return err
}
```

### 管理实例

```go
// 列出实例
instances, err := p.ListInstances(context.Background())

// 启动实例
err = p.StartInstance(context.Background(), "instance-id")

// 停止实例
err = p.StopInstance(context.Background(), "instance-id")

// 重启实例
err = p.RestartInstance(context.Background(), "instance-id")

// 删除实例
err = p.DeleteInstance(context.Background(), "instance-id")

// 获取实例详情
instance, err := p.GetInstance(context.Background(), "instance-id")
```

### 镜像管理

```go
// 列出镜像
images, err := p.ListImages(context.Background())

// 拉取镜像
err = p.PullImage(context.Background(), "ubuntu:22.04")

// 删除镜像
err = p.DeleteImage(context.Background(), "image-id")
```

### 密码管理

```go
// 设置实例密码
err = p.SetInstancePassword(context.Background(), "instance-id", "new-password")

// 重置实例密码（自动生成）
newPassword, err := p.ResetInstancePassword(context.Background(), "instance-id")
```

### 健康检查

```go
// 执行健康检查
result, err := p.HealthCheck(context.Background())
if err != nil {
    return err
}

fmt.Printf("Status: %s\n", result.Status)
fmt.Printf("SSH Status: %s\n", result.SSHStatus)
fmt.Printf("API Status: %s\n", result.APIStatus)
fmt.Printf("Duration: %v\n", result.Duration)

if len(result.Errors) > 0 {
    fmt.Printf("Errors: %v\n", result.Errors)
}
```

## 进度回调

创建实例时可以使用进度回调获取实时进度：

```go
progressCallback := func(percentage int, message string) {
    fmt.Printf("Progress: %d%% - %s\n", percentage, message)
}

err := p.CreateInstanceWithProgress(
    ctx,
    instanceConfig,
    progressCallback,
)
```

## 错误处理

所有 Provider 操作都返回标准的 Go error,建议进行适当的错误处理：

```go
err := p.StartInstance(ctx, instanceID)
if err != nil {
    if strings.Contains(err.Error(), "not found") {
        // 处理实例不存在
    } else if strings.Contains(err.Error(), "timeout") {
        // 处理超时
    } else {
        // 其他错误
    }
}
```

## 上下文支持

所有操作都支持 context.Context,可以实现：

- 超时控制
- 取消操作
- 传递请求范围的值

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

err := p.CreateInstance(ctx, instanceConfig)
```

## 扩展新 Provider

要添加新的 Provider 实现：

1. 创建新的 package（如 `newprovider/`）
2. 实现 Provider 接口
3. 注册 Provider 到全局注册表
4. 添加健康检查器实现
5. 编写单元测试

参考现有的 Docker 或 LXD 实现作为模板。
