package traffic

import (
	"fmt"

	"oneclickvirt/global"
)

func (s *QueryService) BatchGetUsersMonthlyTraffic(userIDs []uint, year, month int) (map[uint]*TrafficStats, error) {
	results := make(map[uint]*TrafficStats, len(userIDs))
	if len(userIDs) == 0 {
		return results, nil
	}
	for _, userID := range userIDs {
		results[userID] = &TrafficStats{}
	}

	type instanceUserRow struct {
		InstanceID uint
		UserID     uint
	}
	var rows []instanceUserRow
	if err := global.APP_DB.Unscoped().Table("instances").
		Select("id as instance_id, user_id").
		Where("user_id IN ?", userIDs).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("batch query user instances failed: %w", err)
	}
	if len(rows) == 0 {
		return results, nil
	}

	instanceIDs := make([]uint, 0, len(rows))
	instanceUserMap := make(map[uint]uint, len(rows))
	for _, row := range rows {
		if row.InstanceID == 0 || row.UserID == 0 {
			continue
		}
		instanceIDs = append(instanceIDs, row.InstanceID)
		instanceUserMap[row.InstanceID] = row.UserID
	}
	if len(instanceIDs) == 0 {
		return results, nil
	}

	instanceStats, err := s.BatchGetInstancesMonthlyTraffic(instanceIDs, year, month)
	if err != nil {
		return nil, err
	}
	for instanceID, stats := range instanceStats {
		userID := instanceUserMap[instanceID]
		if userID == 0 {
			continue
		}
		total := results[userID]
		if total == nil {
			total = &TrafficStats{}
			results[userID] = total
		}
		total.RxBytes += stats.RxBytes
		total.TxBytes += stats.TxBytes
		total.TotalBytes += stats.TotalBytes
		total.ActualUsageMB += stats.ActualUsageMB
	}

	return results, nil
}
