package proxmox

import (
	"strconv"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// persistCreatedRuntimeID stores PVE's immutable VMID/CTID after a successful
// create. The conditional update preserves an ID already filled by discovery
// or a concurrent reconciliation, and deliberately does not hold a database
// transaction while the provider performs remote work.
func (p *ProxmoxProvider) persistCreatedRuntimeID(instanceName string, vmid int) {
	if p.config.ID == 0 || vmid <= 0 || strings.TrimSpace(instanceName) == "" {
		return
	}
	runtimeID := strconv.Itoa(vmid)
	result := global.APP_DB.Model(&providerModel.Instance{}).
		Where("provider_id = ? AND name = ?", p.config.ID, instanceName).
		Where("provider_vm_id IS NULL OR provider_vm_id = '' OR provider_vm_id = ?", instanceName).
		Update("provider_vm_id", runtimeID)
	if result.Error != nil && global.APP_LOG != nil {
		global.APP_LOG.Warn("保存 PVE VMID/CTID 失败",
			zap.Uint("providerID", p.config.ID),
			zap.String("instanceName", instanceName),
			zap.Int("vmid", vmid),
			zap.Error(result.Error))
	}
}
