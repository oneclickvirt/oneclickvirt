# 用户查询缓存机制

## 概述

为了优化普通用户查询性能，避免频繁的数据库查询和复杂的流量统计计算，系统实现了针对用户端高频查询的缓存机制。

## 缓存策略

### 1. 用户Dashboard缓存
- **缓存键**: `user:dashboard:{userID}`
- **TTL**: 1分钟
- **缓存内容**: 
  - 用户基本信息
  - 实例统计（总数/运行/停止/容器/虚拟机）
  - 资源使用情况（CPU/内存/磁盘）
  - 最近5个实例
- **失效时机**: 
  - 用户创建/删除实例时
  - 用户执行实例操作（启动/停止/重启）时

### 2. 用户流量概览缓存
- **缓存键**: `user:traffic:overview:{userID}`
- **TTL**: 2分钟
- **缓存内容**:
  - 当月流量使用量
  - 流量限制信息
  - 使用百分比
  - 是否受限状态
- **失效时机**:
  - 用户实例操作时（可能影响流量统计）

### 3. 用户流量汇总缓存
- **缓存键**: `user:traffic:summary:{userID}:{year}:{month}`
- **TTL**: 3分钟（与pmacct采集周期一致）
- **缓存内容**:
  - 用户所有实例的月度流量汇总
  - 每个实例的流量详情
- **失效时机**:
  - 用户实例操作时

### 4. 实例流量详情缓存
- **缓存键**: `instance:traffic:detail:{instanceID}`
- **TTL**: 2分钟
- **缓存内容**:
  - 实例当月流量统计
  - 流量历史数据（最近30天）
  - 流量计算模式和倍率
- **失效时机**:
  - 实例操作时

## 技术实现

### 缓存架构
```
┌─────────────────────────────────────┐
│   UserCacheService (单例)           │
│   - sync.Map (线程安全)             │
│   - 定期清理过期缓存 (5分钟)        │
│   - 优雅关闭机制                    │
└─────────────────────────────────────┘
          │
          ├─ GetUserDashboard (1分钟TTL)
          ├─ GetUserTrafficOverview (2分钟TTL)
          ├─ GetUserInstancesTrafficSummary (3分钟TTL)
          └─ GetInstanceTrafficDetail (2分钟TTL)
```

### 内存管理
- **预估内存占用**: 
  - 200用户 × 平均5KB/Dashboard = 1MB
  - 200用户 × 平均10KB/流量汇总 = 2MB
  - 总计约 3-5MB（非常小）
  
- **防止内存泄漏**:
  - 所有缓存条目都有TTL
  - 定期清理过期条目（每5分钟）
  - 监听系统关闭信号，优雅退出
  - 使用sync.Map避免锁竞争

### 缓存失效策略
1. **被动失效**: 缓存条目过期后自动失效
2. **主动失效**: 
   - 用户创建实例 → 清除该用户所有缓存
   - 用户操作实例 → 清除该用户所有缓存 + 该实例缓存
   - 前缀匹配删除（如删除用户所有月度流量缓存）

## 使用示例

### 后端使用
```go
// 获取用户Dashboard（自动缓存）
dashboardService := resources.UserDashboardService{}
dashboard, err := dashboardService.GetUserDashboard(userID)

// 获取流量概览（自动缓存）
trafficService := traffic.NewUserTrafficService()
overview, err := trafficService.GetUserTrafficOverview(userID)

// 手动使缓存失效
cacheService := cache.GetUserCacheService()
cacheService.InvalidateUserCache(userID)
```

### 缓存键管理
```go
// 生成缓存键
key := cache.MakeUserDashboardKey(userID)
key := cache.MakeUserTrafficSummaryKey(userID, year, month)

// 删除单个缓存
cacheService.Delete(key)

// 删除前缀匹配的所有缓存
cacheService.DeleteByPrefix("user:traffic:summary:123:")
```

## 性能优化效果

### 优化前（无缓存）
- Dashboard查询: ~100-200ms（涉及多表聚合）
- 流量汇总查询: ~200-500ms（复杂SQL，处理pmacct重启检测）
- 每用户每次刷新页面都触发多次数据库查询

### 优化后（有缓存）
- Dashboard查询: ~1-2ms（缓存命中）
- 流量汇总查询: ~1-2ms（缓存命中）
- 首次查询仍需访问数据库，后续请求直接返回缓存
- 缓存命中率预计 > 80%

## 注意事项

1. **不要缓存实时变化的数据**
   - 实例列表不缓存（状态快速变化）
   - 任务状态不缓存（需要实时查看进度）

2. **缓存失效时机**
   - 实例创建/删除/操作后立即失效相关缓存
   - 避免用户看到过期数据

3. **内存监控**
   - 缓存大小可控（预估 < 10MB）
   - 定期清理机制确保不会无限增长
   - 系统关闭时优雅清理

4. **并发安全**
   - 使用sync.Map保证线程安全
   - 无需额外的锁机制

## 测试

运行测试:
```bash
cd server/service/cache
go test -v
```

测试覆盖:
- 基本的Set/Get操作
- 过期机制
- 前缀删除
- 用户缓存失效
- GetOrSet功能
- 缓存键生成

## 未来扩展

可以考虑添加:
1. **Redis缓存支持**: 用于多实例部署
2. **缓存统计**: 监控命中率
3. **缓存预热**: 系统启动时预加载热数据
4. **自适应TTL**: 根据访问频率动态调整TTL
