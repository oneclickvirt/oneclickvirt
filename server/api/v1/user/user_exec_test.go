package user

import (
	"strings"
	"testing"

	"oneclickvirt/constant"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

func TestGetExecCommandQuotesInstanceName(t *testing.T) {
	instanceName := "ct'; touch /tmp/pwned; echo '"
	cmd, err := getExecCommand(constant.ProviderTypeDocker, instanceName)
	if err != nil {
		t.Fatal(err)
	}
	quotedName := utils.ShellSingleQuote(instanceName)
	if !strings.Contains(cmd, "docker exec -it "+quotedName+" ") {
		t.Fatalf("instance name was not shell quoted: %s", cmd)
	}
	if strings.Contains(cmd, "docker exec -it "+instanceName+" ") {
		t.Fatalf("raw instance name appeared in command: %s", cmd)
	}
}

func TestExecUsesProviderIdentifierAfterPanelRename(t *testing.T) {
	instance := providerModel.Instance{Name: "renamed-in-panel", ProviderVMID: "runtime-container"}
	command, err := getExecCommand(constant.ProviderTypeDocker, instance.ProviderInstanceIdentifier())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "docker exec -it 'runtime-container'") || strings.Contains(command, "renamed-in-panel") {
		t.Fatalf("exec command does not use provider identifier: %s", command)
	}
}
