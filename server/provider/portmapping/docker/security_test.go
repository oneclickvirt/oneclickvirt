package docker

import (
	"strings"
	"testing"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/portmapping"
	"oneclickvirt/utils"
)

func TestBuildRunCommandQuotesInstanceName(t *testing.T) {
	names := []string{"safe-name", "name with spaces", "quote'$(touch /tmp/pwned);", "line\nbreak"}
	mapper := &DockerPortMapping{BaseProvider: portmapping.NewBaseProvider("docker", nil), cliName: "docker"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			cmd := mapper.buildDockerRunCommand(&providerModel.Instance{Name: name}, "alpine:latest", "", 22000, 22, "tcp")
			quoted := "--name " + utils.ShellSingleQuote(name)
			if !strings.Contains(cmd, quoted) {
				t.Fatalf("command does not quote instance name: %q", cmd)
			}
		})
	}
}
