package lxd

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

type createRouteExecutor struct {
	recordingLXDIPv6Executor
}

func (e *createRouteExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	return "", errors.New("CLI preflight reached")
}

func (e *createRouteExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

type createRouteTransport struct{ calls int }

func (tr *createRouteTransport) RoundTrip(*http.Request) (*http.Response, error) {
	tr.calls++
	return nil, errors.New("unexpected controller API request")
}

func TestLXDContainerCopyAndGPUUseNodeExecutorBeforeAPI(t *testing.T) {
	for _, progress := range []bool{false, true} {
		for _, options := range []struct{ copy, gpu bool }{{copy: true}, {gpu: true}, {copy: true, gpu: true}} {
			p := NewLXDProvider().(*LXDProvider)
			p.connected = true
			p.config = provider.NodeConfig{Host: "127.0.0.1", ExecutionRule: "auto"}
			p.transport.TLSClientConfig = &tls.Config{}
			executor := &createRouteExecutor{}
			p.sshClient = utils.NewSafeShellExecutor(executor)
			transport := &createRouteTransport{}
			p.apiClient = &http.Client{Transport: transport}
			cfg := provider.InstanceConfig{Name: "guest", Image: "fixture", InstanceType: "container", CopyMode: options.copy, CopySourceName: "template", GpuEnabled: options.gpu}
			var err error
			if progress {
				err = p.CreateInstanceWithProgress(context.Background(), cfg, nil)
			} else {
				err = p.CreateInstance(context.Background(), cfg)
			}
			if err == nil || !strings.Contains(err.Error(), "LXD命令不可用") {
				t.Errorf("copy=%v GPU=%v progress=%v: wrong create path: %v", options.copy, options.gpu, progress, err)
			}
			if transport.calls != 0 || len(executor.commands) != 1 || !strings.HasPrefix(executor.commands[0], "command -v lxc") {
				t.Errorf("copy=%v GPU=%v progress=%v: API calls=%d, node commands=%v", options.copy, options.gpu, progress, transport.calls, executor.commands)
			}
		}
	}
}

func TestLXDAPIOnlyRejectsUnsupportedCopyAndGPUBeforeMutation(t *testing.T) {
	for _, progress := range []bool{false, true} {
		for _, copyMode := range []bool{false, true} {
			p := NewLXDProvider().(*LXDProvider)
			p.connected = true
			p.config.ExecutionRule = " API_ONLY "
			p.transport.TLSClientConfig = &tls.Config{}
			transport := &createRouteTransport{}
			p.apiClient = &http.Client{Transport: transport}
			cfg := provider.InstanceConfig{Name: "guest", InstanceType: "container", CopyMode: copyMode, CopySourceName: "template", GpuEnabled: !copyMode}
			var err error
			if progress {
				err = p.CreateInstanceWithProgress(context.Background(), cfg, nil)
			} else {
				err = p.CreateInstance(context.Background(), cfg)
			}
			if err == nil || !strings.Contains(err.Error(), "Agent") || transport.calls != 0 {
				t.Fatalf("unsupported request reached API: copy=%v progress=%v calls=%d error=%v", copyMode, progress, transport.calls, err)
			}
		}
	}
}

type copyGPUFailureExecutor struct {
	recordingLXDIPv6Executor
}

func (e *copyGPUFailureExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	switch {
	case command == "lxc list 'template' --format csv -c n,s,t":
		return "template,STOPPED,CONTAINER\n", nil
	case command == "lxc query '/1.0/instances/guest'":
		return `{"devices":{},"expanded_devices":{}}`, nil
	case command == "lxc config device add 'guest' 'gpu0' gpu 'id=0'":
		return "GPU not available", errors.New("GPU attach failure")
	default:
		return "", nil
	}
}

func (e *copyGPUFailureExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func TestLXDAgentCopiesTemplateButNeverStartsCloneAfterGPUFailure(t *testing.T) {
	p := NewLXDProvider().(*LXDProvider)
	p.connected = true
	p.reverseAgent.Store(true)
	p.config = provider.NodeConfig{Host: "127.0.0.1", ExecutionRule: "auto"}
	// A leftover certificate must not cause post-copy configuration to switch
	// back to the controller's loopback LXD API either.
	p.transport.TLSClientConfig = &tls.Config{}
	executor := &copyGPUFailureExecutor{}
	p.sshClient = utils.NewSafeShellExecutor(executor)
	transport := &createRouteTransport{}
	p.apiClient = &http.Client{Transport: transport}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := p.CreateInstanceWithProgress(ctx, provider.InstanceConfig{Name: "guest", InstanceType: "container", CopyMode: true, CopySourceName: "template", GpuEnabled: true, GpuDeviceIds: "0", Metadata: map[string]string{"network_type": "nat_ipv4"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "配置GPU直通失败") {
		t.Fatalf("GPU failure was swallowed: %v", err)
	}
	copyCount := 0
	for _, command := range executor.commands {
		if command == "lxc copy 'template' 'guest'" {
			copyCount++
		}
		if strings.HasPrefix(command, "lxc start ") || strings.HasPrefix(command, "lxc delete ") || strings.Contains(command, "config device add 'template'") || strings.Contains(command, "config device remove 'template'") {
			t.Fatalf("failed create started/deleted an instance or mutated the source: %s", command)
		}
	}
	if copyCount != 1 || transport.calls != 0 {
		t.Fatalf("copy count=%d, controller API calls=%d; commands=%v", copyCount, transport.calls, executor.commands)
	}
}
