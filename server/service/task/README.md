# 任务系统 (Task System)

## 概述

基于 Go 语言开发的高性能异步任务管理系统，专为云主机管理平台设计。采用 **Channel 工作池 (Worker Pool)** 架构，提供 Provider 级别隔离的并发控制、任务调度和状态管理。

## 核心特性

- **Channel 工作池**：基于 Go Channel 实现的高效并发控制
- **Provider 级别隔离**：每个云服务商独立的工作池，避免相互影响
- **动态并发调整**：支持运行时调整 Provider 的并发数配置
- **统一状态管理**：跨表事务安全的状态同步
- **内存友好**：无锁设计，自动垃圾回收

## 支持的任务类型

| 任务类型 | 说明 | 默认超时 |
|---|---|---|
| `create` | 创建实例 | 30 分钟 |
| `start` | 启动实例 | 5 分钟 |
| `stop` | 停止实例 | 5 分钟 |
| `restart` | 重启实例 | 10 分钟 |
| `delete` | 删除实例 | 30 分钟 |
| `reset` | 重置实例 | 20 分钟 |
| `reset-password` | 重置密码 | 5 分钟 |
| `create-port` | 创建端口映射 | 5 分钟 |
| `delete-port` | 删除端口映射 | 5 分钟 |

## 任务状态管理

完整的任务生命周期：

```
pending → running → completed
   ↓         ↓
cancelled   failed
   ↓
timeout   cancelling
```

| 状态 | 说明 |
|---|---|
| `pending` | 任务已创建，等待执行 |
| `running` | 任务正在执行 |
| `completed` | 任务成功完成 |
| `failed` | 任务执行失败 |
| `cancelled` | 任务已取消 |
| `cancelling` | 任务取消中（等待执行线程响应） |
| `timeout` | 任务执行超时 |

### 实例状态与缓存

任务系统会把长耗时实例操作映射到实例状态：

- `start` -> `starting` -> `running`
- `stop` -> `stopping` -> `stopped`
- `restart` -> `restarting` -> `running`
- `reset` -> `resetting` -> `running` 或失败恢复
- `rebuild` -> `rebuilding`
- `delete` -> `deleting` -> 软删除

实例状态集合统一维护在 `server/constant/instance_status.go`。新增状态时必须同步确认：

- 是否属于展示态，避免用户列表中实例短暂消失。
- 是否仍占用既有资源并应计入 `used_quota`。
- 是否属于创建/重置待确认状态并应计入 `pending_quota`。
- 任务完成、失败或取消后是否会触发用户 dashboard/实例流量缓存失效。

## 并发控制

### 并发模式

- **串行模式**：`AllowConcurrentTasks = false`（默认）
- **并发模式**：`AllowConcurrentTasks = true` + `MaxConcurrentTasks` 配置
- **队列缓冲**：支持任务排队（缓冲大小 = 并发数 × 2）
- **超时保护**：任务级别和系统级别的超时机制

### Provider 级别配置

```yaml
allowConcurrentTasks: true    # 是否允许并发
maxConcurrentTasks: 3         # 最大并发数
taskPollInterval: 60          # 轮询间隔(秒)
enableTaskPolling: true       # 是否启用轮询
```

## 技术架构

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Task API      │───▶│   TaskService    │───▶│ ProviderPool    │
│   (HTTP)        │    │   (Singleton)    │    │ (Per Provider)  │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                                │                        │
                       ┌────────▼────────┐    ┌─────────▼─────────┐
                       │ TaskStateManager│    │   Worker Pool     │
                       │ (Unified State) │    │ (Channel Based)   │
                       └─────────────────┘    └───────────────────┘
                                │                        │
                       ┌────────▼────────┐    ┌─────────▼─────────┐
                       │    Database     │    │  Task Execution   │
                       │   (GORM/MySQL)  │    │ (Provider APIs)   │
                       └─────────────────┘    └───────────────────┘
```

## 文件结构

### 核心文件

| 文件 | 职责 |
|---|---|
| `service.go` | 任务服务入口和生命周期管理（单例、初始化、启动恢复、优雅关闭） |
| `worker_pool.go` | Channel 工作池实现（Provider 级别隔离、动态并发、任务调度） |
| `manager.go` | 任务 CRUD 和查询（创建、用户/管理员查询、统计信息） |
| `control.go` | 任务控制和取消（用户取消、管理员强制停止、资源释放） |
| `state_manager.go` | 统一状态管理（跨表同步、事务安全、状态验证） |
| `helpers.go` | 辅助函数（默认超时、进度更新、任务路由、超时清理） |
| `context_manager.go` | 任务上下文管理 |
| `pool_manager.go` | 工作池管理器 |

### 任务执行文件

| 文件 | 职责 |
|---|---|
| `instance_operations.go` | 启动/停止/重启/重置密码等实例操作 |
| `delete_task.go` | 实例删除（指数退避重试、资源配额释放、数据库清理） |
| `reset_task.go` | 实例重置主逻辑 |
| `reset_helpers.go` | 重置辅助函数 |
| `reset_task_helpers.go` | 重置任务辅助函数 |
| `reset_task_ports.go` | 重置时的端口映射处理 |
| `port_mapping_tasks.go` | 端口映射的创建和删除任务 |
| `sync_port_mappings_task.go` | 端口映射同步任务 |
| `freeze_check.go` | 冻结检查（防止卡死任务） |

## API 接口

### 创建任务

```go
taskService := task.GetTaskService()
task, err := taskService.CreateTask(
    userID,      // 用户 ID
    &providerID, // Provider ID
    &instanceID, // 实例 ID
    "create",    // 任务类型
    taskData,    // 任务数据 (JSON)
    1800,        // 超时时间(秒)，0 使用默认值
)
```

### 启动任务

```go
err := taskService.StartTask(taskID)
```

### 取消任务

```go
// 用户取消
err := taskService.CancelTask(taskID, userID)

// 管理员取消
err := taskService.CancelTaskByAdmin(taskID, reason)

// 强制停止
err := taskService.ForceStopTask(taskID)
```

### 查询任务

```go
// 用户任务列表
tasks, total, err := taskService.GetUserTasks(userID, request)

// 管理员任务列表（支持按 Provider/类型/状态/用户名/实例类型筛选）
tasks, total, err := taskService.GetAdminTasks(request)

// 任务统计
stats, err := taskService.GetTaskStats()
```

### 优雅关闭

```go
taskService.Shutdown()
```

## 删除任务重试策略

```
第 1 次尝试 → 失败 → 等待 2s
第 2 次尝试 → 失败 → 等待 4s
第 3 次尝试 → 失败 → 等待 8s
第 4 次尝试 → 失败 → 标记为 failed
```

删除任务清理内容：虚拟机实例、存储卷、网络配置、数据库记录、资源配额。

## 端口映射任务

- 创建端口映射：配置 NAT 规则、端口冲突检测、自动分配可用端口
- 删除端口映射：清理 NAT 规则
- IP 地址刷新：更新实例 IP 信息
- 支持 TCP/UDP 协议和端口范围映射

## 启动时恢复

服务启动时自动将所有 `running` 状态的任务标记为 `failed`，避免状态不一致。

## 错误处理

任务执行过程中的错误记录到任务的 `error_message` 字段，并自动更新状态为 `failed`。

常见错误场景：Provider 连接失败、实例操作超时、资源不足、配置错误、网络异常。

## 最佳实践

- **计算密集型任务**：建议 `maxConcurrentTasks` 设为 1-2
- **I/O 密集型任务**：可设为 3-5
- 设置合理的超时时间，实现重试机制，监控异常任务
- 定期清理超时任务，合理配置队列大小
