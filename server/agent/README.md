# oneclickvirt-agent

轻量级虚拟机/容器实例监控代理。使用 nftables 命名计数器追踪每个实例的网络流量（排除私有/特殊用途 IP 地址段），并采集各类虚拟化提供商的资源使用情况（CPU、内存、磁盘）。

代理运行在每个 Provider 宿主机上，通过 HTTP API 供中心管理服务器管理监控器、查询流量数据和获取资源指标。

## 系统要求

- Linux 系统，已安装 nftables（`nft` 命令可用）
- 内核版本 >= 3.14（完整支持 nftables）
- Root 权限（nftables 操作需要）
- Rust 1.75+（编译需要）

## 配置说明

通过环境变量或工作目录下的 `.env` 文件进行配置。

| 变量名 | 必填 | 说明 |
|---|---|---|
| `API_TOKEN` | 是 | 认证令牌。所有 API 请求必须在 `x-token` 请求头中包含此值。 |
| `TRAFFIC_COLLECT_INTERVAL` | 否 | 流量采集间隔，单位秒（默认：`5`）。 |
| `RESOURCE_COLLECT_INTERVAL` | 否 | 资源采集间隔，单位秒（默认：`30`）。 |
| `EXTRA_EXCLUDE_CIDRS_V4` | 否 | 逗号分隔的额外排除 IPv4 CIDR 列表（不计入流量统计）。 |
| `EXTRA_EXCLUDE_CIDRS_V6` | 否 | 逗号分隔的额外排除 IPv6 CIDR 列表（不计入流量统计）。 |
| `RUST_LOG` | 否 | 日志级别过滤器（默认：`info`）。示例：`debug`、`warn`、`oneclickvirt_agent=debug`。 |

## 编译构建

```bash
cargo build --release
```

输出二进制文件位于 `target/release/oneclickvirt-agent`。

交叉编译 Linux 目标：

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

代理将在 `0.0.0.0:23782` 启动 HTTP 服务。

Swagger UI 可通过 `http://<host>:23782/swagger-ui/` 访问，用于交互式 API 探索（Swagger UI 端点无需认证）。

## 数据存储

流量数据和资源指标存储在工作目录下的 SQLite 数据库文件 `traffic.db` 中。

- 流量计数器无限累积（直到监控器被删除或清理）。
- 资源指标（CPU、内存、磁盘）保留 24 小时，自动清理过期数据。
- 超过 30 天未更新的过期监控器会被自动清理。

## 流量监控架构

代理使用两个 nftables 作用域捕获流量：

- **inet** 族（表 `vm_traffic_monitor`，链 `forward`）—— 路由层 L3 流量
- **bridge** 族（表 `vm_traffic_monitor_br`，链 `forward`）—— 桥接层 L2 流量

对于每个被监控的网络接口，在两个作用域中创建规则来统计入站和出站的 IPv4/IPv6 流量，排除私有和特殊用途地址段（RFC1918、RFC6598、环回地址、链路本地、组播等）。

流量计数器按照配置的间隔读取（默认每 5 秒）并累积到 SQLite 数据库中。

### 网卡变更处理

当实例重建或使用新网络接口重启时：

1. 管理服务器调用 `POST /api/v1/update` 传入新的接口名称。
2. 旧的 nftables 规则和计数器被移除，创建新的。
3. 累积的 `total_bytes` 值被保留（不会重置）。
4. 如果检测到计数器被重置（当前值 < 上次值），仅将当前值作为增量添加，防止产生负数差值。

### 接口别名

如果某个接口有主桥接（通过 `/sys/class/net/<interface>/master` 检测），会同时为接口名和其桥接主设备名创建规则。

## 资源监控

当创建监控器时指定了 `provider_kind` 和 `instance_name` 字段，代理将按照配置的间隔（默认每 30 秒）采集 CPU、内存和磁盘使用情况。使用的采集方式取决于虚拟化提供商类型：

| 提供商 | 采集方式 |
|---|---|
| Docker | `docker stats --no-stream` + `docker inspect` |
| Podman | `podman stats --no-stream` + `podman inspect` |
| Containerd | `nerdctl stats` + cgroup v2 回退方案 |
| LXD | `lxc info` + `lxc config show` + `lxc config device show` |
| Incus | `incus info` + `incus config show` + `incus config device show` |
| Proxmox VE | `pvesh get /nodes/<node>/lxc\|qemu/<vmid>/status/current` |

### 资源更新行为

和流量统计一样，当通过 `update` 接口更新监控器的网卡/实例信息时，**不会重置**之前已采集的资源历史记录。资源指标以 `monitor_id` 为关联键，monitor_id 在 update 操作中保持不变。

## API 参考

除 `/swagger-ui/` 外，所有端点都需要 `x-token` 请求头进行认证。

### POST /api/v1/add

为一个或多个网络接口创建新的流量监控器。

**请求：**
```json
{
  "interface": "veth1001i0",
  "provider_kind": "proxmox",
  "instance_name": "1001"
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

`provider_kind` 和 `instance_name` 字段为可选。提供后代理会为该实例采集资源指标。

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
  "instance_name": "1001"
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

删除监控器及其关联的 nftables 规则。

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

## 许可证

与父项目使用相同的许可证。
