package pmacct

import (
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
)

// CollectInstanceTraffic commits the remaining SQLite samples before files or
// monitor mappings are removed. Collection errors preserve both for a retry.
func (s *Service) CollectInstanceTraffic(instanceID uint) error {
	var monitors []monitoringModel.PmacctMonitor
	if err := global.APP_DB.WithContext(s.ctx).Where("instance_id = ?", instanceID).Find(&monitors).Error; err != nil {
		return err
	}
	if len(monitors) == 0 {
		return nil
	}
	var instance providerModel.Instance
	if err := global.APP_DB.WithContext(s.ctx).Unscoped().First(&instance, instanceID).Error; err != nil {
		return err
	}
	for i := range monitors {
		if err := s.CollectTrafficFromSQLite(&instance, &monitors[i]); err != nil {
			return err
		}
	}
	return s.ctx.Err()
}
