package podman

import (
	"context"
	"strings"
	"testing"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func TestPodmanInfrastructureContainerIsNotDiscoveredOrManaged(t *testing.T) {
	previousLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previousLogger }()
	ctx := context.Background()
	p := NewPodmanProvider().(*PodmanProvider)
	executor := &routedPodmanExecutor{outputs: []string{
		`[{"Id":"service-id","Name":"/ndpresponder","State":{"Status":"running","Running":true},"Config":{"Image":"spiritlhl/ndpresponder_x86"}}]`,
		"",
	}}
	p.sshClient.SetExecutor(executor)
	discovered, err := p.sshDiscoverInstances(ctx)
	if err != nil || len(discovered) != 0 {
		t.Fatalf("discovered infrastructure container: %#v, %v", discovered, err)
	}

	executor = &routedPodmanExecutor{outputs: []string{"NAMES\tSTATUS\tIMAGE\tID\tCREATED\nndpresponder\tUp\tservice-image\tservice-id\tnow\n"}}
	p.sshClient.SetExecutor(executor)
	listed, err := p.sshListInstances(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("listed infrastructure container: %#v, %v", listed, err)
	}

	for _, operation := range []struct {
		name string
		run  func(string) error
	}{
		{"stop", func(id string) error { return p.sshStopInstance(ctx, id) }},
		{"delete", func(id string) error { return p.sshDeleteInstance(ctx, id) }},
	} {
		for _, id := range []string{"ndpresponder", "service-id"} {
			executor = &routedPodmanExecutor{outputs: []string{"/ndpresponder\n"}}
			p.sshClient.SetExecutor(executor)
			if err := operation.run(id); err == nil || !strings.Contains(err.Error(), "infrastructure") {
				t.Fatalf("%s %s was accepted: %v", operation.name, id, err)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, " stop ") || strings.Contains(command, " rm ") || strings.Contains(command, " kill ") {
					t.Fatalf("%s %s mutated infrastructure: %q", operation.name, id, command)
				}
			}
		}
	}
}
