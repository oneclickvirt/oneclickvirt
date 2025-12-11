# Provider Health Check 模块

## 概述

本模块实现了针对不同虚拟化提供商（Provider）的统一健康检查机制，支持SSH连接检测、API服务检测、资源信息采集和存储池路径探测等功能。

## 架构设计

### 核心组件

- **interface.go**: 定义健康检查的核心接口和数据结构
  - `HealthChecker`: 健康检查器接口
  - `HealthResult`: 健康检查结果
  - `HealthConfig`: 健康检查配置
  - `ResourceInfo`: 节点资源信息

- **manager.go**: 健康检查管理器，负责创建和管理不同类型的健康检查器
  - 支持的提供商类型: Docker、LXD、Incus、Proxmox
  - 提供统一的检查器创建接口

- **base.go**: 基础健康检查器实现
  - 提供HTTP客户端管理（带连接池）
  - 实现通用的检查逻辑
  - 处理并发配置更新

- **factory.go**: 工厂方法和适配器
  - 提供便捷的检查器创建函数
  - 适配器模式集成到现有系统

- **utils.go**: 辅助工具和SSH命令执行
  - 提供SSH连接管理
  - 实现资源信息采集逻辑
  - 健康检查结果解析

- **storage_detection.go**: 存储池路径自动检测
  - 根据提供商类型自动检测存储池实际挂载路径
  - 支持Proxmox、LXD、Incus、Docker

### 具体实现

- **docker.go**: Docker提供商健康检查实现
- **lxd.go**: LXD提供商健康检查实现
- **incus.go**: Incus提供商健康检查实现
- **proxmox.go**: Proxmox提供商健康检查实现

## 工作流程

1. 通过`HealthManager`创建指定类型的健康检查器
2. 配置检查参数（SSH、API、超时等）
3. 调用`CheckHealth()`执行检查
4. 返回包含状态、资源信息、错误等的`HealthResult`

## 检查内容

每个健康检查器可执行以下检查项：

- **SSH连接检查**: 验证SSH服务可达性和认证
- **API服务检查**: 验证提供商API服务状态
- **服务状态检查**: 检查特定系统服务运行状态
- **资源信息采集**: CPU、内存、磁盘、存储池路径等
- **主机名获取**: 节点hostname用于区分多节点环境

## 使用示例

### 基本用法

```go
// 创建健康检查管理器
manager := NewHealthManager(logger)

// 配置检查参数
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

// 创建Docker检查器
checker, err := manager.CreateChecker(ProviderTypeDocker, config)
if err != nil {
    return err
}

// 执行健康检查
result, err := checker.CheckHealth(context.Background())
if err != nil {
    return err
}

// 处理检查结果
fmt.Printf("Status: %s\n", result.Status)
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

## 新增提供商实现指南

### 步骤1: 创建提供商特定的健康检查器文件

创建新文件 `<provider_name>.go`，实现以下结构：

```go
package health

import (
    "context"
    "go.uber.org/zap"
)

// <Provider>HealthChecker <提供商名称>健康检查器
type <Provider>HealthChecker struct {
    *BaseHealthChecker
}

// New<Provider>HealthChecker 创建检查器
func New<Provider>HealthChecker(config HealthConfig, logger *zap.Logger) *<Provider>HealthChecker {
    return &<Provider>HealthChecker{
        BaseHealthChecker: NewBaseHealthChecker(config, logger),
    }
}

// CheckHealth 实现健康检查逻辑
func (c *<Provider>HealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
    // 定义需要执行的检查项
    checks := []func(context.Context) CheckResult{}
    
    // SSH检查
    if c.config.SSHEnabled {
        checks = append(checks, c.createCheckFunc(CheckTypeSSH, c.checkSSH))
    }
    
    // API检查
    if c.config.APIEnabled {
        checks = append(checks, c.createCheckFunc(CheckTypeAPI, c.checkAPI))
    }
    
    // 执行检查并返回结果
    result := c.executeChecks(ctx, checks)
    return result, nil
}

// checkAPI 实现提供商特定的API检查逻辑
func (c *<Provider>HealthChecker) checkAPI(ctx context.Context) error {
    // 实现具体的API检查逻辑
    // 例如: 调用提供商的健康检查API端点
    return nil
}
```

### 步骤2: 在manager.go中注册新提供商

在 `manager.go` 的 `CreateChecker()` 方法中添加新的case分支：

```go
case ProviderType<Provider>:
    if configCopy.APIPort == 0 {
        configCopy.APIPort = <默认API端口>
    }
    checker = New<Provider>HealthChecker(configCopy, hm.logger)
    checkerTypeName = "<Provider>HealthChecker"
```

在 `ProviderType` 常量中添加新类型：

```go
const (
    // ...现有类型...
    ProviderType<Provider> ProviderType = "<provider_name>"
)
```

### 步骤3: 实现存储池路径检测（可选）

在 `storage_detection.go` 中添加检测方法：

```go
// detect<Provider>StoragePath 检测<提供商>存储池路径
func (phc *ProviderHealthChecker) detect<Provider>StoragePath(client *ssh.Client, storagePoolName string) (string, error) {
    // 实现存储池路径检测逻辑
    // 可通过SSH命令查询或配置文件解析
    return "/path/to/storage", nil
}
```

在 `DetectStoragePoolPath()` 中添加case分支：

```go
case "<provider_name>":
    return phc.detect<Provider>StoragePath(client, storagePoolName)
```

### 步骤4: 实现资源信息采集（可选）

如果提供商有特殊的资源获取方式，在新文件中实现：

```go
// getResourceInfo 获取资源信息
func (c *<Provider>HealthChecker) getResourceInfo(ctx context.Context) (*ResourceInfo, error) {
    // 通过SSH或API获取CPU、内存、磁盘等信息
    return &ResourceInfo{
        CPUCores:    cpuCount,
        MemoryTotal: memTotal,
        DiskTotal:   diskTotal,
        DiskFree:    diskFree,
    }, nil
}
```

在 `CheckHealth()` 中调用资源信息采集：

```go
if result.Status == HealthStatusHealthy {
    if resourceInfo, err := c.getResourceInfo(ctx); err == nil {
        result.ResourceInfo = resourceInfo
    }
}
```

## 注意事项

1. **并发安全**: 所有健康检查器需要支持并发调用，使用`sync.RWMutex`保护共享状态
2. **资源清理**: HTTP Transport需要注册到清理管理器，防止内存泄漏
3. **配置隔离**: 使用`DeepCopy()`创建配置副本，避免并发修改
4. **超时控制**: 所有网络操作应遵守配置的超时时间
5. **日志追踪**: 使用ProviderID和ProviderName进行日志追踪，便于问题排查
6. **错误处理**: 检查失败时应在`HealthResult.Errors`中记录详细错误信息

## 配置参数说明

### 基础连接配置
- `Host`: 提供商主机地址
- `Port`: SSH端口（默认22）
- `Username`: SSH用户名
- `Password`: SSH密码
- `PrivateKey`: SSH私钥（优先于密码）

### API配置
- `APIEnabled`: 是否启用API检查
- `APIPort`: API服务端口
- `APIScheme`: API协议（http/https）
- `SkipTLSVerify`: 是否跳过TLS证书验证
- `Token`: API访问令牌
- `CertPath/CertContent`: TLS证书路径或内容
- `KeyPath/KeyContent`: TLS密钥路径或内容

### 检查配置
- `Timeout`: 检查超时时间
- `SSHEnabled`: 是否启用SSH检查
- `ServiceChecks`: 需要检查的系统服务列表
- `CustomCommands`: 自定义检查命令

## 健康状态定义

- `HealthStatusHealthy`: 所有检查项通过
- `HealthStatusUnhealthy`: 存在检查项失败
- `HealthStatusPartial`: 部分检查项通过
- `HealthStatusUnknown`: 无法确定健康状态
