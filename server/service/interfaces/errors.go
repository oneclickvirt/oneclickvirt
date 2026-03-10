package interfaces

import "errors"

// ErrAsyncCompletion 当任务逻辑函数将任务完成权移交给后台 goroutine 时返回此哨兵错误。
// Worker pool 收到此错误时应跳过 CompleteTask 调用，等待后台 goroutine 负责标记任务完成状态。
var ErrAsyncCompletion = errors.New("async task completion - background goroutine will mark task complete")
