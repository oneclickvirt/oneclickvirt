package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type agentMonitoringAdminTaskData struct {
	ProviderID uint   `json:"providerId"`
	Action     string `json:"action"`
	Version    string `json:"version,omitempty"`
}

func CreateAgentMonitoringAdminTask(providerID uint, userID uint, taskType string, version string) (*adminModel.Task, error) {
	if err := GetTaskService().EnsureTaskPoolAccepting(); err != nil {
		return nil, err
	}

	action := "deploy"
	timeout := 1800
	estimated := 600
	if taskType == "agent-uninstall" {
		action = "uninstall"
		timeout = 600
		estimated = 120
	}
	data, _ := json.Marshal(agentMonitoringAdminTaskData{
		ProviderID: providerID,
		Action:     action,
		Version:    version,
	})
	task := &adminModel.Task{
		UserID:            userID,
		ProviderID:        &providerID,
		TaskType:          taskType,
		Status:            "pending",
		TaskData:          string(data),
		TimeoutDuration:   timeout,
		EstimatedDuration: estimated,
		CanForceStop:      true,
		IsForceStoppable:  true,
		StatusMessage:     "agent.pending",
	}
	if err := global.APP_DB.Create(task).Error; err != nil {
		return nil, err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return task, nil
}

func (s *TaskService) executeAgentMonitoringTask(ctx context.Context, task *adminModel.Task) error {
	var data agentMonitoringAdminTaskData
	if err := json.Unmarshal([]byte(task.TaskData), &data); err != nil {
		return fmt.Errorf("解析Agent任务数据失败: %w", err)
	}
	if data.ProviderID == 0 && task.ProviderID != nil {
		data.ProviderID = *task.ProviderID
	}
	if data.ProviderID == 0 {
		return fmt.Errorf("Agent任务缺少providerId")
	}

	switch task.TaskType {
	case "agent-deploy":
		return s.executeAgentDeployTask(ctx, task.ID, data.ProviderID, data.Version)
	case "agent-uninstall":
		return s.executeAgentUninstallTask(ctx, task.ID, data.ProviderID)
	default:
		return fmt.Errorf("未知Agent任务类型: %s", task.TaskType)
	}
}

func (s *TaskService) executeAgentDeployTask(ctx context.Context, taskID uint, providerID uint, version string) error {
	if version == "" {
		version = constant.CompatibleAgentVersion
	}
	utils.UpdateTaskProgress(taskID, 5, "agent.deployStarted")

	config, err := agentService.GetMonitoringConfig(global.APP_DB, providerID)
	if err != nil {
		return fmt.Errorf("读取监控配置失败: %w", err)
	}
	if config.AgentToken == "" {
		config.AgentToken = agentService.GenerateAgentToken()
		if err := global.APP_DB.Save(config).Error; err != nil {
			return fmt.Errorf("保存Agent token失败: %w", err)
		}
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("读取Provider失败: %w", err)
	}
	if dbProvider.ConnectionType == "agent" {
		return fmt.Errorf("Agent 模式节点已由节点端 ocv 管理，无需从主控重复部署监控 Agent")
	}

	utils.UpdateTaskProgress(taskID, 15, "agent.connectProvider")
	providerInstance, err := providerService.GetProviderInstanceByID(providerID)
	if err != nil {
		return fmt.Errorf("Provider未连接: %w", err)
	}

	if config.TrafficCollectMethod == "" || config.TrafficCollectMethod == "nft" {
		utils.UpdateTaskProgress(taskID, 25, "agent.detectKernel")
		nftCtx, nftCancel := context.WithTimeout(ctx, 30*time.Second)
		kernelOK, detectErr := agentService.DetectKernelVersionForNFT(nftCtx, providerInstance)
		nftCancel()
		if detectErr != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("kernel version check failed, proceeding with deploy", zap.Uint("providerID", providerID), zap.Error(detectErr))
			}
		} else if !kernelOK {
			config.TrafficCollectMethod = "ipt"
			if err := global.APP_DB.Save(config).Error; err != nil {
				return fmt.Errorf("切换到iptables采集模式失败: %w", err)
			}
		}
	}

	agentCfg := &agentService.AgentConfig{
		Token:                   config.AgentToken,
		TrafficCollectInterval:  config.CollectInterval,
		ResourceCollectInterval: config.ResourceCollectInterval,
		ExtraExcludeCIDRsV4:     config.ExtraExcludeCIDRsV4,
		ExtraExcludeCIDRsV6:     config.ExtraExcludeCIDRsV6,
		TrafficCollectMethod:    config.TrafficCollectMethod,
		EnableReverseProxy:      dbProvider.EnableDomainBinding,
		ProxyHTTPPort:           dbProvider.ProxyHTTPPort,
		ProxyHTTPSPort:          dbProvider.ProxyHTTPSPort,
		ProxyEnableHTTP:         dbProvider.ProxyEnableHTTP,
		ProxyEnableHTTPS:        dbProvider.ProxyEnableHTTPS,
		ProxyTLSCertPath:        dbProvider.ProxyTLSCertPath,
		ProxyTLSKeyPath:         dbProvider.ProxyTLSKeyPath,
	}

	utils.UpdateTaskProgress(taskID, 45, "agent.deployRemote")
	deployCtx, deployCancel := context.WithTimeout(ctx, 10*time.Minute)
	logs, err := agentService.DeployAgentWithConfig(deployCtx, providerInstance, agentCfg, version)
	deployCancel()
	if err != nil {
		utils.AppendTaskError(taskID, 45, "agent.deployFailed", err)
		return err
	}
	if logs != "" {
		utils.AppendTaskLog(taskID, 80, "info", utils.TruncateString(logs, 3500))
	}

	utils.UpdateTaskProgress(taskID, 90, "agent.updateConfig")
	config.AgentInstalled = true
	config.AgentVersion = version
	config.MonitoringMode = "agent"
	if err := global.APP_DB.Save(config).Error; err != nil {
		return fmt.Errorf("保存Agent配置失败: %w", err)
	}
	utils.UpdateTaskProgress(taskID, 100, "agent.deployCompleted")
	return nil
}

func (s *TaskService) executeAgentUninstallTask(ctx context.Context, taskID uint, providerID uint) error {
	utils.UpdateTaskProgress(taskID, 10, "agent.uninstallStarted")
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("读取Provider失败: %w", err)
	}
	if dbProvider.ConnectionType == "agent" {
		return fmt.Errorf("Agent 模式节点不能从主控端卸载，请在节点上执行 ocv uninstall")
	}

	providerInstance, err := providerService.GetProviderInstanceByID(providerID)
	if err != nil {
		return fmt.Errorf("Provider未连接: %w", err)
	}
	utils.UpdateTaskProgress(taskID, 40, "agent.uninstallRemote")
	uninstallCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err = agentService.UninstallAgent(uninstallCtx, providerInstance)
	cancel()
	if err != nil {
		return err
	}

	utils.UpdateTaskProgress(taskID, 90, "agent.updateConfig")
	config, _ := agentService.GetMonitoringConfig(global.APP_DB, providerID)
	if config != nil {
		config.AgentInstalled = false
		config.MonitoringMode = "pmacct"
		_ = global.APP_DB.Save(config).Error
	}
	utils.UpdateTaskProgress(taskID, 100, "agent.uninstallCompleted")
	return nil
}
