package traffic

import (
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
)

const (
	trafficDataSourceNone   = "none"
	trafficDataSourceAgent  = "agent"
	trafficDataSourcePmacct = "pmacct"
	trafficDataSourceMixed  = "mixed"
)

// trafficDataSourceForMethod maps legacy empty values to pmacct because the
// scheduler treats an unset traffic_sync_method as the legacy collector.
func trafficDataSourceForMethod(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case trafficDataSourceAgent:
		return trafficDataSourceAgent
	case "", trafficDataSourcePmacct:
		return trafficDataSourcePmacct
	default:
		return trafficDataSourceNone
	}
}

func trafficDataSourceFromMethods(methods []string) string {
	hasAgent := false
	hasPmacct := false
	for _, method := range methods {
		switch trafficDataSourceForMethod(method) {
		case trafficDataSourceAgent:
			hasAgent = true
		case trafficDataSourcePmacct:
			hasPmacct = true
		}
	}

	switch {
	case hasAgent && hasPmacct:
		return trafficDataSourceMixed
	case hasAgent:
		return trafficDataSourceAgent
	case hasPmacct:
		return trafficDataSourcePmacct
	default:
		return trafficDataSourceNone
	}
}

func trafficDataSourceForProvider(p provider.Provider) string {
	if !p.EnableTrafficControl {
		return trafficDataSourceNone
	}
	return trafficDataSourceForMethod(p.TrafficSyncMethod)
}

func trafficDataSourceForUser(userID uint) (string, error) {
	sources, err := trafficDataSourcesForUsers([]uint{userID}, 0)
	if err != nil {
		return trafficDataSourceNone, err
	}
	return sources[userID], nil
}

func defaultTrafficDataSources(userIDs []uint) map[uint]string {
	uniqueIDs := normalizeTrafficUserIDs(userIDs)
	result := make(map[uint]string, len(uniqueIDs))
	for _, userID := range uniqueIDs {
		result[userID] = trafficDataSourceNone
	}
	return result
}

// trafficDataSourcesForUsers loads the collection method for every requested
// user in one join, so rank pages do not perform a provider query per user.
func trafficDataSourcesForUsers(userIDs []uint, ownerAdminID uint) (map[uint]string, error) {
	uniqueIDs := normalizeTrafficUserIDs(userIDs)
	result := defaultTrafficDataSources(uniqueIDs)
	if len(uniqueIDs) == 0 {
		return result, nil
	}

	type sourceRow struct {
		UserID            uint   `gorm:"column:user_id"`
		TrafficSyncMethod string `gorm:"column:traffic_sync_method"`
	}
	query := global.APP_DB.Table("instances i").
		Select("DISTINCT i.user_id, COALESCE(p.traffic_sync_method, '') AS traffic_sync_method").
		Joins("INNER JOIN providers p ON p.id = i.provider_id").
		Where("i.user_id IN ? AND i.deleted_at IS NULL AND p.enable_traffic_control = ?", uniqueIDs, true)
	if ownerAdminID > 0 {
		query = query.Where("p.owner_admin_id = ?", ownerAdminID)
	}

	var rows []sourceRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	methodsByUserID := make(map[uint][]string, len(uniqueIDs))
	for _, row := range rows {
		methodsByUserID[row.UserID] = append(methodsByUserID[row.UserID], row.TrafficSyncMethod)
	}
	for _, userID := range uniqueIDs {
		result[userID] = trafficDataSourceFromMethods(methodsByUserID[userID])
	}
	return result, nil
}
