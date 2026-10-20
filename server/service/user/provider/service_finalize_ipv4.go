package provider

import (
	"fmt"
	"net"
	"strings"

	"gorm.io/gorm"
	providerModel "oneclickvirt/model/provider"
)

func applyAllocatedPublicIPv4(db *gorm.DB, instance providerModel.Instance, node providerModel.Provider, updates map[string]interface{}) error {
	kind := effectiveInstanceNetworkType(instance.NetworkType, node.NetworkType)
	if kind != "dedicated_ipv4" && kind != "dedicated_ipv4_ipv6" {
		return nil
	}
	var allocation providerModel.ProviderIPv4Pool
	err := db.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instance.ProviderID, instance.ID, true).First(&allocation).Error
	if err != nil {
		return fmt.Errorf("独立IPv4实例缺少有效地址池绑定: %w", err)
	}
	address := net.ParseIP(strings.TrimSpace(allocation.Address))
	if address == nil || address.To4() == nil || !address.IsGlobalUnicast() || address.IsPrivate() {
		return fmt.Errorf("独立IPv4分配地址无效")
	}
	updates["public_ip"] = address.String()
	return nil
}
