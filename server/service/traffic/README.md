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
| `user.go` | 用户流量相关操作 |
| `clear.go` | 流量数据清理（月度重置等） |

## 数据表字段单位

| 表名 | 字段 | 单位 | 说明 |
|---|---|---|---|
| `users` | `total_traffic` | MB | 用户流量限额 |
| `users` | `used_traffic` | MB | 用户当月已使用流量 |
| `providers` | `max_traffic` | MB | Provider 流量限额 |
| `providers` | `used_traffic` | MB | Provider 当月已使用流量 |
| `providers` | `traffic_count_mode` | 字符串 | 流量统计模式：both/out/in |
| `providers` | `traffic_multiplier` | 数字 | 流量计费倍率（默认 1.0） |
| `instances` | `max_traffic` | MB | 实例流量限额 |
| `instances` | `used_traffic` | MB | 实例当月已使用流量（双向总和） |
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
| 用户流量统计 | ✅ | `getUserMonthlyTrafficFromPmacct()` |
| Provider 流量统计 | ✅ | `getProviderMonthlyTrafficFromPmacct()` |
| 流量排行查询 | ✅ | `GetUsersTrafficRanking()` |
| 流量限制检查 | ✅ | `CheckUserTrafficLimit()` / `CheckProviderTrafficLimit()` |

### 统计模式详解

| 模式 | 计算公式 | 适用场景 |
|---|---|---|
| `both`（默认） | `(rx + tx) × multiplier` | 大多数场景 |
| `out`（仅出站） | `tx × multiplier` | 仅出站计费的 IDC |
| `in`（仅入站） | `rx × multiplier` | 特殊计费场景 |

### 统计查询 SQL

```sql
SELECT COALESCE(SUM(
    CASE 
        WHEN p.traffic_count_mode = 'out' THEN vr.tx_bytes * COALESCE(p.traffic_multiplier, 1.0)
        WHEN p.traffic_count_mode = 'in' THEN vr.rx_bytes * COALESCE(p.traffic_multiplier, 1.0)
        ELSE (vr.rx_bytes + vr.tx_bytes) * COALESCE(p.traffic_multiplier, 1.0)
    END
), 0) / 1048576 as month_usage
FROM instances i
LEFT JOIN providers p ON i.provider_id = p.id
LEFT JOIN pmacct_traffic_records vr ON i.id = vr.instance_id
    AND vr.year = ? AND vr.month = ? AND vr.day = 0 AND vr.hour = 0
WHERE i.user_id = ?
```

**关键点**：从 `pmacct_traffic_records` 读取原始字节 → 按 `traffic_count_mode` 选方向 → 应用 `traffic_multiplier` 倍率 → 转换为 MB。查询时必须加 `day = 0 AND hour = 0` 过滤月度汇总记录。

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

单位转换：Agent 存储字节 → pmacct_traffic_records 存储字节 → instances 存储 MB → traffic_records 存储 MB

### 统计阶段（应用流量模式）

```
pmacct_traffic_records (原始字节)
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
- **资源采集间隔**：默认 30 秒，通过 `RESOURCE_COLLECT_INTERVAL` 配置

管理面板可实时修改节点监控配置，修改后服务器自动同步 `.env` 到远端 Agent 并重启服务。

### 网卡变更

实例重建/重启导致网卡变更时，通过 Agent 的 `update` 接口更新接口信息，**不会重置**之前的流量和资源记录。

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

限额检查已经拆分为实例、用户、Provider 三个专用文件，并通过恢复逻辑避免在 `start`、`stop`、`restart`、`reset`、`rebuild`、`delete`、`reset-password` 等活跃任务期间误解锁实例。实例操作入口还会通过 `operation_guard.go` 判断当前流量锁定状态，避免用户在超限后继续启动或分享受限实例。

## 相关函数

### 数据采集（不应用流量模式）

- `SyncInstanceTraffic()` — 同步实例流量
- `updateTrafficRecord()` — 更新流量记录
- `getPmacctData()` — 获取原始数据

### 统计查询（应用流量模式）

- `getUserMonthlyTrafficFromPmacct()` — 用户月度流量统计
- `getProviderMonthlyTrafficFromPmacct()` — Provider 月度流量统计
- `GetUsersTrafficRanking()` — 用户流量排行
- `CheckUserTrafficLimit()` — 用户流量限制检查
- `CheckProviderTrafficLimit()` — Provider 流量限制检查

## 注意事项

1. ⚠️ **原始数据不可修改**：pmacct_traffic_records 中的数据是原始监控数据，任何修改都会导致统计错误
2. ⚠️ **流量模式仅用于统计**：不要在数据写入时应用流量模式，只在查询统计时应用
3. ⚠️ **倍率影响计费**：修改 traffic_multiplier 会影响所有统计查询
4. ✅ **向后兼容**：默认值（both + 1.0）保持原有行为
5. ✅ **月度过滤**：查询时必须加 `day = 0 AND hour = 0` 过滤月度汇总记录
