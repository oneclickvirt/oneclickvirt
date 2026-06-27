package user

import (
	"strings"
	"testing"

	"oneclickvirt/constant"
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
