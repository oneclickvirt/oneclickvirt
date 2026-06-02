# oneclickvirt-agent

基于 Rust 开发的轻量级宿主机监控代理，负责实例级别的网络流量监控、资源使用情况采集、**域名反向代理**和内容过滤规则管理。

代理运行在每个 Provider 宿主机上，通过 HTTP API 供中心管理服务器管理监控器、查询流量数据、获取资源指标、管理内容过滤规则，**并内置高性能 HTTP 反向代理服务器，完全无需依赖 Nginx**。

## 总体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                     管理服务器（Go 后端）                         │
│  service/agent/client.go  ──>  HTTP API 调用                    │
└───────────────────────────────┬─────────────────────────────────┘
                                │ HTTP (x-token 认证)
                                ▼ :23782
┌─────────────────────────────────────────────────────────────────┐
│               oneclickvirt-agent（Rust/Axum）                    │
│                                                                  │
│  ┌──────────────────┐  ┌───────────────────┐  ┌──────────────┐ │
│  │   流量监控模块    │  │   资源采集模块     │  │  域名代理模块 │ │
│  │  nft/ + ipt.rs   │  │   resource.rs      │  │   proxy.rs   │ │
│  └────────┬─────────┘  └────────┬──────────┘  └──────┬───────┘ │
│           │                     │                      │         │
│  ┌────────▼─────────┐  ┌────────▼──────────┐  ┌──────▼───────┐ │
│  │   nftables /       │  │  Docker/Podman/   │  │  纯 Rust 实现 │ │
│  │   iptables 规则   │  │  Containerd/LXD/  │  │  HTTP 反向代理│ │
│  └────────┬─────────┘  │  Incus/Proxmox     │  │  (Hyper客户端)│ │
│           │             └────────┬──────────┘  └──────────────┘ │
│           │                      │                   ▲           │
│           │                      │                   │ :80      │
│  ┌────────▼──────────────────────▼───────────────────┘          │
│  │                 SQLite (traffic.db)                        │  │
│  │  monitors / interface_states / resource_metrics /          │  │
│  │  domain_proxies                                            │  │
│  └────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
        │
        │ 用户 HTTP 请求 :80
        ▼
  ┌──────────────────┐
  │  based on Host   │
  │  domain routing  │──> 内部实例 IP:Port
  └──────────────────┘
```

## 核心功能

- **双模式流量监控**：支持 nftables（nft）和 iptables（ipt）两种流量采集方式，通过环境变量切换
- **资源采集**：支持 Docker/Podman/Containerd/LXD/Incus/Proxmox 六种虚拟化平台的实例资源监控
- **双控制通道**：支持控制端直连 Agent HTTP API，也支持 Agent 主动连接控制端的反向 WebSocket 模式
- **连接复用安全**：控制端按 Provider ID 缓存 Agent 客户端，节点重载或删除时会清理旧客户端与运行时连接，避免旧 token、旧 host 或旧 WebSocket 上下文误复用
- **纯 Rust 反向代理**：内置基于 Hyper 的高性能 HTTP/HTTPS 反向代理服务器，支持：
  - 基于 Host 头的动态域名路由
  - HTTP 和 HTTPS 双协议支持
  - TLS/SSL 终端（支持自签名和 CA 签名证书）
  - HTTP/1.1 和 HTTP/2 自动协商
  - WebSocket 透明支持
  - 实时路由热更新（无需重启）
  - **可选启用**：默认关闭，避免端口冲突
  - **完全无需依赖 Nginx**
- **内容过滤（Block Rules）**：基于 nftables 原生规则的出站流量内容过滤，支持挖矿、BT、测速等行为的阻断
- **Swagger API 文档**：内置 Swagger UI，方便 API 探索和调试
- **数据持久化**：SQLite 本地存储，支持重启恢复

## 控制端集成模式

| 模式 | 适用场景 | 说明 |
|---|---|---|
| HTTP API | 控制端可以直接访问 Provider 宿主机的 Agent 端口 | 控制端通过共享 HTTP client 调用 Agent API，并使用 `x-token` 鉴权。 |
| 反向 WebSocket | Provider 位于 NAT/防火墙后或不开放 SSH/HTTP 端口 | Agent 使用 `agent_secret` 主动连接控制端，控制端通过 WebSocket 执行命令、文件管理、终端和资源同步。 |

Agent 模式下，控制端会在 Provider 删除、重载或连接参数变化时刷新缓存客户端，并清理对应的 SSH/Transport/Agent 运行时资源。执行长命令时会优先使用 detached/polling 模式，避免单个长任务阻塞 WebSocket 控制通道。

## 系统要求

- Linux 系统
- Root 权限（nftables/iptables 操作需要）
- 流量监控依赖：
  - **nft 模式**（默认）：需要安装 nftables（`nft` 命令可用），内核 >= 3.14
  - **ipt 模式**：需要安装 iptables（`iptables` 命令可用）
- 内容过滤依赖：需要 nftables（`nft` 命令可用）
- **反向代理**：无需安装任何第三方软件，纯 Rust 实现
- Rust 1.75+（编译需要）

## 配置说明

通过环境变量或工作目录下的 `.env` 文件进行配置。

| 变量名 | 必填 | 说明 |
|---|---|---|
| `API_TOKEN` | 是 | 认证令牌。所有 API 请求必须在 `x-token` 请求头中包含此值。 |
| `TRAFFIC_COLLECT_METHOD` | 否 | 流量采集方式：`nft`（默认，使用 nftables）或 `ipt`（使用 iptables）。 |
| `TRAFFIC_COLLECT_INTERVAL` | 否 | 流量采集间隔，单位秒（默认：`5`）。 |
| `RESOURCE_COLLECT_INTERVAL` | 否 | 资源采集间隔，单位秒（默认：`30`）。 |
| `ENABLE_REVERSE_PROXY` | 否 | 是否启用反向代理功能（默认：`false`）。设为 `true` 才会启动反向代理服务器。 |
| `PROXY_HTTP_ADDR` | 否 | HTTP 反向代理监听地址（如：`0.0.0.0:80`）。仅当 `ENABLE_REVERSE_PROXY=true` 时生效。 |
| `PROXY_HTTPS_ADDR` | 否 | HTTPS 反向代理监听地址（如：`0.0.0.0:443`）。需同时配置证书。 |
| `PROXY_TLS_CERT` | 否 | TLS 证书文件路径（启用 HTTPS 时必填）。支持 PEM 格式。 |
| `PROXY_TLS_KEY` | 否 | TLS 私钥文件路径（启用 HTTPS 时必填）。支持 PEM 格式的 PKCS#8 私钥。 |
| `EXTRA_EXCLUDE_CIDRS_V4` | 否 | 逗号分隔的额外排除 IPv4 CIDR 列表（不计入流量统计）。 |
| `EXTRA_EXCLUDE_CIDRS_V6` | 否 | 逗号分隔的额外排除 IPv6 CIDR 列表（不计入流量统计）。 |
| `RUST_LOG` | 否 | 日志级别过滤器（默认：`info`）。示例：`debug`、`warn`、`oneclickvirt_agent=debug`。 |

## 编译构建

```bash
cargo build --release
```

输出二进制文件位于 `target/release/oneclickvirt-agent`。

交叉编译静态链接的 Linux 目标：

```bash
# amd64
cargo build --release --target x86_64-unknown-linux-musl

# arm64（需要 musl 交叉编译工具链）
CC_aarch64_unknown_linux_musl=aarch64-linux-musl-gcc \
CARGO_TARGET_AARCH64_UNKNOWN_LINUX_MUSL_LINKER=aarch64-linux-musl-gcc \
cargo build --release --target aarch64-unknown-linux-musl
```

## 运行

```bash
API_TOKEN=your-secret-token ./oneclickvirt-agent
```

**默认情况下，仅启动 API 服务器（端口 23782）**。如需启用反向代理功能，需设置 `ENABLE_REVERSE_PROXY=true`：

```bash
# 仅 HTTP 反向代理
ENABLE_REVERSE_PROXY=true PROXY_HTTP_ADDR=0.0.0.0:80 API_TOKEN=your-token ./oneclickvirt-agent

# 仅 HTTPS 反向代理
ENABLE_REVERSE_PROXY=true \
  PROXY_HTTPS_ADDR=0.0.0.0:443 \
  PROXY_TLS_CERT=/path/to/cert.pem \
  PROXY_TLS_KEY=/path/to/key.pem \
  API_TOKEN=your-token \
  ./oneclickvirt-agent

# HTTP + HTTPS 双协议
ENABLE_REVERSE_PROXY=true \
  PROXY_HTTP_ADDR=0.0.0.0:80 \
  PROXY_HTTPS_ADDR=0.0.0.0:443 \
  PROXY_TLS_CERT=/path/to/cert.pem \
  PROXY_TLS_KEY=/path/to/key.pem \
  API_TOKEN=your-token \
  ./oneclickvirt-agent
```

**服务器启动说明**：

- **仅 API 服务器**（默认）：监听 `0.0.0.0:23782`，用于管理接口
- **启用反向代理后**：
  - API 服务器继续在 23782 端口运行
  - 根据配置启动 HTTP（默认 80）和/或 HTTPS（默认 443）反向代理服务器
  - 反向代理服务器接收用户的域名访问请求

Swagger UI 可通过 `http://<host>:23782/swagger-ui/` 访问（Swagger UI 端点无需认证）。

## 技术栈

| 组件 | 说明 |
|---|---|
| axum 0.7 | HTTP 框架 |
| tokio | 异步运行时 |
| rusqlite (bundled SQLite3) | 本地数据持久化 |
| utoipa + utoipa-swagger-ui | OpenAPI 文档与 Swagger UI |
| tracing + tracing-subscriber | 结构化日志 |
| subtle | 常量时间 token 比较（防时间侧信道） |
| dotenvy | `.env` 文件加载 |
| hyper + hyper-util + http-body-util | HTTP 反向代理核心引擎 |
| tower | 中间件服务抽象层 |
| axum-server (tls-rustls) | HTTPS 服务支持 |
| tokio-rustls + rustls + rustls-pemfile | TLS/SSL 栈 |
| regex | 正则匹配 |
| serde + serde_json | JSON 序列化/反序列化 |

## 项目结构

```
src/
├── main.rs          # 入口：配置加载、数据库初始化、路由注册、HTTP 服务启动
├── app_state.rs     # 应用全局状态（数据库连接、配置项）
├── auth.rs          # x-token 认证中间件（常量时间比较）
├── db.rs            # SQLite 数据库初始化和辅助函数
├── models.rs        # 请求/响应数据结构定义
├── handlers.rs      # API 路由处理函数
├── proxy.rs         # 域名反向代理服务器（Hyper HTTP/HTTPS 反向代理）
├── nft/             # nftables 流量监控 + Block Rules 模块
│   ├── mod.rs       # 模块入口
│   ├── block.rs     # 屏蔽规则逻辑
│   ├── counter.rs   # nftables 计数器操作
│   └── gc.rs        # 过期规则清理
├── ipt.rs           # iptables 流量监控实现
├── collector.rs     # 后台定时采集任务（流量 + 资源）
├── resource.rs      # 多平台资源采集（Docker/Podman/Containerd/LXD/Incus/Proxmox）
├── docs.rs          # OpenAPI 文档定义
└── error.rs         # 统一错误类型
```

## 数据存储

流量数据和资源指标存储在工作目录下的 SQLite 数据库文件 `traffic.db` 中。

### 数据库表结构

- **monitors**：监控器主表，存储接口列表、累计流量（总计/入站/出站）、provider_kind、instance_name、inner_ip
- **interface_states**：各接口的计数器状态（last_counter_in/out，用于增量计算）
- **resource_metrics**：资源监控历史数据（CPU/内存/磁盘），按 monitor_id + timestamp 索引
- **domain_proxies**：域名反向代理记录，存储域名、内部 IP:Port、协议、SSL 开关

### 数据保留策略

- 流量计数器无限累积（直到监控器被删除或清理）
- 资源指标保留 24 小时，自动清理过期数据
- 超过 30 天未更新的过期监控器自动清理

## 流量监控架构

### nft 模式（默认）

使用 nftables 命名计数器追踪流量，在 inet 族的 `vm_traffic_monitor` 表的 `forward` 链中创建规则：

- 对每个被监控的网络接口，创建入站和出站的 IPv4/IPv6 流量计数规则
- 排除私有和特殊用途地址段（RFC1918、RFC6598、环回、链路本地、组播等）
- 支持按实例内网 IP（`inner_ip`）进行精确的单 IP 流量过滤

### ipt 模式

使用 iptables 自定义链和规则追踪流量：

- 为每个监控器创建独立的入站（`VM_IN_<id>_<hash>`）和出站（`VM_OUT_<id>_<hash>`）链
- 在 `VM_TRAFFIC_FWD` 主链中按接口名分发到各监控器子链
- 排除 RFC1918 等私有地址段，仅统计公网流量
- 支持通过 `EXTRA_EXCLUDE_CIDRS_V4` 添加额外排除段

### 模式切换

通过 `TRAFFIC_COLLECT_METHOD` 环境变量选择：

- `nft`（默认）：推荐用于内核支持 nftables 的现代系统
- `ipt`：用于仅支持 iptables 的旧系统

### 采集流程

流量计数器按配置的间隔（默认每 5 秒）读取计数器值，计算增量并累积到 SQLite 数据库中。采集逻辑统一处理两种模式，根据 `traffic_collect_method` 调用 `nft::read_external_bytes` 或 `ipt::read_external_bytes`。

### 网卡变更处理

当实例重建或使用新网络接口重启时：

1. 管理服务器调用 `POST /api/v1/update` 传入新的接口名称
2. 旧的规则和计数器被移除，创建新的
3. 累积的 `total_bytes` 值被保留（不会重置）
4. 如果检测到计数器被重置（当前值 < 上次值），仅将当前值作为增量添加，防止产生负数差值

### 接口别名（nft 模式）

如果某个接口有主桥接（通过 `/sys/class/net/<interface>/master` 检测），会同时为接口名和其桥接主设备名创建规则。

## 资源监控

当创建监控器时指定了 `provider_kind` 和 `instance_name` 字段，代理将按照配置的间隔（默认每 30 秒）采集 CPU、内存和磁盘使用情况。

### 各平台采集方式

| 提供商 | 采集方式 |
|---|---|
| Docker | `docker stats --no-stream` + `docker inspect` |
| Podman | `podman stats --no-stream` + `podman inspect` |
| Containerd | `nerdctl stats` + cgroup v2 回退方案 |
| LXD | `lxc info` + `lxc config show` + `lxc config device show` |
| Incus | `incus info` + `incus config show` + `incus config device show` |
| Proxmox VE | `pvesh get /nodes/<node>/lxc\|qemu/<vmid>/status/current` |

### 更新行为

通过 `update` 接口更新监控器的网卡/实例信息时，**不会重置**之前已采集的资源历史记录。资源指标以 `monitor_id` 为关联键，在 update 操作中保持不变。

## 内容过滤（Block Rules）

基于 nftables 原生规则实现出站流量内容过滤，用于阻止挖矿、BT 下载、测速等滥用行为。

### 工作原理

1. 创建独立的 nft 表 `inet abuse_block`，包含 `block_output` 和 `block_forward` 两个链
2. 对每个需要过滤的字符串，转换为十六进制后通过 `@th` 原始载荷匹配
3. 分别在 TCP（载荷偏移 160 位）和 UDP（载荷偏移 64 位）上添加匹配规则
4. 匹配到的数据包将被 DROP 并计数
5. 支持按 IP 协议版本过滤（ipv4/ipv6/both）

### 规则持久化

- 规则列表持久化到 `/opt/oneclickvirt/agent/block_rules.json`
- 代理启动时自动从文件恢复规则
- 删除规则时同时清理文件和 nft 表

## 域名反向代理

**✨ 完全基于 Rust 实现，无需安装 Nginx**。Agent 内置基于 Hyper 的高性能 HTTP/HTTPS 反向代理服务器，根据请求的 Host 头动态路由到对应的内部实例服务。

### 工作流程

```
用户域名请求
    │  (DNS A记录 → 节点公网IP:80/443)
    ▼
Rust 反向代理服务器 (:80/:443)
    │  (基于 Host 头查询路由表)
    ▼
容器/虚拟机内网 IP:Port
    │  (Hyper HTTP Client 转发)
    ▼
实例内部服务
```

### 特性

- **零依赖**：无需安装、配置任何第三方软件
- **默认关闭**：需显式启用（`ENABLE_REVERSE_PROXY=true`），避免不必要的端口冲突
- **双协议支持**：可同时或单独启用 HTTP 和 HTTPS
- **TLS/SSL 终端**：完整的 TLS 1.2/1.3 支持，使用 rustls 实现
- **热更新**：添加/删除域名路由立即生效，无需重启服务
- **高性能**：基于 Hyper 异步 HTTP 客户端，支持 HTTP/1.1 和 HTTP/2
- **WebSocket 支持**：自动处理 Upgrade 头，透明支持 WebSocket 协议
- **健康检查**：上游连接失败时返回 502 Bad Gateway

### 路由存储

- 域名路由记录持久化到 SQLite 数据库 `traffic.db` 的 `domain_proxies` 表
- 启动时自动加载所有路由到内存（`Arc<RwLock<HashMap<String, ProxyTarget>>>`）
- 通过 API 添加/删除域名时同时更新数据库和内存路由表

### 配置示例

**添加域名反向代理：**

```bash
curl -X POST http://node-ip:23782/api/v1/domain-proxy \
  -H "x-token: your-api-token" \
  -H "Content-Type: application/json" \
  -d '{
    "domain": "app.example.com",
    "internal_ip": "172.17.0.3",
    "internal_port": 8080,
    "protocol": "http"
  }'
```

**访问：**

1. 配置 DNS A 记录：`app.example.com` → 节点公网 IP
2. 用户访问 `http://app.example.com` 或 `https://app.example.com`（如启用 HTTPS）
3. 反向代理服务器根据 Host 头 `app.example.com` 查询路由表
4. 转发请求到 `http://172.17.0.3:8080`
5. 返回响应给用户

### TLS 证书配置

支持两种证书配置方式：

**1. 通过环境变量（手动配置）：**

```bash
ENABLE_REVERSE_PROXY=true \
  PROXY_HTTPS_ADDR=0.0.0.0:443 \
  PROXY_TLS_CERT=/etc/oneclickvirt/ssl/cert.pem \
  PROXY_TLS_KEY=/etc/oneclickvirt/ssl/key.pem \
  API_TOKEN=your-token \
  ./oneclickvirt-agent
```

**2. 通过主控面板配置（自动同步）：**

在主控面板的节点管理 → 高级设置 → 域名反向代理配置中：
- 启用域名绑定功能
- 启用 HTTPS 反向代理
- 配置证书路径（节点上的绝对路径）
- 主控会自动同步配置到节点的 agent

**证书要求**：
- 证书格式：PEM 格式（文本格式，Base64 编码）
- 私钥格式：PEM 格式的 PKCS#8 私钥
- 支持自签名证书和 CA 签名证书
- 支持通配符证书（`*.example.com`）
- 证书和私钥文件需对 agent 进程可读

**生成自签名证书示例：**

```bash
# 生成私钥
openssl genpkey -algorithm RSA -out key.pem -pkeyopt rsa_keygen_bits:2048

# 生成自签名证书（有效期365天）
openssl req -new -x509 -key key.pem -out cert.pem -days 365 \
  -subj "/CN=*.example.com"
```

### 监听端口配置

反向代理服务器可灵活配置监听地址：

```bash
# 仅 HTTP（端口 80）
PROXY_HTTP_ADDR=0.0.0.0:80

# 仅 HTTPS（端口 443）
PROXY_HTTPS_ADDR=0.0.0.0:443

# 自定义端口（无需 root 权限）
PROXY_HTTP_ADDR=0.0.0.0:8080
PROXY_HTTPS_ADDR=0.0.0.0:8443
```

**注意**：绑定 80 和 443 端口需要 root 权限或 `CAP_NET_BIND_SERVICE` 能力。

## API 参考

除 `/swagger-ui/` 外，所有端点都需要 `x-token` 请求头进行认证。

### POST /api/v1/add

为一个或多个网络接口创建新的流量监控器。

**请求：**
```json
{
  "interface": "veth1001i0",
  "provider_kind": "proxmox",
  "instance_name": "1001",
  "inner_ip": "172.17.0.3"
}
```

`interface` 字段接受单个字符串或字符串数组（用于多网卡监控）：
```json
{
  "interface": ["veth1001i0", "veth1001i1"],
  "provider_kind": "docker",
  "instance_name": "my-container"
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `interface` | string / string[] | 是 | 监控的网络接口名称 |
| `provider_kind` | string | 否 | 虚拟化类型：docker/podman/containerd/lxd/incus/proxmox |
| `instance_name` | string | 否 | 实例名称（配合 provider_kind 启用资源采集） |
| `inner_ip` | string | 否 | 实例内网 IP（用于按 IP 精确过滤流量） |

**响应：**
```json
{
  "id": 1,
  "interface": ["veth1001i0"]
}
```

### POST /api/v1/update

替换已有监控器的监控接口。累积的流量和资源历史不会被重置。

**请求：**
```json
{
  "id": 1,
  "new_interface": ["veth1001i0", "veth1001i1"],
  "provider_kind": "proxmox",
  "instance_name": "1001",
  "inner_ip": "172.17.0.3"
}
```

**响应：**
```json
{
  "id": 1,
  "interface": ["veth1001i0", "veth1001i1"]
}
```

### POST /api/v1/delete

删除监控器及其关联的流量规则。

**请求：**
```json
{
  "id": 1
}
```

**响应：**
```json
{
  "id": 1,
  "deleted": true
}
```

### POST /api/v1/info

查询监控器的流量使用情况。添加 `?human=1` 查询参数可同时返回人类可读的流量值。

**请求：**
```json
{
  "id": 1
}
```

**响应：**
```json
{
  "id": 1,
  "interface": ["veth1001i0"],
  "used_traffic": 1073741824,
  "used_traffic_in": 536870912,
  "used_traffic_out": 536870912,
  "used_traffic_human": "1.00G",
  "last_update_time": 1711929600
}
```

### POST /api/v1/resources

查询监控器的资源监控历史数据。

**请求：**
```json
{
  "id": 1,
  "limit": 288
}
```

**响应：**
```json
{
  "id": 1,
  "data": [
    {
      "timestamp": 1711929600,
      "cpu_percent": 15.3,
      "memory_used": 134217728,
      "memory_total": 536870912,
      "disk_used": 1073741824,
      "disk_total": 10737418240
    }
  ]
}
```

### POST /api/v1/cleanup

删除在指定时间段内未更新的监控器。

**请求：**
```json
{
  "max_update_time": "7d"
}
```

时长格式支持组合：`3d12h30m45s`。

**响应：**
```json
{
  "deleted": 5,
  "max_update_seconds": 604800
}
```

### GET /api/v1/list

列出所有监控器。

**响应：**
```json
{
  "monitors": [
    {
      "id": 1,
      "interface": ["veth1001i0"],
      "provider_kind": "docker",
      "instance_name": "my-container",
      "total_bytes": 1073741824,
      "updated_at": 1711929600
    }
  ],
  "total": 1
}
```

### POST /api/v1/block-rules

应用内容过滤规则。

**请求：**
```json
{
  "strings": ["stratum+tcp", ".torrent", "speedtest"]
}
```

**响应：**
```json
{
  "applied": 3
}
```

### DELETE /api/v1/block-rules

移除所有内容过滤规则。

**响应：**
```json
{
  "removed": true
}
```

### GET /api/v1/block-rules

获取当前生效的内容过滤规则列表。

**响应：**
```json
{
  "strings": ["stratum+tcp", ".torrent", "speedtest"],
  "count": 3
}
```

### POST /api/v1/domain-proxy

为指定域名添加反向代理路由，将域名流量转发到容器/虚拟机内网 IP:Port。路由记录保存到数据库并立即加载到内存，**无需重启服务**。

**请求：**
```json
{
  "domain": "app.example.com",
  "internal_ip": "172.17.0.3",
  "internal_port": 8080,
  "protocol": "http",
  "enable_ssl": false
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `domain` | string | 是 | 完整域名（FQDN），须符合域名格式 |
| `internal_ip` | string | 是 | 容器/虚拟机内网 IP |
| `internal_port` | integer | 是 | 容器/虚拟机内部端口（1-65535） |
| `protocol` | string | 否 | `http`（默认）或 `https`，指定上游协议 |
| `enable_ssl` | boolean | 否 | 保留字段（向后兼容），默认 `false` |

**响应：**
```json
{
  "domain": "app.example.com",
  "status": "active"
}
```

> **说明**：反向代理支持 HTTP 和 HTTPS。是否启用 HTTPS 由环境变量 `PROXY_HTTPS_ADDR` 和证书配置决定，与此 API 的 `enable_ssl` 字段无关。`protocol` 字段仅影响转发给上游服务时使用的协议。

### DELETE /api/v1/domain-proxy

移除指定域名的反向代理路由。从数据库和内存路由表中删除，**立即生效**。

**请求：**
```json
{
  "domain": "app.example.com"
}
```

**响应：**
```json
{
  "domain": "app.example.com",
  "removed": true
}
```

### GET /api/v1/domain-proxy

列出当前所有域名反向代理记录。

**响应：**
```json
{
  "proxies": [
    {
      "domain": "app.example.com",
      "internal_ip": "172.17.0.3",
      "internal_port": 8080,
      "protocol": "http",
      "enable_ssl": false,
      "created_at": 1711929600
    }
  ],
  "total": 1
}
```

## 排除的 IP 地址段

以下地址段默认不计入流量统计：

**IPv4：**
- 0.0.0.0/8、10.0.0.0/8、100.64.0.0/10、127.0.0.0/8
- 169.254.0.0/16、172.16.0.0/12、192.0.0.0/24、192.0.2.0/24
- 192.88.99.0/24、192.168.0.0/16、198.18.0.0/15、198.51.100.0/24
- 203.0.113.0/24、224.0.0.0/4、240.0.0.0/4

**IPv6：**
- ::/128、::1/128、fc00::/7、fe80::/10、ff00::/8、2001:db8::/32

可通过 `EXTRA_EXCLUDE_CIDRS_V4` 和 `EXTRA_EXCLUDE_CIDRS_V6` 环境变量添加额外的排除地址段。

## 接口名验证

接口名安全验证规则：

- 仅允许字母、数字、连字符、下划线、点号
- 最大长度 15 字符（Linux 接口名限制）
- 自动去除 `@ifX` 后缀（如 `veth123@if456` → `veth123`）
- 拒绝空值和包含特殊字符的输入（防止命令注入）

## 许可证

与父项目使用相同的许可证。
