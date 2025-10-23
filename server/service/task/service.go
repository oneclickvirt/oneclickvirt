package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/provider/portmapping"
	"oneclickvirt/provider/proxmox"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	userprovider "oneclickvirt/service/user/provider"
	"oneclickvirt/service/vnstat"
	"oneclickvirt/utils"
	"sort"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TaskRequest 任务请求
type TaskRequest struct {
	Task       adminModel.Task
	ResponseCh chan TaskResult // 用于接收任务结果
}

// TaskResult 任务结果
type TaskResult struct {
	Success bool
	Error   error
	Data    map[string]interface{}
}

// ProviderWorkerPool Provider工作池
type ProviderWorkerPool struct {
	ProviderID  uint
	TaskQueue   chan TaskRequest   // 任务队列
	WorkerCount int                // 工作者数量（并发数）
	Ctx         context.Context    // 上下文
	Cancel      context.CancelFunc // 取消函数
	TaskService *TaskService       // 任务服务引用
}

// TaskContext 任务执行上下文
type TaskContext struct {
	TaskID     uint
	Context    context.Context
	CancelFunc context.CancelFunc
	StartTime  time.Time
}

// TaskService 任务管理服务 - 简化版本使用Channel池
type TaskService struct {
	dbService       *database.DatabaseService
	runningContexts map[uint]*TaskContext        // 正在执行的任务上下文，用于取消操作
	contextMutex    sync.RWMutex                 // 保护runningContexts的锁
	providerPools   map[uint]*ProviderWorkerPool // Provider工作池
	poolMutex       sync.RWMutex                 // 保护providerPools的锁
	shutdown        chan struct{}                // 系统关闭信号
	wg              sync.WaitGroup               // 用于等待所有goroutine完成
	ctx             context.Context              // 服务级别的context
	cancel          context.CancelFunc           // 服务级别的cancel函数
}

var (
	taskService     *TaskService
	taskServiceOnce sync.Once
)

// GetTaskService 获取任务服务单例
func GetTaskService() *TaskService {
	taskServiceOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		taskService = &TaskService{
			dbService:       database.GetDatabaseService(),
			runningContexts: make(map[uint]*TaskContext),
			providerPools:   make(map[uint]*ProviderWorkerPool),
			shutdown:        make(chan struct{}),
			ctx:             ctx,
			cancel:          cancel,
		}
		// 设置全局任务锁释放器
		// 使用channel池实现并发控制，无需额外的锁释放
		global.APP_TASK_LOCK_RELEASER = taskService

		// 初始化统一任务状态管理器
		InitTaskStateManager(taskService)

		// 只有在数据库已初始化时才清理running状态的任务
		if isSystemInitialized() {
			taskService.cleanupRunningTasksOnStartup()
		} else {
			global.APP_LOG.Debug("系统未初始化，跳过任务清理")
		}
	})
	return taskService
}

// isSystemInitialized 检查系统是否已初始化（本地检查，避免循环依赖）
func isSystemInitialized() bool {
	if global.APP_DB == nil {
		return false
	}

	// 简单的数据库连接测试
	sqlDB, err := global.APP_DB.DB()
	if err != nil {
		return false
	}

	if err := sqlDB.Ping(); err != nil {
		return false
	}

	// 检查是否有用户表，这是一个基本的初始化标志
	return global.APP_DB.Migrator().HasTable("users")
}

// cleanupRunningTasksOnStartup 服务启动时清理running状态的任务
func (s *TaskService) cleanupRunningTasksOnStartup() {
	// 再次检查数据库是否可用，防止在初始化过程中数据库状态发生变化
	if global.APP_DB == nil {
		global.APP_LOG.Warn("数据库连接不存在，无法清理运行中的任务")
		return
	}

	// 将所有running状态的任务标记为failed
	result := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ?", "running").
		Updates(map[string]interface{}{
			"status":        "failed",
			"error_message": "服务重启，任务被中断",
			"completed_at":  time.Now(),
		})

	if result.Error != nil {
		global.APP_LOG.Error("清理运行中任务失败", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		global.APP_LOG.Info("服务启动时清理了运行中的任务", zap.Int64("count", result.RowsAffected))
	}

	// 内存计数器从空开始，不需要额外初始化
}

// Shutdown 优雅关闭任务服务，等待所有goroutine完成
func (s *TaskService) Shutdown() {
	global.APP_LOG.Info("开始关闭任务服务，等待所有后台任务完成...")

	// 发送关闭信号
	if s.cancel != nil {
		s.cancel()
	}
	select {
	case <-s.shutdown:
		// 已关闭
	default:
		close(s.shutdown)
	}

	// 关闭所有工作池
	s.poolMutex.Lock()
	for providerID, pool := range s.providerPools {
		global.APP_LOG.Info("关闭Provider工作池", zap.Uint("providerId", providerID))
		pool.Cancel()
	}
	s.poolMutex.Unlock()

	// 等待所有goroutine完成
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	// 等待最多30秒
	select {
	case <-done:
		global.APP_LOG.Info("所有后台任务已完成")
	case <-time.After(30 * time.Second):
		global.APP_LOG.Warn("等待后台任务超时，强制退出")
	}

	global.APP_LOG.Info("TaskService关闭完成")
}

// StartTask 启动任务 - 委托给新的实现
func (s *TaskService) StartTask(taskID uint) error {
	return s.StartTaskWithPool(taskID)
}

// executeCreateInstanceTask 执行创建实例任务
func (s *TaskService) executeCreateInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 使用用户provider服务处理创建实例任务，避免循环依赖
	userProviderService := userprovider.NewService()
	return userProviderService.ProcessCreateInstanceTask(ctx, task)
}

// executeResetInstanceTask 执行重置实例任务
func (s *TaskService) executeResetInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 5, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 10, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权
	if instance.UserID != task.UserID {
		return fmt.Errorf("无权限操作此实例")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 15, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取系统镜像信息...")

	// 获取原始系统镜像，使用Provider的架构信息进行匹配
	var systemImage systemModel.SystemImage
	if err := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ?",
		instance.Image, provider.Type, instance.InstanceType, provider.Architecture).First(&systemImage).Error; err != nil {
		global.APP_LOG.Error("获取系统镜像信息失败",
			zap.String("image", instance.Image),
			zap.String("providerType", provider.Type),
			zap.String("instanceType", instance.InstanceType),
			zap.String("architecture", provider.Architecture),
			zap.Error(err))
		return fmt.Errorf("获取系统镜像信息失败: %v", err)
	}

	providerApiService := &provider2.ProviderApiService{}

	// 更新进度
	s.updateTaskProgress(task.ID, 25, "正在保存端口映射配置...")

	// 保存旧的端口映射配置（在删除实例之前）
	global.APP_LOG.Info("保存原有端口映射配置",
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	var oldPortMappings []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ?", instance.ID).Find(&oldPortMappings).Error; err != nil {
		global.APP_LOG.Warn("获取旧端口映射失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("已保存端口映射配置",
			zap.Uint("instanceId", instance.ID),
			zap.Int("portCount", len(oldPortMappings)))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在删除旧实例...")

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在删除旧实例...")

	// 第一步：删除现有实例，使用 Provider ID 确保使用正确的 Provider
	global.APP_LOG.Info("开始删除旧实例",
		zap.Uint("taskId", task.ID),
		zap.String("instanceName", instance.Name),
		zap.String("provider", provider.Name))

	deleteErr := providerApiService.DeleteInstanceByProviderID(ctx, provider.ID, instance.Name)
	if deleteErr != nil {
		global.APP_LOG.Error("Provider删除实例失败（重置过程中）",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Error(deleteErr))

		// 检查是否是容器不存在的错误
		errorStr := strings.ToLower(deleteErr.Error())
		isNotFoundError := strings.Contains(errorStr, "no such container") ||
			strings.Contains(errorStr, "not found") ||
			strings.Contains(errorStr, "already removed") ||
			strings.Contains(errorStr, "container not found")

		if !isNotFoundError {
			// 如果不是"不存在"的错误，说明删除确实失败了
			global.APP_DB.Model(&instance).Update("status", "error")
			return fmt.Errorf("删除旧实例失败: %v", deleteErr)
		}

		global.APP_LOG.Info("实例可能已不存在，继续重置流程",
			zap.String("instanceName", instance.Name))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 40, "等待旧实例完全删除...")

	// 等待删除完成，并验证容器是否真的被删除
	// 对于Docker容器，需要更长的等待时间确保完全删除
	maxWaitTime := 100 * time.Second
	checkInterval := 10 * time.Second
	waitStart := time.Now()

	global.APP_LOG.Info("等待旧实例完全删除",
		zap.String("instanceName", instance.Name),
		zap.Duration("maxWaitTime", maxWaitTime))

	// 简单等待，确保删除操作完成
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("任务已取消")
		case <-time.After(checkInterval):
			// 如果超过最大等待时间，跳出循环
			if time.Since(waitStart) >= maxWaitTime {
				global.APP_LOG.Info("等待删除完成",
					zap.String("instanceName", instance.Name),
					zap.Duration("waitTime", time.Since(waitStart)))
				goto CreateNewInstance
			}
		}
	}

CreateNewInstance:

	// 更新进度
	s.updateTaskProgress(task.ID, 50, "正在准备重新创建实例...")

	// 第二步：重新创建实例，使用 CreateInstanceByProviderID 方法以指定准确的Provider
	createReq := provider2.CreateInstanceRequest{
		InstanceConfig: providerModel.ProviderInstanceConfig{
			Name:         instance.Name,
			Image:        instance.Image,
			InstanceType: instance.InstanceType,
			CPU:          fmt.Sprintf("%d", instance.CPU),
			Memory:       fmt.Sprintf("%dMB", instance.Memory),
			Disk:         fmt.Sprintf("%dMB", instance.Disk),
			Env:          make(map[string]string),
			Metadata:     make(map[string]string),
		},
		SystemImageID: systemImage.ID,
	}

	createReq.InstanceConfig.Env["RESET_OPERATION"] = "true"
	createReq.InstanceConfig.Metadata["original_instance_id"] = fmt.Sprintf("%d", instance.ID)

	// Docker特殊处理：需要在创建时传递端口映射配置
	if provider.Type == "docker" && len(oldPortMappings) > 0 {
		global.APP_LOG.Info("Docker类型实例，在创建时配置端口映射",
			zap.Int("portCount", len(oldPortMappings)))

		// 将端口映射信息添加到实例配置中，Docker容器需要在创建时指定端口映射
		var ports []string
		for _, oldPort := range oldPortMappings {
			// 格式: "0.0.0.0:公网端口:容器端口/协议"
			portMapping := fmt.Sprintf("0.0.0.0:%d:%d/%s", oldPort.HostPort, oldPort.GuestPort, oldPort.Protocol)
			ports = append(ports, portMapping)
		}
		createReq.InstanceConfig.Ports = ports

		global.APP_LOG.Info("Docker容器端口映射配置已添加",
			zap.Int("portCount", len(ports)),
			zap.Strings("ports", ports))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 55, "正在重新创建实例...")

	if err := providerApiService.CreateInstanceByProviderID(ctx, provider.ID, createReq); err != nil {
		global.APP_LOG.Error("Provider重建实例失败（重置过程中）",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Error(err))

		// 更新实例状态为重置失败
		global.APP_DB.Model(&instance).Update("status", "error")
		return fmt.Errorf("重置实例失败（重建阶段）: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 65, "正在等待实例启动...")

	// 等待实例启动完成（特别是Docker和VM类型需要更长的启动时间）
	global.APP_LOG.Info("等待实例启动完成",
		zap.String("instanceName", instance.Name))

	// 简单等待，确保实例完全启动
	time.Sleep(15 * time.Second)

	// 更新进度
	s.updateTaskProgress(task.ID, 66, "正在获取实例最新内网IP地址...")

	// 第三步：获取实例最新的内网IP地址（关键步骤，避免后续端口映射失败）
	var newPrivateIP string

	// 获取Provider实例以调用getInstanceIP方法
	prov, _, err := providerApiService.GetProviderByID(provider.ID)
	if err != nil {
		global.APP_LOG.Error("获取Provider实例失败",
			zap.Uint("providerId", provider.ID),
			zap.Error(err))
	} else {
		// 根据不同的Provider类型获取内网IP
		switch provider.Type {
		case "lxd":
			if lxdProv, ok := prov.(*lxd.LXDProvider); ok {
				if ip, err := lxdProv.GetInstanceIPv4(instance.Name); err == nil {
					newPrivateIP = ip
					global.APP_LOG.Info("成功获取LXD实例最新内网IP",
						zap.String("instanceName", instance.Name),
						zap.String("privateIP", newPrivateIP))
				} else {
					global.APP_LOG.Warn("获取LXD实例内网IP失败，将在后续重试",
						zap.String("instanceName", instance.Name),
						zap.Error(err))
				}
			}
		case "incus":
			if incusProv, ok := prov.(*incus.IncusProvider); ok {
				if ip, err := incusProv.GetInstanceIPv4(ctx, instance.Name); err == nil {
					newPrivateIP = ip
					global.APP_LOG.Info("成功获取Incus实例最新内网IP",
						zap.String("instanceName", instance.Name),
						zap.String("privateIP", newPrivateIP))
				} else {
					global.APP_LOG.Warn("获取Incus实例内网IP失败，将在后续重试",
						zap.String("instanceName", instance.Name),
						zap.Error(err))
				}
			}
		case "proxmox":
			if proxmoxProv, ok := prov.(*proxmox.ProxmoxProvider); ok {
				if ip, err := proxmoxProv.GetInstanceIPv4(ctx, instance.Name); err == nil {
					newPrivateIP = ip
					global.APP_LOG.Info("成功获取Proxmox实例最新内网IP",
						zap.String("instanceName", instance.Name),
						zap.String("privateIP", newPrivateIP))
				} else {
					global.APP_LOG.Warn("获取Proxmox实例内网IP失败，将在后续重试",
						zap.String("instanceName", instance.Name),
						zap.Error(err))
				}
			}
		case "docker":
			// Docker通常不需要内网IP映射，跳过
			global.APP_LOG.Debug("Docker实例跳过内网IP获取")
		}

		// 如果成功获取到新的内网IP，立即更新到数据库
		if newPrivateIP != "" && newPrivateIP != instance.PrivateIP {
			if err := global.APP_DB.Model(&instance).Update("private_ip", newPrivateIP).Error; err != nil {
				global.APP_LOG.Error("更新实例内网IP到数据库失败",
					zap.Uint("instanceId", instance.ID),
					zap.String("oldPrivateIP", instance.PrivateIP),
					zap.String("newPrivateIP", newPrivateIP),
					zap.Error(err))
			} else {
				// 更新内存中的实例对象，确保后续使用最新IP
				instance.PrivateIP = newPrivateIP
				global.APP_LOG.Info("实例内网IP已更新到数据库",
					zap.Uint("instanceId", instance.ID),
					zap.String("oldPrivateIP", instance.PrivateIP),
					zap.String("newPrivateIP", newPrivateIP))
			}
		}
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 68, "正在生成并设置新密码...")

	// 第四步：生成新密码并设置到实例
	newPassword := utils.GenerateStrongPassword(12)

	global.APP_LOG.Info("开始设置实例新密码",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	// 通过Provider设置实例密码，使用重试机制
	providerService := provider2.GetProviderService()
	maxPasswordRetries := 3
	var lastPasswordErr error
	passwordSetSuccess := false

	for attempt := 1; attempt <= maxPasswordRetries; attempt++ {
		if attempt > 1 {
			s.updateTaskProgress(task.ID, 68+attempt, fmt.Sprintf("正在设置密码（第%d次尝试）...", attempt))
			// 重试前等待一段时间
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		err := providerService.SetInstancePassword(ctx, instance.ProviderID, instance.Name, newPassword)
		if err != nil {
			lastPasswordErr = err
			global.APP_LOG.Warn("设置实例密码失败，准备重试",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.String("instanceName", instance.Name),
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		passwordSetSuccess = true
		global.APP_LOG.Info("实例密码设置成功",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.Int("attempt", attempt))
		break
	}

	if !passwordSetSuccess {
		global.APP_LOG.Error("设置实例密码失败，已达最大重试次数",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.Int("maxRetries", maxPasswordRetries),
			zap.Error(lastPasswordErr))
		// 注意：这里不返回错误，因为可能是实例类型不支持密码设置
		// 但我们会记录警告，并继续完成重置流程
		global.APP_LOG.Warn("将继续完成重置流程，但密码可能未正确设置",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID))
		// 如果密码设置失败，使用默认密码
		newPassword = "root" // 或者保持原密码
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 72, "正在更新实例信息到数据库...")

	// 第四步：更新实例信息到数据库
	// 注意：重建后的IP地址应该从Provider获取，这里保持原IP
	instanceUpdates := map[string]interface{}{
		"status":    "running",
		"public_ip": instance.PublicIP, // 保持原IP，实际情况下应该从Provider重新获取
		"username":  "root",
		"password":  newPassword, // 使用设置成功的新密码
	}

	if err := global.APP_DB.Model(&instance).Updates(instanceUpdates).Error; err != nil {
		global.APP_LOG.Error("更新重置后的实例信息失败", zap.Error(err))
		return fmt.Errorf("更新实例信息失败: %v", err)
	}

	global.APP_LOG.Info("实例密码已更新到数据库",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	// 更新进度
	s.updateTaskProgress(task.ID, 75, "正在恢复端口映射...")

	// 第四步：恢复原有的端口映射
	global.APP_LOG.Info("开始恢复原有端口映射",
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.String("providerType", provider.Type),
		zap.Int("portCount", len(oldPortMappings)))

	if len(oldPortMappings) > 0 {
		// 先删除数据库中的旧记录（ID会改变，但配置保持不变）
		if err := global.APP_DB.Where("instance_id = ?", instance.ID).Delete(&providerModel.Port{}).Error; err != nil {
			global.APP_LOG.Warn("清理旧端口映射记录失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		}

		// 使用保存的端口映射配置重新创建
		successCount := 0
		failCount := 0

		// Docker类型：端口映射在创建时已处理，只需恢复数据库记录
		if provider.Type == "docker" {
			global.APP_LOG.Info("Docker实例，恢复端口映射数据库记录",
				zap.Uint("instanceId", instance.ID))

			for _, oldPort := range oldPortMappings {
				newPort := providerModel.Port{
					InstanceID:    instance.ID,
					ProviderID:    provider.ID,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Protocol:      oldPort.Protocol,
					Description:   oldPort.Description,
					Status:        "active",
					IsSSH:         oldPort.IsSSH,
					IsAutomatic:   oldPort.IsAutomatic,
					PortType:      oldPort.PortType,
					MappingMethod: oldPort.MappingMethod,
					IPv6Enabled:   oldPort.IPv6Enabled,
				}

				if err := global.APP_DB.Create(&newPort).Error; err != nil {
					global.APP_LOG.Warn("恢复Docker端口映射记录失败",
						zap.Int("hostPort", oldPort.HostPort),
						zap.Error(err))
					failCount++
				} else {
					successCount++
				}
			}
		} else {
			// LXD/Incus/Proxmox类型：需要应用到远程服务器
			global.APP_LOG.Info("非Docker实例，恢复端口映射并应用到远程服务器",
				zap.Uint("instanceId", instance.ID),
				zap.String("providerType", provider.Type))

			// 初始化portmapping manager
			manager := portmapping.NewManager(&portmapping.ManagerConfig{
				DefaultMappingMethod: provider.IPv4PortMappingMethod,
			})

			// 确定portmapping类型
			portMappingType := provider.Type
			if portMappingType == "proxmox" {
				portMappingType = "iptables"
			}

			// 按协议分组端口映射
			tcpPorts := make([]providerModel.Port, 0)
			udpPorts := make([]providerModel.Port, 0)
			bothPorts := make([]providerModel.Port, 0)
			for _, oldPort := range oldPortMappings {
				switch oldPort.Protocol {
				case "tcp":
					tcpPorts = append(tcpPorts, oldPort)
				case "udp":
					udpPorts = append(udpPorts, oldPort)
				case "both":
					bothPorts = append(bothPorts, oldPort)
				}
			}

			// 处理TCP端口
			if len(tcpPorts) > 0 {
				processedCount, failedCount := s.restorePortMappingsOptimized(ctx, tcpPorts, instance, provider, manager, portMappingType)
				successCount += processedCount
				failCount += failedCount
			}

			// 处理UDP端口
			if len(udpPorts) > 0 {
				processedCount, failedCount := s.restorePortMappingsOptimized(ctx, udpPorts, instance, provider, manager, portMappingType)
				successCount += processedCount
				failCount += failedCount
			}

			// 处理Both端口（保持协议为both，在实际映射时会分别创建TCP和UDP）
			if len(bothPorts) > 0 {
				processedCount, failedCount := s.restorePortMappingsOptimized(ctx, bothPorts, instance, provider, manager, portMappingType)
				successCount += processedCount
				failCount += failedCount
			}
		}

		global.APP_LOG.Info("端口映射恢复完成",
			zap.Uint("instanceId", instance.ID),
			zap.String("providerType", provider.Type),
			zap.Int("成功", successCount),
			zap.Int("失败", failCount))
	} else {
		global.APP_LOG.Warn("未找到旧的端口映射配置，将创建默认端口映射",
			zap.Uint("instanceId", instance.ID))

		// 如果没有旧的端口映射，则创建默认端口映射
		portMappingService := &resources.PortMappingService{}
		if err := portMappingService.CreateDefaultPortMappings(instance.ID, provider.ID); err != nil {
			global.APP_LOG.Warn("创建默认端口映射失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("默认端口映射创建成功",
				zap.Uint("instanceId", instance.ID))
		}
	}

	// 第4.5步：从数据库查询SSH端口映射并更新实例的ssh_port字段
	var sshPortMapping providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instance.ID).First(&sshPortMapping).Error; err == nil {
		// 找到SSH端口映射，更新实例的ssh_port字段为映射的公网端口（HostPort）
		if err := global.APP_DB.Model(&instance).Update("ssh_port", sshPortMapping.HostPort).Error; err != nil {
			global.APP_LOG.Warn("更新实例SSH端口失败",
				zap.Uint("instanceId", instance.ID),
				zap.Int("sshPort", sshPortMapping.HostPort),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("实例SSH端口已更新",
				zap.Uint("instanceId", instance.ID),
				zap.Int("sshPort", sshPortMapping.HostPort))
		}
	} else {
		global.APP_LOG.Warn("未找到SSH端口映射，使用默认值22",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		// 如果没有找到SSH端口映射，设置为默认值22
		global.APP_DB.Model(&instance).Update("ssh_port", 22)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 85, "正在重新初始化监控服务...")

	// 第五步：清理旧的vnstat接口记录并重新初始化
	global.APP_LOG.Info("开始重新初始化vnstat监控",
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	// 清理旧的vnstat数据
	vnstatService := vnstat.NewService()
	if err := vnstatService.CleanupVnStatData(instance.ID); err != nil {
		global.APP_LOG.Warn("清理旧的vnstat数据失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
	}

	// 重新初始化vnstat监控
	if err := vnstatService.InitializeVnStatForInstance(instance.ID); err != nil {
		global.APP_LOG.Warn("重新初始化vnstat监控失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		// 不影响重置流程，继续执行
	} else {
		global.APP_LOG.Info("vnstat监控重新初始化成功",
			zap.Uint("instanceId", instance.ID))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 95, "重置完成，正在收尾...")

	global.APP_LOG.Info("用户实例重置成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID))

	return nil
}

// restorePortMappingsOptimized 优化的端口映射恢复逻辑
// 检测连续端口范围，避免重复创建端口代理设备
func (s *TaskService) restorePortMappingsOptimized(
	ctx context.Context,
	ports []providerModel.Port,
	instance providerModel.Instance,
	provider providerModel.Provider,
	manager *portmapping.Manager,
	portMappingType string,
) (successCount int, failCount int) {
	if len(ports) == 0 {
		return 0, 0
	}

	// 按端口号排序
	sort.Slice(ports, func(i, j int) bool {
		return ports[i].HostPort < ports[j].HostPort
	})

	// 检测连续端口范围
	consecutiveGroups := make([][]providerModel.Port, 0)
	currentGroup := []providerModel.Port{ports[0]}

	for i := 1; i < len(ports); i++ {
		prevPort := currentGroup[len(currentGroup)-1]
		currPort := ports[i]

		// 检查是否连续且是1:1映射
		if currPort.HostPort == prevPort.HostPort+1 &&
			currPort.GuestPort == prevPort.GuestPort+1 &&
			currPort.HostPort == currPort.GuestPort {
			// 连续端口，加入当前组
			currentGroup = append(currentGroup, currPort)
		} else {
			// 不连续，保存当前组并开始新组
			consecutiveGroups = append(consecutiveGroups, currentGroup)
			currentGroup = []providerModel.Port{currPort}
		}
	}
	// 保存最后一组
	consecutiveGroups = append(consecutiveGroups, currentGroup)

	global.APP_LOG.Info("端口映射分组完成",
		zap.Int("totalPorts", len(ports)),
		zap.Int("groups", len(consecutiveGroups)))

	// 处理每个分组
	for _, group := range consecutiveGroups {
		// 判断是否应该使用范围映射
		// LXD/Incus 支持范围映射，Proxmox 使用 iptables 需要逐个创建
		useRangeMapping := (provider.Type == "lxd" || provider.Type == "incus") && len(group) >= 3

		if useRangeMapping {
			// 3个或以上连续端口，使用范围映射（避免创建重复设备）
			// 注意：直接操作provider层，跳过portmapping manager以避免创建独立代理
			global.APP_LOG.Info("检测到连续端口范围，跳过portmapping创建（已由实例创建时处理）",
				zap.String("providerType", provider.Type),
				zap.String("protocol", group[0].Protocol),
				zap.Int("startPort", group[0].HostPort),
				zap.Int("endPort", group[len(group)-1].HostPort),
				zap.Int("count", len(group)))

			// 只恢复数据库记录，不创建实际代理（因为范围代理已存在）
			for _, oldPort := range group {
				newPort := providerModel.Port{
					InstanceID:    instance.ID,
					ProviderID:    provider.ID,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Protocol:      oldPort.Protocol,
					Description:   oldPort.Description,
					Status:        "active",
					IsSSH:         oldPort.IsSSH,
					IsAutomatic:   oldPort.IsAutomatic,
					PortType:      oldPort.PortType,
					MappingMethod: oldPort.MappingMethod,
					IPv6Enabled:   oldPort.IPv6Enabled,
				}
				if err := global.APP_DB.Create(&newPort).Error; err != nil {
					global.APP_LOG.Warn("恢复端口映射数据库记录失败",
						zap.Int("hostPort", oldPort.HostPort),
						zap.Error(err))
					failCount++
				} else {
					successCount++
				}
			}
		} else {
			// Proxmox 或少于3个端口，逐个处理
			for _, oldPort := range group {
				isSSH := oldPort.IsSSH
				portReq := &portmapping.PortMappingRequest{
					InstanceID:    fmt.Sprintf("%d", instance.ID),
					ProviderID:    provider.ID,
					Protocol:      oldPort.Protocol,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Description:   oldPort.Description,
					MappingMethod: provider.IPv4PortMappingMethod,
					IsSSH:         &isSSH,
				}

				result, err := manager.CreatePortMapping(ctx, portMappingType, portReq)
				if err != nil {
					global.APP_LOG.Warn("应用端口映射到远程服务器失败",
						zap.Int("hostPort", oldPort.HostPort),
						zap.Error(err))

					// 即使失败也创建数据库记录（状态为failed）
					newPort := providerModel.Port{
						InstanceID:    instance.ID,
						ProviderID:    provider.ID,
						HostPort:      oldPort.HostPort,
						GuestPort:     oldPort.GuestPort,
						Protocol:      oldPort.Protocol,
						Description:   oldPort.Description,
						Status:        "failed",
						IsSSH:         oldPort.IsSSH,
						IsAutomatic:   oldPort.IsAutomatic,
						PortType:      oldPort.PortType,
						MappingMethod: oldPort.MappingMethod,
						IPv6Enabled:   oldPort.IPv6Enabled,
					}
					global.APP_DB.Create(&newPort)
					failCount++
				} else {
					successCount++
					global.APP_LOG.Debug("端口映射已应用到远程服务器",
						zap.Uint("portId", result.ID),
						zap.Int("hostPort", result.HostPort),
						zap.Int("guestPort", result.GuestPort))
				}
			}
		}
	}

	return successCount, failCount
}

// GetStateManager 获取任务状态管理器
func (s *TaskService) GetStateManager() interfaces.TaskStateManagerInterface {
	return GetTaskStateManager()
}
