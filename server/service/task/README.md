# 任务系统 (Task System)

## 概述

这是一个基于 Go 语言开发的高性能异步任务管理系统，专为云主机管理平台设计。系统采用现代化的 **Channel 工作池 (Worker Pool)** 架构，提供强大的并发控制、任务调度和状态管理能力。

## 核心特性

- **Channel 工作池**: 基于 Go Channel 实现的高效并发控制
- **Provider 级别隔离**: 每个云服务商独立的工作池，避免相互影响
- **动态并发调整**: 支持运行时调整 Provider 的并发数配置
- **内存友好**: 无锁设计，自动垃圾回收，避免内存泄漏


- **create**: 创建云主机实例 (默认30分钟超时)
- **start**: 启动实例 (5分钟超时)
- **stop**: 停止实例 (5分钟超时)
- **restart**: 重启实例 (10分钟超时)
- **delete**: 删除实例 (10分钟超时)
- **reset**: 重置实例 (20分钟超时)
- **reset-password**: 重置密码

### 状态管理

完整的任务生命周期管理，支持以下状态：
```
pending → running → completed
   ↓         ↓         ↑
cancelled   failed ←───┘
   ↓         ↓
timeout   cancelling
```

### 🛡️ 并发控制
- **串行模式**: `AllowConcurrentTasks = false` (默认)
- **并发模式**: `AllowConcurrentTasks = true` + `MaxConcurrentTasks` 配置
- **队列缓冲**: 支持任务排队，避免拥塞
- **超时保护**: 任务级别和系统级别的超时机制

## 核心组件

### TaskService
主要的任务管理服务，提供：
- 任务创建、启动、取消
- 工作池管理
- 状态查询和监控
- 优雅关闭

### ProviderWorkerPool
Provider 专用工作池，特性：
- 独立的任务队列
- 可配置的工作者数量
- 上下文取消支持
- 自动负载均衡

### TaskStateManager
统一的任务状态管理器：
- 跨表状态同步
- 事务安全更新
- 状态流转验证
- 错误处理

## 使用示例

### 创建任务
```go
taskService := task.GetTaskService()
task, err := taskService.CreateTask(
    userID,      // 用户ID
    &providerID, // Provider ID
    &instanceID, // 实例ID
    "create",    // 任务类型
    taskData,    // 任务数据 (JSON)
    1800,        // 超时时间(秒)
)
```

### 启动任务
```go
err := taskService.StartTask(taskID)
```

### 查询任务状态
```go
tasks, total, err := taskService.GetAdminTasks(request)
```

### 取消任务
```go
err := taskService.CancelTask(taskID, userID)
```

## 配置参数

### Provider 级别配置
```yaml
allowConcurrentTasks: true    # 是否允许并发
maxConcurrentTasks: 3         # 最大并发数
taskPollInterval: 60          # 轮询间隔(秒)
enableTaskPolling: true       # 是否启用轮询
```

### 系统级别配置
- 默认超时时间: 30分钟
- 队列缓冲大小: 并发数 × 2
- 取消监听间隔: 1秒
- 优雅关闭等待: 5秒

## 监控指标

### 任务统计
- 总任务数
- 各状态任务数量
- Provider 任务分布
- 执行时间统计

### 性能指标
- 队列长度
- 工作者利用率
- 平均响应时间
- 错误率统计

## 最佳实践

### 1. 并发配置
```go
// 计算密集型任务建议较低并发
maxConcurrentTasks: 1-2

// I/O 密集型任务可以较高并发
maxConcurrentTasks: 3-5
```

### 2. 错误处理
- 设置合理的超时时间
- 实现重试机制
- 记录详细的错误日志
- 监控异常任务

### 3. 资源管理
- 定期清理超时任务
- 监控内存使用
- 合理配置队列大小
- 及时释放资源

## 技术架构

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Task API      │───▶│   TaskService    │───▶│ ProviderPool    │
│   (HTTP/gRPC)   │    │   (Singleton)    │    │ (Per Provider)  │
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
