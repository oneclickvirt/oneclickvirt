# 任务系统 (Task System)

## 概述

这是一个基于 Go 语言开发的高性能异步任务管理系统，专为云主机管理平台设计。系统采用现代化的 **Channel 工作池 (Worker Pool)** 架构，提供强大的并发控制、任务调度和状态管理能力。

## 核心特性

- **Channel 工作池**: 基于 Go Channel 实现的高效并发控制
- **Provider 级别隔离**: 每个云服务商独立的工作池，避免相互影响
- **动态并发调整**: 支持运行时调整 Provider 的并发数配置
- **内存友好**: 无锁设计，自动垃圾回收，避免内存泄漏

## 支持的任务类型

- **create**: 创建云主机实例 (默认30分钟超时)
- **start**: 启动实例 (5分钟超时)
- **stop**: 停止实例 (5分钟超时)
- **restart**: 重启实例 (10分钟超时)
- **delete**: 删除实例 (30分钟超时)
- **reset**: 重置实例 (20分钟超时)
- **reset-password**: 重置密码 (10分钟超时)

## 任务状态管理

完整的任务生命周期管理，支持以下状态：

```
pending → running → completed
   ↓         ↓         ↑
cancelled   failed ←───┘
   ↓         ↓
timeout   cancelling
```

**状态说明：**

- `pending`: 任务已创建，等待执行
- `running`: 任务正在执行
- `completed`: 任务成功完成
- `failed`: 任务执行失败
- `cancelled`: 任务已取消
- `cancelling`: 任务取消中
- `timeout`: 任务执行超时

## 并发控制

### 并发模式

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

**核心结构：**

```go
type TaskService struct {
    dbService       *database.DatabaseService
    runningContexts map[uint]*TaskContext
    contextMutex    sync.RWMutex
    providerPools   map[uint]*ProviderWorkerPool
    poolMutex       sync.RWMutex
    shutdown        chan struct{}
    wg              sync.WaitGroup
    ctx             context.Context
    cancel          context.CancelFunc
}
```

### ProviderWorkerPool

Provider 专用工作池，特性：

- 独立的任务队列
- 可配置的工作者数量
- 上下文取消支持
- 自动负载均衡

**核心结构：**

```go
type ProviderWorkerPool struct {
    ProviderID  uint
    TaskQueue   chan TaskRequest
    WorkerCount int
    Ctx         context.Context
    Cancel      context.CancelFunc
    TaskService *TaskService
}
```

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

## API 接口

### GetTaskService

获取任务服务单例实例。

```go
func GetTaskService() *TaskService
```

### CreateTask

创建新任务。

```go
func (s *TaskService) CreateTask(
    userID uint,
    providerID *uint,
    instanceID *uint,
    taskType string,
    taskData string,
    timeoutDuration int,
) (*adminModel.Task, error)
```

**参数说明：**

- `userID`: 用户ID
- `providerID`: Provider ID（可选）
- `instanceID`: 实例ID（可选）
- `taskType`: 任务类型
- `taskData`: 任务数据（JSON格式）
- `timeoutDuration`: 超时时间（秒），0表示使用默认值

### StartTask

启动任务执行。

```go
func (s *TaskService) StartTask(taskID uint) error
```

### CancelTask

用户取消任务。

```go
func (s *TaskService) CancelTask(taskID uint, userID uint) error
```

### CancelTaskByAdmin

管理员取消任务。

```go
func (s *TaskService) CancelTaskByAdmin(taskID uint, reason string) error
```

### GetUserTasks

获取用户任务列表。

```go
func (s *TaskService) GetUserTasks(
    userID uint,
    req userModel.UserTasksRequest,
) ([]userModel.TaskResponse, int64, error)
```

### GetAdminTasks

获取管理员任务列表。

```go
func (s *TaskService) GetAdminTasks(
    req adminModel.AdminTaskListRequest,
) ([]adminModel.AdminTaskResponse, int64, error)
```

### Shutdown

优雅关闭任务服务。

```go
func (s *TaskService) Shutdown()
```

## 错误处理

任务执行过程中的错误会被记录到任务的 `error_message` 字段，并自动更新任务状态为 `failed`。

**常见错误场景：**

- Provider连接失败
- 实例操作超时
- 资源不足
- 配置错误
- 网络异常

## 超时机制

### 任务级别超时

每个任务类型都有默认的超时时间，也可以在创建任务时指定自定义超时时间。

### 上下文超时

使用 Context 实现超时控制，超时后会自动取消任务执行并清理资源。

### 取消监听

后台监听任务取消信号，支持用户主动取消正在执行的任务。

## 资源管理

### 自动清理

- 任务完成后自动清理运行时上下文
- 释放 Provider 资源计数
- 清理临时数据

### 启动时恢复

服务启动时自动将所有 `running` 状态的任务标记为 `failed`，避免状态不一致。

## 日志记录

## 文件结构

### 核心文件

#### service.go
**职责**: 任务服务的核心入口和生命周期管理

**主要功能:**
- `TaskService` 结构体定义 - 包含所有服务依赖和状态
- 单例模式实现 - `GetTaskService()` 确保全局唯一实例
- 服务初始化 - 数据库连接、工作池初始化
- 启动时恢复 - `cleanupRunningTasksOnStartup()` 清理异常状态
- 优雅关闭 - `Shutdown()` 等待所有任务完成
- 任务启动入口 - `StartTask()` 委托给工作池处理
- 状态管理器接口 - `GetStateManager()` 获取状态管理器

**关键方法:**
```go
GetTaskService() *TaskService           // 获取单例
cleanupRunningTasksOnStartup()          // 启动时清理
Shutdown()                              // 优雅关闭
StartTask(taskID uint) error            // 启动任务
executeCreateInstanceTask()             // 创建实例任务
executeResetInstanceTask()              // 重置实例任务
GetStateManager()                       // 获取状态管理器
```

---

#### worker_pool.go
**职责**: 基于 Channel 的工作池实现，提供并发控制和任务调度

**主要功能:**
- Provider 级别工作池管理 - 每个云服务商独立的工作池
- 动态并发控制 - 支持运行时调整工作者数量
- 任务队列管理 - Channel 实现的无锁队列
- 工作者生命周期 - worker goroutine 的启动和退出
- 任务执行编排 - 状态更新、超时控制、结果回传

**关键方法:**
```go
getOrCreateProviderPool()               // 获取或创建工作池
worker(workerID int)                    // 工作者 goroutine
executeTask(taskReq TaskRequest)        // 执行单个任务
StartTaskWithPool(taskID uint) error    // 将任务发送到工作池
```

**特性:**
- 队列缓冲: 并发数 × 2
- 幂等性保证: 检查任务状态避免重复执行
- 超时保护: Context 超时自动取消
- 资源清理: 自动清理任务上下文

---

#### manager.go
**职责**: 任务的 CRUD 操作和查询管理

**主要功能:**
- 任务创建 - 验证参数、设置默认值、持久化
- 用户任务查询 - 分页、筛选、权限控制
- 管理员任务查询 - 全局视图、多维度筛选
- 统计信息 - 任务数量、状态分布、性能指标

**关键方法:**
```go
CreateTask()                            // 创建新任务
GetUserTasks()                          // 获取用户任务列表
GetAdminTasks()                         // 获取管理员任务列表
GetTaskStats()                          // 获取任务统计
GetTaskOverallStats()                   // 获取总体统计
```

**查询功能:**
- 按 Provider 筛选
- 按任务类型筛选
- 按状态筛选
- 按用户名搜索
- 按实例类型筛选
- 分页和排序

---

#### control.go
**职责**: 任务控制和取消逻辑，包括用户取消和管理员强制停止

**主要功能:**
- 任务完成处理 - 更新状态、记录结果、清理资源
- 用户取消 - 权限验证、状态检查、Context 取消
- 管理员取消 - 强制停止、记录原因
- 强制终止 - 处理无法正常取消的任务
- 资源释放 - Provider 资源配额、任务锁释放

**关键方法:**
```go
CompleteTask()                          // 完成任务
CancelTask()                            // 用户取消任务
CancelTaskByAdmin()                     // 管理员取消任务
ForceStopTask()                         // 强制停止任务
ReleaseTaskLocks()                      // 释放任务锁
cancelPendingTask()                     // 取消等待中任务
cancelRunningTask()                     // 取消运行中任务
forceStopRunningTask()                  // 强制停止运行中任务
handleCancelledTaskCleanup()            // 清理已取消任务
releaseTaskResources()                  // 释放任务资源
```

**状态流转:**
```
pending → cancelled (直接取消)
running → cancelling → cancelled (需要等待)
```

---

#### state_manager.go
**职责**: 统一的任务状态管理，确保跨表状态一致性

**主要功能:**
- 状态同步 - 任务表和实例表状态同步
- 事务安全 - 所有状态更新在事务中执行
- 状态验证 - 检查状态流转的合法性
- 错误处理 - 统一的错误记录和回滚

**关键方法:**
```go
InitTaskStateManager()                  // 初始化状态管理器
GetTaskStateManager()                   // 获取单例实例
UpdateTaskState()                       // 更新任务状态
SyncTaskStatusToInstance()              // 同步状态到实例
```

**支持的状态:**
- `pending` - 等待执行
- `running` - 执行中
- `completed` - 已完成
- `failed` - 失败
- `cancelled` - 已取消
- `cancelling` - 取消中
- `timeout` - 超时

---

### 任务执行文件

#### instance_operations.go
**职责**: 实例生命周期操作任务的执行逻辑

**主要功能:**
- 启动实例 - 调用 Provider API 启动虚拟机
- 停止实例 - 优雅停止或强制停止
- 重启实例 - 停止后重新启动
- 重置密码 - 修改实例 root 密码

**关键方法:**
```go
executeStartInstanceTask()              // 执行启动任务
executeStopInstanceTask()               // 执行停止任务
executeRestartInstanceTask()            // 执行重启任务
executeResetPasswordTask()              // 执行重置密码任务
```

**特性:**
- 进度实时更新 (0% → 100%)
- 状态同步到实例表
- 错误详细记录
- Provider API 集成
- 流量统计服务集成 (pmacct)

**支持的 Provider:**
- LXD
- Incus
- Proxmox
- Docker

---

#### delete_task.go
**职责**: 实例删除任务，包含重试机制和资源清理

**主要功能:**
- 删除实例 - 调用 Provider API 删除虚拟机
- 指数退避重试 - 最多重试 3 次
- 资源配额释放 - CPU、内存、存储
- 数据库清理 - 删除实例记录

**关键方法:**
```go
executeDeleteInstanceTask()             // 执行删除任务
```

**重试策略:**
```
1. 首次尝试
2. 等待 2 秒后重试
3. 等待 4 秒后重试
4. 等待 8 秒后重试
```

**清理内容:**
- 虚拟机实例
- 存储卷
- 网络配置
- 数据库记录
- 资源配额

---

#### port_mapping_tasks.go
**职责**: 端口映射的创建和删除任务

**主要功能:**
- 创建端口映射 - 配置 NAT 规则
- 删除端口映射 - 清理 NAT 规则
- IP 地址刷新 - 更新实例 IP 信息
- 多 Provider 支持 - LXD/Incus/Proxmox

**关键方法:**
```go
executeCreatePortMappingTask()          // 执行创建端口映射任务
executeDeletePortMappingTask()          // 执行删除端口映射任务
```

**端口映射类型:**
- TCP 端口映射
- UDP 端口映射
- 端口范围映射
- 动态端口分配

**特性:**
- 端口冲突检测
- 自动分配可用端口
- 实例 IP 自动刷新
- Provider API 适配

---

### 辅助文件

#### helpers.go
**职责**: 通用辅助函数和任务路由

**主要功能:**
- 默认超时配置 - 各任务类型的超时时间
- 进度更新 - 统一的进度更新接口
- 任务路由 - 根据类型分发到对应的执行函数
- 超时清理 - 定期清理超时任务

**关键方法:**
```go
getDefaultTimeout()                     // 获取默认超时时间
updateTaskProgress()                    // 更新任务进度
markTaskCompleted()                     // 标记任务完成
executeTaskLogic()                      // 任务路由器
CleanupTimeoutTasksWithLockRelease()    // 清理超时任务
```

**超时配置:**
```go
create:         1800s (30分钟)
start:          300s  (5分钟)
stop:           300s  (5分钟)
restart:        600s  (10分钟)
delete:         1800s (30分钟)
reset:          1200s (20分钟)
reset-password: 300s  (5分钟)
create-port:    300s  (5分钟)
delete-port:    300s  (5分钟)
```