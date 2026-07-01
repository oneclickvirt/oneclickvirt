package instance

import (
	"testing"

	"oneclickvirt/constant"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func TestApplyUpdateInstanceRequestPreservesOmittedFields(t *testing.T) {
	inst := providerModel.Instance{
		Name:   "old-name",
		CPU:    2,
		Memory: 2048,
		Disk:   20,
		Status: constant.InstanceStatusRunning,
	}
	req := admin.UpdateInstanceRequest{
		ProvidedFields: map[string]bool{"name": true},
		Name:           "new-name",
	}

	applyUpdateInstanceRequest(&inst, req)

	if inst.Name != "new-name" {
		t.Fatalf("name = %q, want new-name", inst.Name)
	}
	if inst.CPU != 2 || inst.Memory != 2048 || inst.Disk != 20 || inst.Status != constant.InstanceStatusRunning {
		t.Fatalf("omitted fields changed: cpu=%d memory=%d disk=%d status=%q", inst.CPU, inst.Memory, inst.Disk, inst.Status)
	}
}

func TestApplyUpdateInstanceRequestAppliesExplicitResourceFields(t *testing.T) {
	inst := providerModel.Instance{
		Name:   "old-name",
		CPU:    1,
		Memory: 1024,
		Disk:   10,
		Status: constant.InstanceStatusRunning,
	}
	req := admin.UpdateInstanceRequest{
		ProvidedFields: map[string]bool{
			"cpu":    true,
			"memory": true,
			"disk":   true,
			"status": true,
		},
		CPU:    2,
		Memory: 4096,
		Disk:   40,
		Status: constant.InstanceStatusStopped,
	}

	applyUpdateInstanceRequest(&inst, req)

	if inst.CPU != 2 || inst.Memory != 4096 || inst.Disk != 40 || inst.Status != constant.InstanceStatusStopped {
		t.Fatalf("explicit fields not applied: cpu=%d memory=%d disk=%d status=%q", inst.CPU, inst.Memory, inst.Disk, inst.Status)
	}
	if inst.Name != "old-name" {
		t.Fatalf("omitted name changed to %q", inst.Name)
	}
}

func TestPreserveProviderIdentifierBeforeRenameCapturesOldName(t *testing.T) {
	inst := providerModel.Instance{Name: "remote-name"}
	req := admin.UpdateInstanceRequest{
		ProvidedFields: map[string]bool{"name": true},
		Name:           "display-name",
	}

	preserveProviderIdentifierBeforeRename(&inst, req)
	applyUpdateInstanceRequest(&inst, req)

	if inst.Name != "display-name" {
		t.Fatalf("name = %q, want display-name", inst.Name)
	}
	if inst.ProviderVMID != "remote-name" {
		t.Fatalf("provider vm id = %q, want remote-name", inst.ProviderVMID)
	}
}

func TestPreserveProviderIdentifierBeforeRenameKeepsExistingRemoteID(t *testing.T) {
	inst := providerModel.Instance{Name: "old-display", ProviderVMID: "remote-id"}
	req := admin.UpdateInstanceRequest{
		ProvidedFields: map[string]bool{"name": true},
		Name:           "new-display",
	}

	preserveProviderIdentifierBeforeRename(&inst, req)
	applyUpdateInstanceRequest(&inst, req)

	if inst.ProviderVMID != "remote-id" {
		t.Fatalf("provider vm id = %q, want remote-id", inst.ProviderVMID)
	}
}
