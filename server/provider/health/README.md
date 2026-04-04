# Provider 健康检查模块

## 概述

为所有虚拟化提供商（Provider）提供统一的健康检查机制，支持 SSH 连接检测、API 服务检测、系统服务状态检查、资源信息采集和存储池路径探测。

## 架构设计

### 文件结构

| 文件 | 说明 |
|---|---|
| `interface.go` | 核心接口和数据结构定义（`HealthChecker`、`HealthResult`、`HealthConfig`、`ResourceInfo`） |
| `manager.go` | 健康检查管理器，负责创建和管理不同类型的检查器 |
| `base.go` | 基础健康检查器实现，提供 HTTP 客户端管理和通用检查逻辑 |
| `factory.go` | 工厂方法和适配器，提供便捷的创建函数 |
| `utils.go` | 辅助工具：SSH 命令执行、资源信息采集、结果解析 |
| `storage_detection.go` | 存储池路径自动检测（支持 Proxmox/LXD/Incus/Docker） |
| `docker.go` | Docker 提供商健康检查实现 |
| `lxd.go` | LXD 提供商健康检查实现 |
| `incus.go` | Incus 提供商健康检查实现 |
| `proxmox.go` | Proxmox 提供商健康检查实现 |

### 支持的提供商类型

- `docker` — Docker 容器
- `podman` — Podman 容器（复用 Docker 检查逻辑）
- `containerd` — Containerd 容器（复用 Docker 检查逻辑）
- `lxd` — LXD 容器/虚拟机
- `incus` — Incus 容器/虚拟机
- `proxmox` — Proxmox VE 虚拟化

## 工作流程

1. 通过 `HealthManager` 创建指定类型的健康检查器
2. 配置检查参数（SSH、API、超时等）
3. 调用 `CheckHealth()` 执行检查
4. 返回包含状态、资源信息、错误等的 `HealthResult`

## 检查内容

| 检查项 | 说明 |
|---|---|
| SSH 连接检查 | 验证 SSH 服务可达性和认证 |
| API 服务检查 | 验证提供商 API 服务状态 |
| 服务状态检查 | 检查特定系统服务运行状态 |
| 资源信息采集 | CPU、内存、磁盘、存储池路径等 |
| 主机名获取 | 节点 hostname，用于区分多节点环境 |

## 健康状态定义

| 状态 | 说明 |
|---|---|
| `HealthStatusHealthy` | 所有检查项通过 |
| `HealthStatusUnhealthy` | 存在检查项失败 |
| `HealthStatusPartial` | 部分检查项通过 |
| `HealthStatusUnknown` | 无法确定健康状态 |

## 使用示例

### 通过管理器创建

```go
manager := NewHealthManager(logger)

config := HealthConfig{
    ProviderID:   1,
    ProviderName: "my-provider",
    Host:         "192.168.1.100",
    Port:         22,
    Username:     "root",
    Password:     "password",
    SSHEnabled:   true,
    APIEnabled:   true,
    Timeout:      30 * time.Second,
}

checker, err := manager.CreateChecker(ProviderTypeDocker, config)
result, err := checker.CheckHealth(context.Background())

fmt.Printf("状态: %s\n", result.Status)
fmt.Printf("SSH: %s\n", result.SSHStatus)
fmt.Printf("API: %s\n", result.APIStatus)
```

### 使用便捷函数

```go
checker, err := CreateHealthChecker("docker", "192.168.1.100", "root", "password", 22, logger)
result, err := checker.CheckHealth(context.Background())
```

### 存储池路径检测

```go
healthChecker := NewProviderHealthChecker(logger)
path, err := healthChecker.DetectStoragePoolPath(sshClient, "proxmox", "local")
```

## 配置参数

### 基础连接配置

| 参数 | 说明 |
|---|---|
| `Host` | 提供商主机地址 |
| `Port` | SSH 端口（默认 22） |
| `Username` | SSH 用户名 |
| `Password` | SSH 密码 |
| `PrivateKey` | SSH 私钥（优先于密码） |

### API 配置

| 参数 | 说明 |
|---|---|
| `APIEnabled` | 是否启用 API 检查 |
| `APIPort` | API 服务端口 |
| `APIScheme` | API 协议（http/https） |
| `SkipTLSVerify` | 是否跳过 TLS 证书验证 |
| `Token` | API 访问令牌 |
| `CertPath` / `CertContent` | TLS 证书路径或内容 |
| `KeyPath` / `KeyContent` | TLS 密钥路径或内容 |

### 检查配置

| 参数 | 说明 |
|---|---|
| `Timeout` | 检查超时时间 |
| `SSHEnabled` | 是否启用 SSH 检查 |
| `ServiceChecks` | 需要检查的系统服务列表 |
| `CustomCommands` | 自定义检查命令 |

## 新增提供商实现指南

### 步骤 1：创建检查器文件

创建 `<provider_name>.go` 文件：

```go
package health

type NewProviderHealthChecker struct {
    *BaseHealthChecker
}

func NewNewProviderHealthChecker(config HealthConfig, logger *zap.Logger) *NewProviderHealthChecker {
    return &NewProviderHealthChecker{
        BaseHealthChecker: NewBaseHealthChecker(config, logger),
    }
}

func (c *NewProviderHealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
    checks := []func(context.Context) CheckResult{}
    if c.config.SSHEnabled {
        checks = append(checks, c.createCheckFunc(CheckTypeSSH, c.checkSSH))
    }
    if c.config.APIEnabled {
        checks = append(checks, c.createCheckFunc(CheckTypeAPI, c.checkAPI))
    }
    result := c.executeChecks(ctx, checks)
    return result, nil
}
```

### 步骤 2：在 manager.go 中注册

在 `CreateChecker()` 方法中添加新的 case 分支，并在 `ProviderType` 常量中添加新类型。

### 步骤 3：实现存储池路径检测（可选）

在 `storage_detection.go` 中添加检测方法和对应的 case 分支。

### 步骤 4：实现资源信息采集（可选）

如果提供商有特殊的资源获取方式，在检查器中实现 `getResourceInfo` 方法。

## 注意事项

1. **并发安全**：所有健康检查器支持并发调用，使用 `sync.RWMutex` 保护共享状态
2. **资源清理**：HTTP Transport 需注册到清理管理器，防止内存泄漏
3. **配置隔离**：使用 `DeepCopy()` 创建配置副本，避免并发修改
4. **超时控制**：所有网络操作遵守配置的超时时间
5. **日志追踪**：使用 ProviderID 和 ProviderName 进行日志追踪
6. **错误处理**：检查失败时在 `HealthResult.Errors` 中记录详细错误信息
