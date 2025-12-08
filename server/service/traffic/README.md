# 流量数据单位与作用说明

## 数据表字段单位

| 表名 | 字段 | 单位 | 说明 | 备注 |
|------|------|------|------|------|
| `users` | `total_traffic` | MB | 用户流量限额 | 配额设置 |
| `users` | `used_traffic` | MB | 用户当月已使用流量 | 累计值（考虑流量模式） |
| `providers` | `max_traffic` | MB | Provider流量限额 | 配额设置 |
| `providers` | `used_traffic` | MB | Provider当月已使用流量 | 累计值（考虑流量模式） |
| `providers` | `traffic_count_mode` | 字符串 | 流量统计模式 | both/out/in |
| `providers` | `traffic_multiplier` | 数字 | 流量计费倍率 | 默认 1.0 |
| `instances` | `max_traffic` | MB | 实例流量限额 | 配额设置 |
| `instances` | `used_traffic` | MB | 实例当月已使用流量 | 双向流量总和 |
| `instances` | `used_traffic_in` | MB | 实例入站流量 | 原始数据 |
| `instances` | `used_traffic_out` | MB | 实例出站流量 | 原始数据 |
| `traffic_records` | `traffic_in` | MB | 流量记录入站 | 原始数据 |
| `traffic_records` | `traffic_out` | MB | 流量记录出站 | 原始数据 |
| `traffic_records` | `total_used` | MB | 流量记录总计 | 双向流量总和 |
| `pmacct_traffic_records` | `rx_bytes` | **字节** | PMAcct原始数据（接收） | 原始数据，不可修改 |
| `pmacct_traffic_records` | `tx_bytes` | **字节** | PMAcct原始数据（发送） | 原始数据，不可修改 |
| `pmacct_traffic_records` | `total_bytes` | **字节** | PMAcct原始数据（总计） | 原始数据，不可修改 |

## 流量统计模式说明

### 数据存储原则

1. **pmacct_traffic_records**: 存储 PMAcct 原始数据（字节），**永远不修改**
2. **instances**: 存储原始流量数据（MB），`used_traffic_in` 和 `used_traffic_out` 是原始双向数据
3. **traffic_records**: 存储原始流量记录（MB），`traffic_in` 和 `traffic_out` 是原始双向数据
4. **流量模式和倍率**: 仅在**查询统计时**应用，不影响原始数据存储

### 流量模式应用场景

| 场景 | 应用位置 | 说明 |
|------|---------|------|
| PMAcct 数据采集 | ❌ 不应用 | 保持原始数据 |
| 实例流量同步 | ❌ 不应用 | `used_traffic_in/out` 存储原始值 |
| 流量记录写入 | ❌ 不应用 | `traffic_in/out` 存储原始值 |
| 用户流量统计 | ✅ **应用** | `getUserMonthlyTrafficFromPmacct()` |
| Provider流量统计 | ✅ **应用** | `getProviderMonthlyTrafficFromPmacct()` |
| 流量排行查询 | ✅ **应用** | `GetUsersTrafficRanking()` |
| 流量限制检查 | ✅ **应用** | `CheckUserTrafficLimit()` |

### SQL 查询示例

#### 用户流量统计（应用流量模式）

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

**关键点**：
- 从 `pmacct_traffic_records` 读取原始字节数据
- 根据 `providers.traffic_count_mode` 选择统计方向
- 应用 `providers.traffic_multiplier` 倍率
- 转换为 MB（除以 1048576）

## 数据流转流程

### 1. 数据采集阶段（不应用流量模式）

```
PMAcct 守护进程
  ↓ (采集原始数据)
pmacct_traffic_records (存储字节)
  ↓ (SyncInstanceTraffic)
instances.used_traffic_in/out (存储 MB，原始双向)
  ↓ (updateTrafficRecord)
traffic_records.traffic_in/out (存储 MB，原始双向)
```

**单位转换**：
- pmacct_traffic_records: 字节 (bytes)
- instances: MB (bytes / 1048576)
- traffic_records: MB

### 2. 统计查询阶段（应用流量模式）

```
pmacct_traffic_records (原始字节数据)
  ↓ (JOIN providers)
  ↓ (应用 traffic_count_mode 选择 rx/tx/both)
  ↓ (应用 traffic_multiplier 倍率)
  ↓ (转换为 MB)
统计结果
```

## 流量模式详解

### both (双向流量，默认)

```sql
(vr.rx_bytes + vr.tx_bytes) * p.traffic_multiplier
```

- 统计入站 + 出站流量
- 适用于大多数场景

### out (仅出站流量)

```sql
vr.tx_bytes * p.traffic_multiplier
```

- 仅统计出站流量
- 适用于只对出站流量计费的 IDC

### in (仅入站流量)

```sql
vr.rx_bytes * p.traffic_multiplier
```

- 仅统计入站流量
- 适用于特殊计费场景

## 倍率应用示例

### 示例 1: 双倍计费

```
Provider 配置:
  traffic_count_mode: both
  traffic_multiplier: 2.0

原始流量:
  rx_bytes: 10 GB
  tx_bytes: 5 GB
  
计算结果:
  统计流量 = (10 + 5) * 2.0 = 30 GB
```

### 示例 2: 仅出站半价

```
Provider 配置:
  traffic_count_mode: out
  traffic_multiplier: 0.5

原始流量:
  rx_bytes: 10 GB
  tx_bytes: 5 GB
  
计算结果:
  统计流量 = 5 * 0.5 = 2.5 GB
```

## 单位换算

| 单位 | 换算 |
|------|------|
| 1 MB | 1024 KB |
| 1 GB | 1024 MB |
| 1 TB | 1024 GB |
| 1 MB | 1,048,576 字节 |

## 注意事项

1. ⚠️ **原始数据不可修改**：pmacct_traffic_records 的数据是原始监控数据，任何修改都会导致统计错误
2. ⚠️ **流量模式仅用于统计**：不要在数据写入时应用流量模式，只在查询统计时应用
3. ⚠️ **倍率影响计费**：修改 traffic_multiplier 会影响所有统计查询，需谨慎操作
4. ✅ **向后兼容**：默认值（both + 1.0）保持原有行为
5. ✅ **月度过滤必须**：查询时必须加 `day = 0 AND hour = 0` 过滤月度汇总记录

## 相关函数

### 数据采集函数（不应用流量模式）

- `SyncInstanceTraffic()` - 同步实例流量
- `updateTrafficRecord()` - 更新流量记录
- `getPmacctData()` - 获取 PMAcct 原始数据

### 统计查询函数（应用流量模式）

- `getUserMonthlyTrafficFromPmacct()` - 用户月度流量统计
- `getProviderMonthlyTrafficFromPmacct()` - Provider 月度流量统计
- `GetUsersTrafficRanking()` - 用户流量排行
- `CheckUserTrafficLimit()` - 用户流量限制检查
- `CheckProviderTrafficLimit()` - Provider 流量限制检查