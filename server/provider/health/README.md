# Health Check System

## 概述

Health Check System是Provider Package的统一健康检查模块，为所有虚拟化平台提供标准化的健康检查功能。该系统支持SSH连接检查、API服务检查和平台特定的服务状态检查，并提供详细的健康报告。

## 架构设计

```
server/provider/health/
├── interface.go    # 健康检查接口定义
├── base.go         # 基础健康检查器实现
├── manager.go      # 健康检查管理器
├── factory.go      # 工厂函数和适配器
├── utils.go        # 工具函数
├── docker.go       # Docker健康检查器
├── lxd.go          # LXD健康检查器
├── incus.go        # Incus健康检查器
└── proxmox.go      # Proxmox健康检查器
```

## 核心组件

### HealthChecker接口

统一的健康检查接口，所有平台的健康检查器都实现此接口。

```go
type HealthChecker interface {
    CheckHealth(ctx context.Context) (*HealthResult, error)
    GetHealthStatus() HealthStatus
    SetConfig(config HealthConfig)
}
```

### BaseHealthChecker

基础健康检查器，提供通用的健康检查逻辑，所有平台特定的检查器都基于此实现。

**功能**:
- HTTP客户端管理
- TLS配置支持
- 多检查项合并执行
- 统一的结果格式化

### HealthManager

健康检查管理器，用于批量管理多个健康检查器。

**功能**:
- 检查器注册和管理
- 批量健康检查
- Provider类型识别
- 默认配置应用

### 平台特定检查器

每个虚拟化平台都有对应的健康检查器实现：

- **DockerHealthChecker**: Docker平台健康检查
- **LXDHealthChecker**: LXD平台健康检查
- **IncusHealthChecker**: Incus平台健康检查
- **ProxmoxHealthChecker**: Proxmox平台健康检查

## 核心数据结构

### HealthConfig

健康检查配置。

```go
type HealthConfig struct {
    // 基础连接配置
    Host     string
    Port     int
    Username string
    Password string

    // API配置
    APIEnabled    bool
    APIPort       int
    APIScheme     string  // http, https
    SkipTLSVerify bool
    Token         string
    TokenID       string
    CertPath      string
    KeyPath       string
    CertContent   string
    KeyContent    string

    // 检查配置
    Timeout        time.Duration
    SSHEnabled     bool
    ServiceChecks  []string
    CustomCommands []string
}
```

### HealthResult

健康检查结果。

```go
type HealthResult struct {
    Status        HealthStatus
    Timestamp     time.Time
    Duration      time.Duration
    SSHStatus     string
    APIStatus     string
    ServiceStatus string
    Errors        []string
    Details       map[string]interface{}
    ResourceInfo  *ResourceInfo
}
```

### HealthStatus

健康状态枚举。

```go
type HealthStatus string

const (
    HealthStatusUnknown   HealthStatus = "unknown"
    HealthStatusHealthy   HealthStatus = "healthy"
    HealthStatusUnhealthy HealthStatus = "unhealthy"
    HealthStatusPartial   HealthStatus = "partial"
)
```

### CheckResult

单个检查项的结果。

```go
type CheckResult struct {
    Type     CheckType
    Success  bool
    Duration time.Duration
    Error    string
    Details  map[string]interface{}
}
```

### CheckType

检查类型枚举。

```go
type CheckType string

const (
    CheckTypeSSH     CheckType = "ssh"
    CheckTypeAPI     CheckType = "api"
    CheckTypeService CheckType = "service"
    CheckTypeCustom  CheckType = "custom"
)
```

### ResourceInfo

节点资源信息。

```go
type ResourceInfo struct {
    CPUCores    int
    MemoryTotal int64
    SwapTotal   int64
    DiskTotal   int64
    DiskFree    int64
    Synced      bool
    SyncedAt    *time.Time
}
```

## 支持的Provider类型

### Docker

**检查项**:
- SSH连接测试
- Docker API版本检查（可选）
- Docker服务状态检查

**API配置**:
- 默认端口: 2375
- 默认协议: http

### LXD

**检查项**:
- SSH连接测试
- LXD API服务检查
- 证书认证验证
- 系统资源信息

**API配置**:
- 默认端口: 8443
- 默认协议: https
- 认证方式: TLS证书

### Incus

**检查项**:
- SSH连接测试
- Incus API服务检查
- 证书认证验证
- 系统资源信息

**API配置**:
- 默认端口: 8443
- 默认协议: https
- 认证方式: TLS证书

### Proxmox

**检查项**:
- SSH连接测试
- Proxmox VE API检查
- Token认证验证
- 节点状态检查

**API配置**:
- 默认端口: 8006
- 默认协议: https
- 认证方式: API Token

## 使用方法

### 创建健康检查器

```go
import "oneclickvirt/provider/health"

// 使用工厂函数创建
logger, _ := zap.NewProduction()
checker, err := health.CreateHealthChecker(
    "docker",
    "192.168.1.100",
    "root",
    "password",
    22,
    logger,
)

// 或使用管理器创建
manager := health.NewHealthManager(logger)
config := health.HealthConfig{
    Host:       "192.168.1.100",
    Port:       22,
    Username:   "root",
    Password:   "password",
    SSHEnabled: true,
    APIEnabled: true,
    Timeout:    30 * time.Second,
}
checker, err := manager.CreateChecker(health.ProviderTypeDocker, config)
```

### 执行健康检查

```go
ctx := context.Background()
result, err := checker.CheckHealth(ctx)
if err != nil {
    log.Printf("Health check error: %v", err)
    return
}

fmt.Printf("Status: %s\n", result.Status)
fmt.Printf("SSH Status: %s\n", result.SSHStatus)
fmt.Printf("API Status: %s\n", result.APIStatus)
fmt.Printf("Duration: %v\n", result.Duration)

if len(result.Errors) > 0 {
    fmt.Printf("Errors: %v\n", result.Errors)
}
```

### 在Provider中集成

```go
type DockerProvider struct {
    config        provider.NodeConfig
    sshClient     *utils.SSHClient
    connected     bool
    healthChecker health.HealthChecker
}

func (p *DockerProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
    // ... 其他连接逻辑

    // 初始化健康检查器
    healthConfig := health.HealthConfig{
        Host:          config.Host,
        Port:          config.Port,
        Username:      config.Username,
        Password:      config.Password,
        APIEnabled:    true,
        SSHEnabled:    true,
        Timeout:       30 * time.Second,
        ServiceChecks: []string{"docker"},
    }

    p.healthChecker = health.NewDockerHealthChecker(healthConfig, logger)
    return nil
}

func (p *DockerProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
    return p.healthChecker.CheckHealth(ctx)
}

func (p *DockerProvider) GetHealthChecker() health.HealthChecker {
    return p.healthChecker
}
```

### 批量健康检查

```go
manager := health.NewHealthManager(logger)

// 注册多个检查器
manager.RegisterChecker("docker1", dockerChecker1)
manager.RegisterChecker("docker2", dockerChecker2)
manager.RegisterChecker("lxd1", lxdChecker)

// 批量检查
results, err := manager.CheckAllHealth(context.Background())
for id, result := range results {
    fmt.Printf("%s: %s\n", id, result.Status)
}
```

### 使用适配器简化集成

```go
adapter := health.NewHealthCheckAdapter(checker)

// 简单的错误检查
err := adapter.CheckHealth(ctx)
if err != nil {
    log.Printf("Health check failed: %v", err)
}

// 获取详细结果
result, err := adapter.GetHealthResult(ctx)
```

## 配置示例

### Docker配置

```go
config := health.HealthConfig{
    Host:          "192.168.1.100",
    Port:          22,
    Username:      "root",
    Password:      "password",
    SSHEnabled:    true,
    APIEnabled:    false,  // Docker通常不启用API检查或使用HTTP
    Timeout:       30 * time.Second,
    ServiceChecks: []string{"docker"},
}
```

### LXD配置（证书认证）

```go
config := health.HealthConfig{
    Host:          "192.168.1.100",
    Port:          22,
    Username:      "root",
    Password:      "password",
    SSHEnabled:    true,
    APIEnabled:    true,
    APIPort:       8443,
    APIScheme:     "https",
    CertPath:      "/path/to/client.crt",
    KeyPath:       "/path/to/client.key",
    SkipTLSVerify: false,
    Timeout:       30 * time.Second,
}
```

### Proxmox配置（Token认证）

```go
config := health.HealthConfig{
    Host:          "192.168.1.100",
    Port:          22,
    Username:      "root",
    Password:      "password",
    SSHEnabled:    true,
    APIEnabled:    true,
    APIPort:       8006,
    APIScheme:     "https",
    Token:         "PVEAPIToken=user@pam!token=uuid",
    SkipTLSVerify: true,
    Timeout:       30 * time.Second,
}
```

## 状态判断逻辑

健康检查器根据各检查项的结果判断总体健康状态：

- **healthy**: 所有启用的检查项都成功
- **partial**: 部分检查项成功（如SSH成功但API失败）
- **unhealthy**: 所有检查项都失败
- **unknown**: 未执行检查或状态未知

## 超时控制

所有健康检查都支持context超时控制：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := checker.CheckHealth(ctx)
```

## 错误处理

健康检查结果中包含详细的错误信息：

```go
result, err := checker.CheckHealth(ctx)
if err != nil {
    // 检查执行失败
    return err
}

if result.Status == health.HealthStatusUnhealthy {
    // 健康检查不通过
    for _, errMsg := range result.Errors {
        log.Printf("Health check error: %s", errMsg)
    }
}
```

## 性能考虑

- 合理设置超时时间避免长时间阻塞
- 可以禁用不需要的检查项（SSH或API）
- 使用连接池复用SSH连接
- HTTP客户端自动处理连接复用
- 并发执行多个检查项

## 最佳实践

1. **合理配置超时**: 根据网络环境设置适当的超时时间
2. **启用必要的检查**: 根据实际使用场景选择启用的检查项
3. **处理部分失败**: 对partial状态进行适当处理
4. **定期健康检查**: 定时执行健康检查监控系统状态
5. **日志记录**: 记录健康检查结果便于问题排查
6. **TLS证书验证**: 生产环境建议启用证书验证
7. **连接复用**: 重复使用健康检查器实例避免频繁创建

## 扩展自定义检查

可以通过自定义命令添加额外的健康检查：

```go
config := health.HealthConfig{
    // ... 基础配置
    CustomCommands: []string{
        "systemctl status custom-service",
        "df -h | grep /data",
    },
}
```

自定义检查结果会包含在`Details`字段中。
