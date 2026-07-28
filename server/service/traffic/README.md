# 流量管理服务 (Traffic Service)

## 概述

负责流量数据的采集同步、历史记录管理、多级限额检查、统计查询和数据聚合。支持基于 Agent（nftables/iptables）的流量监控模式。

## 文件结构

| 文件 | 职责 |
|---|---|
| `service.go` | 服务入口和初始化 |
| `sync_trigger.go` | Agent 流量数据同步触发 |
| `history.go` | 流量历史记录管理 |
| `history_fill.go` | 历史数据填充（补零） |
| `history_query.go` | 历史数据查询 |
| `aggregation.go` | 流量数据聚合（小时/天/月） |
| `limit.go` | 流量限额检查 |
| `three_tier_limit.go` | 三级限额检查（实例/用户/Provider） |
| `three_tier_limit_instance.go` | 实例级流量限额检查与锁定 |
| `three_tier_limit_user.go` | 用户级流量限额检查与批量影响 |
| `three_tier_limit_provider.go` | Provider 级流量限额检查与批量影响 |
| `three_tier_recovery.go` | 三级限额恢复与活跃任务保护 |
| `operation_guard.go` | 流量超限时的实例操作保护 |
| `query.go` | 流量统计查询 |
| `reset_schedule.go` | Provider 流量重置日和当前周期窗口计算 |
| `traffic_reset.go` | 到期重置 Provider 超限状态并触发恢复检查 |
| `user.go` | 用户流量相关操作 |
| `clear.go` | 流量数据清理 |

## 数据表字段单位

| 表名 | 字段 | 单位 | 说明 |
|---|---|---|---|
| `users` | `total_traffic` | MB | 用户流量限额 |
| `users` | `used_traffic` | MB | 用户当前周期已使用流量 |
| `providers` | `max_traffic` | MB | Provider 流量限额 |
| `providers` | `used_traffic` | MB | Provider 当前周期已使用流量 |
| `providers` | `traffic_reset_day` | 数字或空 | 每月流量重置日，空或 0 表示每月 1 日自然月重置 |
| `providers` | `traffic_count_mode` | 字符串 | 流量统计模式：both/out/in |
| `providers` | `traffic_multiplier` | 数字 | 流量计费倍率（默认 1.0） |
| `instances` | `max_traffic` | MB | 实例流量限额 |
| `instances` | `used_traffic` | MB | 实例当前周期已使用流量（双向总和） |
| `instances` | `used_traffic_in` | MB | 实例入站流量（原始数据） |
| `instances` | `used_traffic_out` | MB | 实例出站流量（原始数据） |
| `traffic_records` | `traffic_in` / `traffic_out` / `total_used` | MB | 流量记录 |
| `pmacct_traffic_records` | `rx_bytes` / `tx_bytes` / `total_bytes` | **字节** | 原始监控数据 |

## 流量统计模式

### 数据存储原则

1. **pmacct_traffic_records**：存储原始数据（字节），**永远不修改**
2. **instances**：存储原始流量（MB），`used_traffic_in/out` 是原始双向数据
3. **traffic_records**：存储原始流量记录（MB）
4. **流量模式和倍率**：仅在**查询统计时**应用，不影响原始数据存储

### 流量模式应用场景

| 场景 | 是否应用 | 说明 |
|---|---|---|
| 数据采集 / 实例同步 / 记录写入 | ❌ | 保持原始数据 |
| 用户流量统计 | ✅ | `GetUserCurrentCycleTraffic()` |
| Provider 流量统计 | ✅ | `GetProviderCurrentCycleTraffic()` |
| 流量排行查询 | ✅ | `GetUsersTrafficRanking()` |
| 流量限制检查 | ✅ | `CheckUserTrafficLimit()` / `CheckProviderTrafficLimit()` |

### 统计模式详解

| 模式 | 计算公式 | 适用场景 |
|---|---|---|
| `both`（默认） | `(rx + tx) × multiplier` | 大多数场景 |
| `out`（仅出站） | `tx × multiplier` | 仅出站计费的 IDC |
| `in`（仅入站） | `rx × multiplier` | 特殊计费场景 |

### 当前周期查询口径

Provider 可配置 `traffic_reset_day`。空值或 0 表示自然月，当前周期为每月 1 日到下月 1 日；1 到 31 表示每月对应日期重置，29 到 31 在短月份会自动钳制到当月最后一天。查询服务先按 Provider 计算 `[start, nextReset)` 窗口，再按窗口批量汇总实例、用户和 Provider 用量。

底层月度缓存仍然使用 `day = 0 AND hour = 0` 表示整月派生汇总，但当前周期、自然月、年度和历史趋势都应通过 `QueryService` 从 `pmacct_traffic_records` 的原始累计字节重新计算。计算时先取窗口开始前最后一个采样作为基线，再对窗口内采样做相邻差分；如果采样值回退，则视为 pmacct/agent 计数器重启并从当前值重新累计。

**关键点**：`instance_traffic_histories`、`provider_traffic_histories`、`user_traffic_histories` 中的 `traffic_in`、`traffic_out`、`total_used` 都存原始 MB。`total_used` 必须是 `traffic_in + traffic_out`，不能存已经应用 `traffic_count_mode` 或 `traffic_multiplier` 后的值。

当前实时展示、排行、三层限额判断统一以 `pmacct_traffic_records` 为来源，经 `QueryService` 按窗口前基线和窗口内相邻采样差分计算；`instance_traffic_histories`、`provider_traffic_histories`、`user_traffic_histories` 只作为派生聚合表和历史兼容数据，不能作为唯一实时来源，避免聚合任务未刷新时显示旧值或限额误判。

## 数据流转流程

### 采集阶段（不应用流量模式）

```
Agent (nftables/iptables 计数器)
  ↓ (每5秒采集, 可配置)
Agent 本地 SQLite (traffic.db)
  ↓ (管理服务器定期同步)
MySQL pmacct_traffic_records / instance_traffic_histories (按小时/月聚合)
  ↓ (聚合)
provider_traffic_histories / user_traffic_histories
```

单位转换：Agent 存储字节 → pmacct_traffic_records 存储字节 → instance/provider/user traffic histories 存储原始 MB → 查询阶段应用计数模式和倍率。

### 统计阶段（应用流量模式）

```
pmacct_traffic_records (原始字节) 或 instance_traffic_histories (原始 MB)
  ↓ JOIN providers
  ↓ 应用 traffic_count_mode (选 rx/tx/both)
  ↓ 应用 traffic_multiplier (倍率)
  ↓ 转换为 MB
统计结果
```

## Agent 流量监控

Agent 支持两种流量采集方式，通过 `TRAFFIC_COLLECT_METHOD` 环境变量控制：

| 模式 | 工具 | 说明 |
|---|---|---|
| `nft`（默认） | nftables | 推荐，支持 IPv4/IPv6、内网 IP 精确过滤 |
| `ipt` | iptables | 兼容旧系统 |

### Agent 采集配置

- **流量采集间隔**：默认 5 秒，通过 `TRAFFIC_COLLECT_INTERVAL` 配置
- **流量采集批次**：默认每轮 512 个 active interface，通过 `TRAFFIC_COLLECT_BATCH_SIZE` 配置
- **规则自愈间隔/批次**：默认 60 秒、每轮 128 个 monitor，通过 `TRAFFIC_RECONCILE_INTERVAL` / `TRAFFIC_RECONCILE_BATCH_SIZE` 配置
- **启动恢复批次**：默认 API 启动前同步恢复 32 个 monitor，通过 `TRAFFIC_BOOTSTRAP_BATCH_SIZE` 配置；剩余项由启动后首轮后台自愈继续恢复
- **资源采集间隔**：默认 30 秒，通过 `RESOURCE_COLLECT_INTERVAL` 配置
- **资源采集批次**：默认每轮 16 个 monitor，通过 `RESOURCE_COLLECT_BATCH_SIZE` 配置

管理面板可实时修改节点监控配置，修改后服务器自动同步 `.env` 到远端 Agent 并重启服务。

### 网卡变更

实例重建/宿主机重启导致 veth/tap 变化时，Agent 保留 `monitors.bindings` desired state（接口、期望协议族、已知地址），但会从 `interface_states` active state 移除不存在的旧接口并报告 unhealthy。即使双栈实例的某个地址尚未回填，也会依据 `families` 创建对应协议族的接口级兜底规则。主控每约 5 分钟错峰检查一次 Agent `/list`，仅对 unhealthy、missing 或元数据漂移的实例重新探测 Provider；正常实例不执行逐实例 SSH/API 探测。更新接口前 Agent 会先结算旧 counter 的未落库增量，因此不会因为规则重建直接丢掉已计数流量。

主控后台自愈使用以下保护：

- 同一 Provider 的流量同步、资源同步和接口自愈串行执行。
- 全局最多同时处理 4 个 Provider；超过槽位的任务延后，不创建无界 goroutine。
- 单次后台最多修复 8 个实例；仍有 deferred 项时约 30 秒后继续，失败时按 1/2/4/.../30 分钟指数退避。
- 每轮先批量加载实例和监控映射，并只调用一次 Agent `/list`；Agent 不可达时整节点退避，不退化为逐实例探测。

### 双栈与 Provider 矩阵

- Docker、Podman、Containerd、OrbStack、QEMU/libvirt、KubeVirt 的单 NIC 双栈合并在同一 binding。
- LXD、Incus 和 Proxmox 的双 NIC 模式按 IPv4/IPv6 分离 binding；单 NIC 双栈自动合并。
- 不支持可靠 host 接口探测的 Provider 明确返回 unsupported，不允许假成功。
- 主控同时保留旧 `interface` / `inner_ip` 字段，支持滚动升级；新版健康自愈依赖 Agent 返回 `bindings`、`healthy` 和 `missing_interfaces`。

### 数据库与并发边界

- Agent 采集使用一次 active-state 批量快照，锁外读取 nft/iptables，再以 CAS 短事务批量回写，消除 SQLite 查询 N+1。
- 资源最新点通过 `/api/v1/resources/batch` 一次读取，主控不再逐实例请求。
- 主控流量写入按最多 25 个 monitor 的短事务分批，一次 `SELECT ... FOR UPDATE` 锁定批内 tracking 快照并比较旧累计值，随后批量更新 tracking、小时历史和原始记录，防止重试/重叠造成重复计费；实例月汇总移到事务外按 200 个实例批量幂等重算，用户汇总同样使用批量 SQL。
- Provider/SSH/Agent/nft/iptables 外部调用均不放在数据库事务内。

## 倍率应用示例

### 双倍计费

```
配置: traffic_count_mode=both, traffic_multiplier=2.0
原始: rx=10GB, tx=5GB
结果: (10+5) × 2.0 = 30GB
```

### 仅出站半价

```
配置: traffic_count_mode=out, traffic_multiplier=0.5
原始: rx=10GB, tx=5GB
结果: 5 × 0.5 = 2.5GB
```

## 三级限额检查

流量限额从三个层级进行检查：

1. **实例级别**：`instances.max_traffic`
2. **用户级别**：`users.total_traffic`
3. **Provider 级别**：`providers.max_traffic`

任一层级超限即触发流量限制。

限额检查已经拆分为实例、用户、Provider 三个专用文件，并通过恢复逻辑避免在 `start`、`stop`、`restart`、`reset`、`rebuild`、`delete`、`reset-password` 等活跃任务期间误解锁实例。实例操作入口还会通过 `operation_guard.go` 判断当前流量锁定状态，避免用户在超限后继续启动或分享受限实例。到达 Provider 重置日时，`traffic_reset.go` 会清除该 Provider 的超限状态并重新运行三级限额检查，符合条件的流量超限停机实例会自动创建启动任务。

## 相关函数

### 数据采集（不应用流量模式）

- `SyncInstanceTraffic()` — 同步实例流量
- `updateTrafficRecord()` — 更新流量记录
- `getPmacctData()` — 获取原始数据

### 统计查询（应用流量模式）

- `GetUserCurrentCycleTraffic()` — 用户当前周期流量统计
- `GetProviderCurrentCycleTraffic()` — Provider 当前周期流量统计
- `BatchGetProvidersCurrentCycleTraffic()` — 后台 Provider 列表批量当前周期统计
- `GetUsersTrafficRanking()` — 用户流量排行
- `CheckUserTrafficLimit()` — 用户流量限制检查
- `CheckProviderTrafficLimit()` — Provider 流量限制检查

## 注意事项

1. ⚠️ **原始数据不可修改**：pmacct_traffic_records 中的数据是原始监控数据，任何修改都会导致统计错误
2. ⚠️ **流量模式仅用于统计**：不要在数据写入时应用流量模式，只在查询统计时应用
3. ⚠️ **倍率影响计费**：修改 traffic_multiplier 会影响所有统计查询
4. ✅ **默认周期**：`traffic_reset_day` 为空或 0 时按自然月重置，等价于每月 1 日
5. ✅ **月度缓存过滤**：读取月度汇总缓存时必须加 `day = 0 AND hour = 0`，业务层再按当前周期窗口计算
