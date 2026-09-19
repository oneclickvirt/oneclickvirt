package task

import (
	"fmt"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

// A reset keeps the port reservation ID but transfers it to a new instance.
// Old queued create/delete commands must not act on the replacement guest.
func validatePortTaskOwnership(task *adminModel.Task, port providerModel.Port, instanceID, providerID uint) error {
	if instanceID == 0 && task.InstanceID != nil {
		instanceID = *task.InstanceID
	}
	if providerID == 0 && task.ProviderID != nil {
		providerID = *task.ProviderID
	}
	if (instanceID != 0 && instanceID != port.InstanceID) || (providerID != 0 && providerID != port.ProviderID) || port.Status == "restoring" {
		return fmt.Errorf("端口 %d 的归属已变化或正在重建，拒绝执行旧任务", port.ID)
	}
	// Legacy tasks may contain only portId. They cannot safely disambiguate a
	// reservation changed since submission; require a fresh task in that case.
	if instanceID == 0 && !task.CreatedAt.IsZero() && port.UpdatedAt.After(task.CreatedAt) {
		return fmt.Errorf("端口 %d 在旧任务提交后发生变化，请重新提交", port.ID)
	}
	return nil
}
