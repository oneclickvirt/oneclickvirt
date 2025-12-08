package resources

import (
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// DeleteInstancePortMappings 删除实例的所有端口映射并释放端口
func (s *PortMappingService) DeleteInstancePortMappings(instanceID uint) error {
	// 获取实例的所有端口映射
	var ports []provider.Port
	if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		global.APP_LOG.Error("获取实例端口映射失败", zap.Error(err))
		return err
	}

	// 使用事务确保端口释放的原子性
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		return s.DeleteInstancePortMappingsInTx(tx, instanceID)
	})
}

// DeleteInstancePortMappingsInTx 在事务中删除实例的所有端口映射并释放端口
func (s *PortMappingService) DeleteInstancePortMappingsInTx(tx *gorm.DB, instanceID uint) error {
	// 获取实例的所有端口映射
	var ports []provider.Port
	if err := tx.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		global.APP_LOG.Error("获取实例端口映射失败", zap.Error(err))
		return err
	}

	// 直接删除端口映射记录（失败实例的端口直接释放）
	if err := tx.Where("instance_id = ?", instanceID).Delete(&provider.Port{}).Error; err != nil {
		return fmt.Errorf("删除端口映射失败: %v", err)
	}

	// 按Provider分组，更新NextAvailablePort以便端口重用
	portsByProvider := make(map[uint][]int)
	for _, port := range ports {
		portsByProvider[port.ProviderID] = append(portsByProvider[port.ProviderID], port.HostPort)
	}

	// 为每个Provider更新NextAvailablePort以端口重用
	for providerID, releasedPorts := range portsByProvider {
		if err := s.optimizeNextAvailablePortInTx(tx, providerID, releasedPorts); err != nil {
			global.APP_LOG.Warn("Provider端口重用失败", zap.Uint("providerId", providerID), zap.Error(err))
			// 不阻止删除操作，只记录警告
		}
	}

	global.APP_LOG.Info("删除实例端口映射成功",
		zap.Uint("instance_id", instanceID),
		zap.Int("releasedPortCount", len(ports)))

	return nil
}

// BatchDeletePortMappingWithTask 批量删除端口映射（通过任务系统异步执行，仅支持删除手动添加的端口）
// 返回任务数据列表（由调用者创建和启动任务）
func (s *PortMappingService) BatchDeletePortMappingWithTask(req admin.BatchDeletePortMappingRequest) ([]*admin.DeletePortMappingTaskRequest, error) {
	// 获取所有要删除的端口
	var ports []provider.Port
	if err := global.APP_DB.Where("id IN ?", req.IDs).Find(&ports).Error; err != nil {
		return nil, fmt.Errorf("获取端口映射失败: %v", err)
	}

	if len(ports) == 0 {
		return nil, fmt.Errorf("未找到要删除的端口映射")
	}

	// 检查是否都是手动添加的端口
	for _, port := range ports {
		if port.PortType != "manual" {
			return nil, fmt.Errorf("端口 %d 是区间映射端口，不能删除", port.ID)
		}
	}

	// 将所有端口状态更新为 deleting
	if err := global.APP_DB.Model(&provider.Port{}).Where("id IN ?", req.IDs).Update("status", "deleting").Error; err != nil {
		global.APP_LOG.Warn("更新端口状态为deleting失败", zap.Error(err))
	}

	// 为每个端口创建任务数据
	var taskDataList []*admin.DeletePortMappingTaskRequest
	for _, port := range ports {
		taskData := &admin.DeletePortMappingTaskRequest{
			PortID:     port.ID,
			InstanceID: port.InstanceID,
			ProviderID: port.ProviderID,
		}
		taskDataList = append(taskDataList, taskData)
	}

	global.APP_LOG.Info("准备创建批量端口删除任务",
		zap.Int("count", len(taskDataList)),
		zap.Any("port_ids", req.IDs))

	return taskDataList, nil
}

// DeletePortMappingWithTask 删除端口映射（通过任务系统异步执行，支持删除手动和批量添加的端口）
// 返回任务数据（由调用者创建和启动任务）
func (s *PortMappingService) DeletePortMappingWithTask(id uint) (*admin.DeletePortMappingTaskRequest, error) {
	var port provider.Port
	if err := global.APP_DB.Where("id = ?", id).First(&port).Error; err != nil {
		return nil, fmt.Errorf("端口映射不存在")
	}

	// 只允许删除手动添加的端口和批量添加的端口
	if port.PortType != "manual" && port.PortType != "batch" {
		return nil, fmt.Errorf("不能删除区间映射的端口，此类端口随实例创建和删除")
	}

	// 获取实例和 Provider 信息验证
	var instance provider.Instance
	if err := global.APP_DB.Where("id = ?", port.InstanceID).First(&instance).Error; err != nil {
		return nil, fmt.Errorf("关联的实例不存在")
	}

	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", port.ProviderID).First(&providerInfo).Error; err != nil {
		return nil, fmt.Errorf("关联的 Provider 不存在")
	}

	// 将端口状态更新为 deleting
	if err := global.APP_DB.Model(&port).Update("status", "deleting").Error; err != nil {
		global.APP_LOG.Warn("更新端口状态为deleting失败", zap.Error(err))
	}

	// 创建任务数据
	taskData := &admin.DeletePortMappingTaskRequest{
		PortID:     port.ID,
		InstanceID: port.InstanceID,
		ProviderID: port.ProviderID,
	}

	if port.PortCount > 1 {
		global.APP_LOG.Info("准备创建端口段删除任务",
			zap.Uint("port_id", port.ID),
			zap.Uint("instance_id", port.InstanceID),
			zap.String("port_range", fmt.Sprintf("%d-%d", port.HostPort, port.HostPortEnd)),
			zap.Int("port_count", port.PortCount))
	} else {
		global.APP_LOG.Info("准备创建端口删除任务",
			zap.Uint("port_id", port.ID),
			zap.Uint("instance_id", port.InstanceID),
			zap.Int("host_port", port.HostPort))
	}

	return taskData, nil
}
