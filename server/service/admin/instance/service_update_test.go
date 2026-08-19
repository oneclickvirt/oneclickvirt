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

func TestApplyUpdateInstanceRequestAppliesExplicitSSHAccessFields(t *testing.T) {
	oldPassword := "old-password"
	newPassword := "new-password"
	newKey := "-----BEGIN PRIVATE KEY-----\nexample\n-----END PRIVATE KEY-----"
	inst := providerModel.Instance{
		SSHHost:  "198.51.100.10",
		SSHPort:  22,
		Username: "root",
		Password: oldPassword,
	}
	req := admin.UpdateInstanceRequest{
		ProvidedFields: map[string]bool{
			"sshHost":  true,
			"sshPort":  true,
			"username": true,
			"password": true,
			"sshKey":   true,
		},
		SSHHost:  "[2001:db8::42]",
		SSHPort:  22042,
		Username: "admin",
		Password: &newPassword,
		SSHKey:   &newKey,
	}

	applyUpdateInstanceRequest(&inst, req)

	if inst.SSHHost != "2001:db8::42" || inst.SSHPort != 22042 || inst.Username != "admin" {
		t.Fatalf("SSH access fields not applied: %#v", inst)
	}
	if inst.Password != newPassword || inst.SSHKey != newKey {
		t.Fatalf("SSH credentials not applied")
	}
}

func TestValidateUpdateInstanceAccessFieldsRejectsInvalidHostAndPort(t *testing.T) {
	for _, req := range []admin.UpdateInstanceRequest{
		{ProvidedFields: map[string]bool{"sshPort": true}, SSHPort: 65536},
		{ProvidedFields: map[string]bool{"sshHost": true}, SSHHost: "example.com:2222"},
		{ProvidedFields: map[string]bool{"sshHost": true}, SSHHost: "https://example.com"},
	} {
		if err := validateUpdateInstanceAccessFields(req); err == nil {
			t.Fatalf("expected validation failure for %#v", req)
		}
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
