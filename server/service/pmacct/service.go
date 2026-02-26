package pmacct

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// Service pmacct服务
type Service struct {
	ctx        context.Context
	providerID uint
	sshPool    *utils.SSHConnectionPool // SSH连接池
}

var (
	batchProcessor     *BatchProcessor
	batchProcessorOnce sync.Once
)

// NewService 创建pmacct服务实例（使用全局SSH连接池）
func NewService() *Service {
	return &Service{
		ctx:     global.APP_SHUTDOWN_CONTEXT,
		sshPool: utils.GetGlobalSSHPool(),
	}
}

// NewServiceWithContext 使用指定context创建pmacct服务实例（使用全局SSH连接池）
func NewServiceWithContext(ctx context.Context) *Service {
	return &Service{
		ctx:     ctx,
		sshPool: utils.GetGlobalSSHPool(),
	}
}

// SetProviderID 设置当前操作的ProviderID
func (s *Service) SetProviderID(providerID uint) {
	s.providerID = providerID
}

// InitializePmacctForInstance 为实例初始化流量监控
// 监控容器/虚拟机通过NAT映射的流量
// 优先使用PortIP（端口映射IP），如果没有则使用Endpoint（SSH连接IP）
func (s *Service) InitializePmacctForInstance(instanceID uint) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("failed to find instance: %w", err)
	}

	// 获取provider配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.First(&providerRecord, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("failed to find provider: %w", err)
	}

	// 检查provider是否启用了流量统计
	if !providerRecord.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量统计，跳过pmacct监控初始化",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Uint("providerID", providerRecord.ID),
			zap.String("providerName", providerRecord.Name))
		return nil
	}

	// 获取provider实例
	providerInstance, exists := providerService.GetProviderService().GetProviderByID(instance.ProviderID)
	if !exists {
		return fmt.Errorf("provider ID %d not found", instance.ProviderID)
	}

	s.SetProviderID(instance.ProviderID)

	global.APP_LOG.Info("开始初始化流量监控",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("providerType", providerInstance.GetType()))

	// 检查是否已存在监控记录（包括启用和停用的）
	var existingMonitor monitoringModel.PmacctMonitor
	if err := global.APP_DB.Where("instance_id = ?", instanceID).First(&existingMonitor).Error; err == nil {
		// 如果已存在且启用，说明是正常状态，跳过初始化
		if existingMonitor.IsEnabled {
			global.APP_LOG.Info("实例已存在启用的监控记录，跳过初始化",
				zap.Uint("instanceID", instanceID),
				zap.Uint("monitorID", existingMonitor.ID),
				zap.String("mappedIP", existingMonitor.MappedIP))
			return nil
		}

		// 如果已存在但停用，说明是重置等场景，先删除旧记录
		global.APP_LOG.Info("发现停用的监控记录，删除后重新创建",
			zap.Uint("instanceID", instanceID),
			zap.Uint("oldMonitorID", existingMonitor.ID),
			zap.Bool("oldIsEnabled", existingMonitor.IsEnabled))

		if err := global.APP_DB.Unscoped().Delete(&existingMonitor).Error; err != nil {
			return fmt.Errorf("删除旧监控记录失败: %w", err)
		}
	}

	// 确定要监控的IPv4地址
	// 优先使用PortIP（如果配置了端口映射专用IP）
	// 否则使用Endpoint（SSH连接的IP地址）
	var monitorIPv4 string
	var ipv4Source string

	if providerRecord.PortIP != "" {
		monitorIPv4 = providerRecord.PortIP
		ipv4Source = "PortIP"
	} else if providerRecord.Endpoint != "" {
		monitorIPv4 = providerRecord.Endpoint
		ipv4Source = "Endpoint"
	}

	// 如果IPv4包含端口，提取IP地址部分
	if monitorIPv4 != "" {
		if idx := strings.Index(monitorIPv4, ":"); idx != -1 {
			monitorIPv4 = monitorIPv4[:idx]
		}
	}

	// 确定要监控的IPv6地址
	var monitorIPv6 string
	var ipv6Source string

	// 检查是否有IPv6映射配置
	if instance.PublicIPv6 != "" {
		monitorIPv6 = instance.PublicIPv6
		ipv6Source = "PublicIPv6"
	} else if instance.IPv6Address != "" {
		monitorIPv6 = instance.IPv6Address
		ipv6Source = "IPv6Address"
	}

	// 至少需要一个IP地址（IPv4或IPv6）
	if monitorIPv4 == "" && monitorIPv6 == "" {
		return fmt.Errorf("provider has no IPv4 or IPv6 address configured")
	}

	global.APP_LOG.Info("确定pmacct监控IP",
		zap.Uint("instanceID", instanceID),
		zap.String("publicIPv4", monitorIPv4),
		zap.String("ipv4Source", ipv4Source),
		zap.String("publicIPv6", monitorIPv6),
		zap.String("ipv6Source", ipv6Source))

	// 在Provider宿主机上安装和配置pmacct
	if err := s.installPmacct(providerInstance); err != nil {
		return fmt.Errorf("failed to install pmacct: %w", err)
	}

	// 如果 PrivateIP 为空，尝试使用 Provider 的标准方法获取
	if instance.PrivateIP == "" {
		global.APP_LOG.Warn("实例PrivateIP为空，尝试通过Provider获取",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.String("providerType", providerInstance.GetType()))

		var privateIP string
		var err error

		// 使用 Provider 接口的标准方法获取私有IP
		switch prov := providerInstance.(type) {
		case interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}:
			// LXD/Incus/Proxmox Provider
			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			defer cancel()
			privateIP, err = prov.GetInstanceIPv4(ctx, instance.Name)
		default:
			global.APP_LOG.Debug("Provider不支持GetInstanceIPv4方法，跳过",
				zap.String("providerType", providerInstance.GetType()))
		}
		if err == nil && privateIP != "" {
			// 更新数据库中的 PrivateIP
			global.APP_DB.Model(&instance).Update("private_ip", privateIP)
			instance.PrivateIP = privateIP // 更新内存中的值
			global.APP_LOG.Info("成功通过Provider获取并更新实例PrivateIP",
				zap.String("instanceName", instance.Name),
				zap.String("privateIP", privateIP),
				zap.String("providerType", providerInstance.GetType()))
		} else if err != nil {
			global.APP_LOG.Warn("通过Provider获取PrivateIP失败",
				zap.String("instanceName", instance.Name),
				zap.Error(err))
		}
	}

	// 配置pmacct监控规则
	// 网络架构说明：
	// - NAT虚拟化（Docker/LXD/Incus）：BPF过滤器使用PrivateIP（内网IP），因为在veth接口上看到的是NAT前的地址
	// - 如果PrivateIP为空：不限制host，只过滤内网间流量（捕获所有通过该接口的外部流量）
	// - IPv6：直接使用公网IPv6（通常不经过NAT）

	// 确定用于BPF过滤器的IP
	// IPv4: 使用PrivateIP（NAT场景），如果为空则不限制host
	bpfIPv4 := instance.PrivateIP
	// IPv6: 直接使用公网IPv6（不经过NAT）
	bpfIPv6 := monitorIPv6

	// 即使 bpfIPv4 和 bpfIPv6 都为空，也可以继续（只过滤内网流量）
	if bpfIPv4 == "" && bpfIPv6 == "" {
		global.APP_LOG.Warn("无法获取实例的监控IP，将监控所有非内网流量",
			zap.String("instanceName", instance.Name),
			zap.String("PrivateIP", instance.PrivateIP),
			zap.String("MappedIP", monitorIPv4),
			zap.String("IPv6", monitorIPv6))
	}

	global.APP_LOG.Info("配置pmacct监控",
		zap.String("bpfIPv4", bpfIPv4),
		zap.String("bpfIPv6", bpfIPv6),
		zap.String("mappedIPv4", monitorIPv4),
		zap.String("mappedIPv6", monitorIPv6))

	// 配置pmacct
	if err := s.configurePmacctForIPs(providerInstance, instance.Name, bpfIPv4, bpfIPv6, monitorIPv4, monitorIPv6); err != nil {
		return fmt.Errorf("failed to configure pmacct: %w", err)
	}

	// 在数据库中创建监控记录（保存MappedIP和网络接口信息）
	// 网络接口信息会在configurePmacctForIPs中更新到instance表
	pmacctMonitor := &monitoringModel.PmacctMonitor{
		InstanceID:   instanceID,
		ProviderID:   instance.ProviderID,
		ProviderType: providerInstance.GetType(),
		MappedIP:     monitorIPv4, // 公网IPv4（用于显示）
		MappedIPv6:   monitorIPv6, // 公网IPv6（用于显示）
		IsEnabled:    true,
		LastSync:     time.Now(),
	}

	if err := global.APP_DB.Create(pmacctMonitor).Error; err != nil {
		return fmt.Errorf("failed to create pmacct monitor record: %w", err)
	}

	global.APP_LOG.Info("pmacct监控初始化成功",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("monitorIPv4", monitorIPv4),
		zap.String("ipv4Source", ipv4Source),
		zap.String("monitorIPv6", monitorIPv6),
		zap.String("ipv6Source", ipv6Source))

	return nil
}

// GetPmacctSummary 获取实例的pmacct流量汇总
func (s *Service) GetPmacctSummary(instanceID uint) (*monitoringModel.PmacctSummary, error) {
	var monitor monitoringModel.PmacctMonitor
	if err := global.APP_DB.Where("instance_id = ? AND is_enabled = ?", instanceID, true).First(&monitor).Error; err != nil {
		return nil, fmt.Errorf("pmacct monitor not found: %w", err)
	}

	// 检查Provider是否启用了流量统计
	var instance providerModel.Instance
	if err := global.APP_DB.Select("provider_id").First(&instance, instanceID).Error; err != nil {
		return nil, fmt.Errorf("instance not found: %w", err)
	}

	var providerRecord providerModel.Provider
	if err := global.APP_DB.Select("enable_traffic_control").First(&providerRecord, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("provider not found: %w", err)
	}

	// 如果Provider未启用流量统计，返回空数据
	if !providerRecord.EnableTrafficControl {
		return &monitoringModel.PmacctSummary{
			InstanceID: instanceID,
			MappedIP:   monitor.MappedIP,
			MappedIPv6: monitor.MappedIPv6,
			Today: &monitoringModel.PmacctTrafficRecord{
				InstanceID: instanceID,
				RxBytes:    0,
				TxBytes:    0,
				TotalBytes: 0,
			},
			ThisMonth: &monitoringModel.PmacctTrafficRecord{
				InstanceID: instanceID,
				RxBytes:    0,
				TxBytes:    0,
				TotalBytes: 0,
			},
			AllTime: &monitoringModel.PmacctTrafficRecord{
				InstanceID: instanceID,
				RxBytes:    0,
				TxBytes:    0,
				TotalBytes: 0,
			},
			History: []*monitoringModel.PmacctTrafficRecord{},
		}, nil
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()

	// 获取今日流量
	today := s.aggregateTrafficRecords(instanceID, year, month, day, 0)

	// 获取本月流量
	thisMonth := s.aggregateTrafficRecords(instanceID, year, month, 0, 0)

	// 获取总流量
	allTime := s.aggregateTrafficRecords(instanceID, 0, 0, 0, 0)

	// 获取历史记录（最近30天）
	history := s.getAggregatedHistory(instanceID, 30)

	return &monitoringModel.PmacctSummary{
		InstanceID: instanceID,
		MappedIP:   monitor.MappedIP,
		MappedIPv6: monitor.MappedIPv6,
		Today:      today,
		ThisMonth:  thisMonth,
		AllTime:    allTime,
		History:    history,
	}, nil
}

// updateInstanceNetworkInterfaces 更新实例的网络接口信息到数据库
// 这个方法接收的是检测到的实际接口，需要从configurePmacctForIPs传递正确的IPv4/IPv6接口
func (s *Service) updateInstanceNetworkInterfaces(instanceName, ipv4Interface, ipv6Interface string) {
	updateData := map[string]interface{}{}
	if ipv4Interface != "" {
		updateData["pmacct_interface_v4"] = ipv4Interface
	}
	if ipv6Interface != "" {
		updateData["pmacct_interface_v6"] = ipv6Interface
	}

	if len(updateData) > 0 {
		if err := global.APP_DB.Model(&providerModel.Instance{}).Where("name = ?", instanceName).Updates(updateData).Error; err != nil {
			global.APP_LOG.Warn("更新实例网络接口信息失败",
				zap.String("instance", instanceName),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("成功更新实例网络接口信息",
				zap.String("instance", instanceName),
				zap.String("ipv4Interface", ipv4Interface),
				zap.String("ipv6Interface", ipv6Interface))
		}
	}
}
