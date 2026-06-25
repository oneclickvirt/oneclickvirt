# Provider 包

虚拟化平台统一抽象层，为所有支持的虚拟化/容器平台提供一致的操作接口。

## 目录结构

```
provider/
├── provider.go              # Provider 统一接口定义、DiscoveredInstance 结构体、注册表
├── transport_cleanup.go     # HTTP Transport 清理管理器（防止连接泄漏）
├── docker/                  # Docker 容器提供商实现
├── podman/                  # Podman 容器提供商实现
├── containerd/              # Containerd 容器提供商实现
├── incus/                   # Incus 容器/虚拟机提供商实现
├── lxd/                     # LXD 容器/虚拟机提供商实现
├── proxmox/                 # Proxmox VE 虚拟化提供商实现
├── qemu/                    # QEMU/KVM 虚拟机提供商实现
├── kubevirt/                # KubeVirt 虚拟机提供商实现
├── multipass/               # Multipass 本地/桌面 VM Provider（基于 vmcli）
├── vagrant/                 # Vagrant 本地/桌面 VM Provider（基于 vmcli）
├── virtualbox/              # VirtualBox 本地/桌面 VM Provider（基于 vmcli）
├── vmware/                  # VMware VM Provider
├── vmcli/                   # Multipass/Vagrant/VirtualBox 共用 CLI Provider 抽象
├── firewall/                # 防火墙管理模块（nftables 优先，iptables 兜底）
├── health/                  # 统一健康检查模块
└── portmapping/             # 端口映射模块
    ├── interface.go         # 端口映射接口定义
    ├── manager.go           # 端口映射管理器
    ├── base.go              # 基础实现
    ├── init.go              # 注册初始化
    ├── docker/              # Docker 端口映射
    ├── podman/              # Podman 端口映射
    ├── containerd/          # Containerd 端口映射
    ├── incus/               # Incus 端口映射
    ├── lxd/                 # LXD 端口映射
    └── iptables/            # iptables/nftables 端口映射（通过 firewall.Manager）
```

## 核心概念

### Provider 接口

Provider 是所有虚拟化平台的统一抽象接口，定义了实例管理、镜像管理、连接管理、健康检查、实例发现等标准操作。
在 `connection_type=agent` 时，控制端会注入基于 AgentHub 的 ShellExecutor，Provider 仍然通过统一接口执行命令，但底层由 WebSocket 代理到远端 Agent。

实现或维护 Provider 时需要保持以下约束：

- 所有后端必须实现同一套实例、镜像、连接、健康检查、密码管理和发现接口，新增能力应优先沉淀为接口或可选接口，而不是只在单个后端暴露。
- Provider 实例不得跨 Provider ID 共享可变连接状态；需要复用 HTTP Transport 时必须通过 `TransportCleanupManager` 注册，Provider 删除或重载时按 Provider ID 清理。
- Agent 模式由 `service/provider` 注入 ShellExecutor，避免 Provider 包反向依赖 Agent 服务；Provider 只感知统一的命令执行器。
- 长耗时命令应接受 `context.Context` 并尊重取消信号，避免任务取消后继续操作错误节点或污染后续任务。
- 错误信息需要包含 Provider 类型、实例名或操作阶段等上下文，但不得输出 token、密码、私钥、证书等敏感字段。

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

    // SSH 命令执行
    ExecuteSSHCommand(ctx context.Context, command string) (string, error)

    // 实例发现（纳管已有实例）
    DiscoverInstances(ctx context.Context) ([]DiscoveredInstance, error)
}
```

### 实例发现与导入

每个 Provider 实现了 `DiscoverInstances` 方法，用于发现宿主机上已存在但未被系统管理的实例。发现结果包含完整的实例信息和端口映射。

#### DiscoveredInstance 结构体

```go
type DiscoveredInstance struct {
    UUID         string                  // 实例唯一标识
    Name         string                  // 实例名称
    Status       string                  // 运行状态（running/stopped）
    InstanceType string                  // 实例类型（container/vm）
    CPU          int                     // CPU 核心数
    Memory       int64                   // 内存大小（MB）
    Disk         int64                   // 磁盘大小（MB）
    PrivateIP    string                  // 内网 IPv4
    PublicIP     string                  // 公网 IPv4
    IPv6Address  string                  // IPv6 地址
    SSHPort      int                     // SSH 端口
    ExtraPorts   []int                   // 其他开放端口
    PortMappings []DiscoveredPortMapping // 完整端口映射信息
    MACAddress   string                  // MAC 地址
    Image        string                  // 使用的镜像
    OSType       string                  // 操作系统类型
    RawData      interface{}             // 原始数据（调试用）
}
```

#### DiscoveredPortMapping 结构体

```go
type DiscoveredPortMapping struct {
    HostPort  int    // 宿主机端口
    GuestPort int    // 容器/虚拟机内部端口
    Protocol  string // 协议：tcp/udp/both
    IsSSH     bool   // 是否为 SSH 端口
}
```

### Provider 注册机制

通过 `RegisterProvider` 函数将 Provider 实现注册到全局注册表，各 Provider 的 `init()` 函数在系统启动时自动完成注册。

```go
func init() {
    provider.RegisterProvider("docker", NewDockerProvider)
}
```

### 执行规则

Provider 支持三种执行规则，控制操作的执行方式：

| 规则 | 说明 |
|---|---|
| `api_only` | 仅通过 API 执行操作 |
| `ssh_only` | 仅通过命令执行通道执行操作 |
| `auto` | 优先使用 API，失败时自动回退到命令执行通道 |

## 当前注册的 Provider 类型

| 类型标识 | 实现目录 | 实例类型 | 说明 |
|---|---|---|---|
| `docker` | `docker/` | container | Docker 容器 Provider |
| `orbstack` | `docker/` | container | Orbstack 兼容 Docker CLI，复用 Docker Provider 和端口映射实现 |
| `podman` | `podman/` | container | Podman 容器 Provider |
| `containerd` | `containerd/` | container | Containerd/nerdctl 容器 Provider |
| `incus` | `incus/` | container, vm | Incus Provider |
| `lxd` | `lxd/` | container, vm | LXD Provider |
| `proxmox`, `proxmoxve` | `proxmox/` | container, vm | Proxmox VE Provider，两个类型标识指向同一实现 |
| `qemu` | `qemu/` | vm | QEMU/KVM Provider |
| `kubevirt` | `kubevirt/` | vm | KubeVirt Provider |
| `multipass` | `multipass/` + `vmcli/` | vm | 通过 Multipass CLI 管理 VM |
| `vagrant` | `vagrant/` + `vmcli/` | vm | 通过 Vagrant CLI 管理 VM |
| `virtualbox` | `virtualbox/` + `vmcli/` | vm | 通过 VBoxManage 管理 VM |
| `vmware` | `vmware/` | vm | 通过 VMware CLI/命令执行通道管理 VM |

`multipass`、`vagrant`、`virtualbox` 和 `vmware` 更偏向本地/桌面虚拟化节点或实验场景。生产接入前应确认目标节点的 CLI、网络、镜像路径、端口转发和健康检查能力是否满足当前部署要求。

## 已支持的 Provider

### Docker

基于 Docker 容器技术的 Provider 实现。

- **类型标识**：`docker`、`orbstack`
- **支持实例类型**：`container`
- **连接方式**：SSH / Agent
- **执行方式**：命令执行通道（SSH 或 Agent WebSocket）
- **IPv4 网络**：`docker-net`
- **IPv6 网络**：`docker-ipv6`
- **特性**：容器生命周期管理、镜像拉取/删除、IPv6 支持检测、自动重连、实例发现与导入

### Podman

基于 Podman 容器技术的独立 Provider 实现。

- **类型标识**：`podman`
- **支持实例类型**：`container`
- **连接方式**：SSH / Agent
- **执行方式**：命令执行通道（SSH 或 Agent WebSocket）
- **端口映射方式**：固定使用 `native`
- **IPv4 网络**：`podman-net`（172.20.0.0/16）
- **IPv6 网络**：`podman-ipv6`
- **镜像存储目录**：`/usr/local/bin/podman_ct_images`
- **健康检查服务名**：`podman`
- **镜像命名空间**：`localhost/`（Podman 加载本地 tar 后镜像统一存储在 localhost/ 命名空间下）
- **特性**：容器生命周期管理、镜像管理（singleflight 去重）、LXCFS 资源隔离、btrfs 磁盘限额、registries.conf 自动确认、IPv6 支持检测、自动重连、实例发现与导入

### Containerd

基于 Containerd 容器运行时的 Provider 实现，使用 `nerdctl` CLI。

- **类型标识**：`containerd`
- **支持实例类型**：`container`
- **连接方式**：SSH / Agent
- **执行方式**：命令执行通道（SSH 或 Agent WebSocket）
- **端口映射方式**：固定使用 `native`
- **IPv4 网络**：`containerd-net`
- **IPv6 网络**：`containerd-ipv6`
- **镜像存储目录**：`/usr/local/bin/containerd_ct_images`
- **镜像下载目录**：`/usr/local/bin/containerd_images`
- **健康检查服务名**：`nerdctl`
- **支持的操作系统/架构**：ubuntu/rockylinux/openeuler/debian/alpine/almalinux × amd64/arm64

### Incus

基于 Incus 容器/虚拟机技术的 Provider 实现。

- **类型标识**：`incus`
- **支持实例类型**：`container`、`vm`
- **连接方式**：SSH / Agent + API（可选）
- **执行方式**：根据执行规则选择 API 或命令执行通道
- **特性**：容器和虚拟机管理、证书认证 API、SSH 命令行备用、IPv6 配置、端口映射、Transport 资源自动清理、实例发现

### LXD

基于 LXD 容器/虚拟机技术的 Provider 实现。

- **类型标识**：`lxd`
- **支持实例类型**：`container`、`vm`
- **连接方式**：SSH / Agent + API（可选）
- **执行方式**：根据执行规则选择 API 或命令执行通道
- **特性**：容器和虚拟机管理、证书认证 API、SSH 命令行备用、IPv6 配置、端口映射、Transport 资源自动清理、实例发现

### Proxmox

基于 Proxmox VE 虚拟化平台的 Provider 实现。

- **类型标识**：`proxmox`
- **支持实例类型**：`container`、`vm`
- **连接方式**：SSH / Agent + API
- **执行方式**：根据执行规则选择 API 或命令执行通道
- **特性**：虚拟机生命周期管理、Token 认证 API、SSH 命令行备用、虚拟机配置管理、网络和存储配置、Transport 资源自动清理、实例发现

### QEMU

基于 QEMU/KVM 虚拟化技术的 Provider 实现，通过 SSH 直接执行命令管理虚拟机。

- **类型标识**：`qemu`
- **支持实例类型**：`vm`
- **连接方式**：SSH / Agent
- **执行方式**：命令执行通道（SSH 或 Agent WebSocket）
- **网络**：libvirt NAT（virbr0, 192.168.122.0/24）
- **端口映射**：通过 firewall.Manager（nftables 优先，iptables 兑底）
- **镜像格式**：qcow2 + cloud-init ISO
- **特性**：直接 SSH 执行创建（无需下载脚本）、KVM/TCG 自动回退、qcow2 镜像管理、多方式密码设置（guest-agent/SSH/virt-customize）、nftables/iptables DNAT 端口发现、实例发现

### KubeVirt

基于 KubeVirt 虚拟化技术的 Provider 实现，通过 SSH 直接执行 kubectl YAML 管理虚拟机。

- **类型标识**：`kubevirt`
- **支持实例类型**：`vm`
- **连接方式**：SSH / Agent
- **执行方式**：命令执行通道（SSH 或 Agent WebSocket）
- **K8s 框架**：K3s
- **命名空间**：`kubevirt-vms`
- **端口映射**：NodePort Service + firewall.Manager（nftables 优先，iptables 兑底）
- **镜像格式**：qcow2/img/raw + CDI PVC
- **创建方式**：直接生成 YAML 并 `kubectl apply`（PVC + VirtualMachine + NodePort Service + cloud-init）
- **特性**：直接 YAML 创建（无需下载脚本）、虚拟机生命周期管理、PVC 磁盘发现、NodePort + DNAT 端口发现、实例发现

### 本地/桌面 VM Provider

本地/桌面虚拟化 Provider 主要用于开发、验证或轻量节点接入。`multipass`、`vagrant` 和 `virtualbox` 复用 `vmcli` 抽象，通过目标节点上的 CLI 执行实例生命周期、镜像和健康检查相关操作；`vmware` 使用独立实现，但同样暴露统一 Provider 接口。

- **类型标识**：`multipass`、`vagrant`、`virtualbox`、`vmware`
- **支持实例类型**：`vm`
- **连接方式**：SSH / Agent
- **执行方式**：命令执行通道（SSH 或 Agent WebSocket）
- **注意事项**：此类 Provider 对宿主机 CLI、镜像目录、网络模式和端口转发配置依赖更强，生产环境使用前需要单独验证节点能力。

## 子模块

### firewall/

防火墙管理模块，为所有 Provider 提供统一的 DNAT 端口转发管理。支持 nftables 优先、iptables 兑底的双后端模式。

- **后端检测**：通过 marker 文件自动识别当前节点的防火墙后端
- **nftables 模式**：每个 Provider 独立的 nft 表（qemu/kubevirt/proxmox/incus/lxd）
- **iptables 模式**：传统 iptables DNAT 规则，通过 comment 追踪规则
- **持久化**：nftables 规则保存到 `/etc/nftables.d/{table}.nft`，iptables 使用 `iptables-save`
- **主要 API**：`AddDNAT`, `AddSingleDNAT`, `RemoveSingleDNAT`, `DeleteRulesByComment`, `DeleteRulesByIP`, `DiscoverDNATRules`, `SaveRules`

### health/

健康检查模块，为所有 Provider 提供统一的健康检查能力。支持 SSH 连接检查、API 服务检查、资源信息采集和存储池路径探测。详见 [health/README.md](health/README.md)。

### portmapping/

端口映射模块，为不同 Provider 提供统一的端口映射管理接口。

- **支持的映射方法**：native（Provider 原生）、iptables/nftables（通过 firewall.Manager）
- **支持的 Provider**：Docker、Orbstack、Podman、Containerd、Incus、LXD、iptables（通用，用于 QEMU/KubeVirt 等）
- **功能**：端口分配、映射创建/删除、冲突检测
- **架构**：接口定义 + Manager 统一管理 + 各 Provider 独立实现

## 新增 Provider 类型的指南

### 步骤 1：创建 Provider 目录

在 `server/provider/` 下创建新的目录，目录名为 Provider 类型（小写）。

```bash
mkdir server/provider/newprovider
```

### 步骤 2：实现 Provider 接口

创建主文件 `newprovider.go`，实现完整的 Provider 接口。

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

// 实现其他接口方法...
```

### 步骤 3：实现连接管理

```go
func (n *NewProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
    n.config = config
    // 建立 SSH 连接
    // 初始化健康检查器
    n.connected = true
    return nil
}

func (n *NewProvider) Disconnect(ctx context.Context) error {
    // 释放连接资源
    n.connected = false
    return nil
}

func (n *NewProvider) IsConnected() bool {
    return n.connected
}
```

### 步骤 4：实现实例发现

```go
func (n *NewProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
    // 通过 SSH 或 API 获取宿主机上的实例列表
    // 解析资源配置、网络信息、端口映射
    // 返回 DiscoveredInstance 列表（含 DiscoveredPortMapping）
    return instances, nil
}
```

### 步骤 5：实现健康检查

如果需要特定的健康检查逻辑，在 `server/provider/health/` 下创建对应的健康检查器。

### 步骤 6：注册 Provider

在主文件末尾添加 `init` 函数进行注册：

```go
func init() {
    provider.RegisterProvider("newprovider", NewNewProvider)
}
```

### 步骤 7：端口映射支持（可选）

如需支持端口映射，在 `server/provider/portmapping/` 下创建对应的实现，并在 `init.go` 中注册。

### 步骤 8：测试

创建单元测试和集成测试验证实现的正确性。

## 注意事项

### 连接管理

- 实现 SSH 连接健康检查和自动重连机制
- 如使用 API 连接，注意 Transport 资源的清理
- 使用 `transport_cleanup.go` 中的管理器注册 HTTP Transport

### 错误处理

- 区分连接错误和业务错误
- 提供清晰的错误信息
- 实现重试机制处理临时性故障

### 日志记录

- 使用 `global.APP_LOG` 记录关键操作
- 敏感信息使用 `utils.TruncateString` 截断
- 区分 Debug、Info、Warn、Error 日志级别

### 并发安全

- 使用 `sync.RWMutex` 保护共享状态
- 注意 SSH 客户端和 API 客户端的并发访问

### 资源清理

- 实现 `Disconnect` 方法释放所有资源
- 使用 Transport 清理管理器管理 HTTP 连接
- 避免资源泄漏

## 工具函数

### Transport 清理管理器

```go
// 注册 Transport
provider.GetTransportCleanupManager().RegisterTransport(transport)

// 关联 Provider ID
provider.GetTransportCleanupManager().RegisterTransportWithProvider(transport, providerID)

// 清理特定 Provider 的 Transport
provider.GetTransportCleanupManager().CleanupProvider(providerID)
```

### SSH 客户端

```go
// 创建
client, err := utils.NewSSHClient(sshConfig)

// 执行命令
output, err := client.Execute(command)

// 带日志的执行
output, err := client.ExecuteWithLogging(command, "OPERATION_NAME")

// 健康检查
healthy := client.IsHealthy()

// 重连
err := client.Reconnect()
```
