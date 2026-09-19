package agent

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
)

// RestoreControllerPortForwards validates the replacement guest's mappings in
// one read and publishes their new targets in one update. The caller owns the
// final active/failed status batch; listener I/O is always outside a DB tx.
func RestoreControllerPortForwards(ctx context.Context, instanceID, providerID uint, ports []providerModel.Port) map[uint]error {
	failures := make(map[uint]error)
	if len(ports) == 0 {
		return failures
	}
	failAll := func(err error) map[uint]error {
		for _, port := range ports {
			failures[port.ID] = err
		}
		return failures
	}
	if err := ctx.Err(); err != nil {
		return failAll(err)
	}
	if global.APP_DB == nil {
		return failAll(gorm.ErrInvalidDB)
	}
	ids := make([]uint, 0, len(ports))
	for _, port := range ports {
		ids = append(ids, port.ID)
	}
	var current []providerModel.Port
	if err := global.APP_DB.WithContext(ctx).Where("id IN ? AND instance_id = ? AND provider_id = ?", ids, instanceID, providerID).Find(&current).Error; err != nil {
		return failAll(err)
	}
	byID := make(map[uint]providerModel.Port, len(current))
	for _, port := range current {
		byID[port.ID] = port
	}
	var valid []providerModel.Port
	var cases strings.Builder
	cases.WriteString("CASE id")
	var args []interface{}
	ids = ids[:0]
	for _, port := range ports {
		stored, ok := byID[port.ID]
		if !ok || stored.HostPort != port.HostPort || stored.GuestPort != port.GuestPort || stored.MappingType != "controller" || stored.Protocol != "tcp" || stored.PortCount > 1 || (stored.Status != "restoring" && stored.Status != "active") || strings.TrimSpace(port.InternalHost) == "" {
			failures[port.ID] = fmt.Errorf("controller port %d changed during reset", port.ID)
			continue
		}
		valid = append(valid, port)
		ids = append(ids, port.ID)
		cases.WriteString(" WHEN ? THEN ?")
		args = append(args, port.ID, port.InternalHost)
	}
	if len(valid) == 0 {
		return failures
	}
	cases.WriteString(" ELSE internal_host END")
	result := global.APP_DB.WithContext(ctx).Model(&providerModel.Port{}).
		Where("id IN ? AND instance_id = ? AND provider_id = ? AND status IN ?", ids, instanceID, providerID, []string{"restoring", "active"}).
		Updates(map[string]interface{}{"internal_host": gorm.Expr(cases.String(), args...), "mapping_method": "controller", "status": "pending"})
	if result.Error != nil {
		return failAll(result.Error)
	}
	if result.RowsAffected != int64(len(valid)) {
		return failAll(fmt.Errorf("controller mappings changed during reset"))
	}
	for _, port := range valid {
		if err := ctx.Err(); err != nil {
			failures[port.ID] = err
			continue
		}
		StopControllerPortForward(port.ID)
		// The caller publishes statuses in a batch. Avoid a redundant read and
		// update per listener, while the resolver rejects pending connections.
		port.Status, port.MappingMethod = "active", "controller"
		if err := startControllerPortForwardWithKnownPort(port, port.InternalHost); err != nil {
			failures[port.ID] = err
		}
	}
	return failures
}
