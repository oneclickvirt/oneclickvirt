package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func TestDetectAcceleratorsWithoutDetectionCommandsReturnsEmptyCapability(t *testing.T) {
	result, err := detectAcceleratorsWithExecutor(context.Background(), "lxd", func(context.Context, string) (string, error) {
		return unavailableGPUCommandMarker + "\n", nil
	})
	if err != nil {
		t.Fatalf("detectAcceleratorsWithExecutor() error = %v", err)
	}
	if len(result.devices) != 0 {
		t.Fatalf("devices = %#v, want empty", result.devices)
	}
	if result.detectionAvailable {
		t.Fatal("detectionAvailable = true, want false when every tool is absent")
	}
	if !strings.Contains(result.detectionReason, "没有可用") {
		t.Fatalf("detectionReason = %q, want an unavailable-command explanation", result.detectionReason)
	}
}

func TestDetectAcceleratorsReportsAvailableToolWithoutDevices(t *testing.T) {
	result, err := detectAcceleratorsWithExecutor(context.Background(), "incus", func(_ context.Context, command string) (string, error) {
		if strings.Contains(command, "command -v lspci") {
			return "", nil
		}
		return unavailableGPUCommandMarker + "\n", nil
	})
	if err != nil {
		t.Fatalf("detectAcceleratorsWithExecutor() error = %v", err)
	}
	if len(result.devices) != 0 {
		t.Fatalf("devices = %#v, want empty", result.devices)
	}
	if !result.detectionAvailable {
		t.Fatal("detectionAvailable = false, want true when lspci is available")
	}
	if result.detectionReason != "节点未检测到 GPU/NPU 设备" {
		t.Fatalf("detectionReason = %q, want no-device explanation", result.detectionReason)
	}
}

func TestDetectAcceleratorsParsesDeviceFromInjectedExecutor(t *testing.T) {
	result, err := detectAcceleratorsWithExecutor(context.Background(), "lxd", func(_ context.Context, command string) (string, error) {
		switch {
		case strings.Contains(command, "command -v nvidia-smi"):
			return "0, NVIDIA GeForce RTX 4090, 0000:17:00.0\n", nil
		default:
			return unavailableGPUCommandMarker + "\n", nil
		}
	})
	if err != nil {
		t.Fatalf("detectAcceleratorsWithExecutor() error = %v", err)
	}
	if len(result.devices) != 1 || result.devices[0]["id"] != "0" || result.devices[0]["source"] != "nvidia-smi" {
		t.Fatalf("devices = %#v, want one nvidia-smi device", result.devices)
	}
	if !result.detectionAvailable || result.detectionReason != "" {
		t.Fatalf("capability metadata = (%t, %q), want available with no reason", result.detectionAvailable, result.detectionReason)
	}
}

func TestDetectAcceleratorsPropagatesProviderExecutionFailure(t *testing.T) {
	previousLog := global.APP_LOG
	if previousLog == nil {
		global.APP_LOG = zap.NewNop()
		t.Cleanup(func() { global.APP_LOG = previousLog })
	}
	for _, message := range []string{"provider not connected", "Agent connection not found", "dial unix /run/agent.sock: no such file or directory"} {
		t.Run(message, func(t *testing.T) {
			wantErr := errors.New(message)
			_, err := detectAcceleratorsWithExecutor(context.Background(), "lxd", func(context.Context, string) (string, error) {
				return "", wantErr
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want provider execution failure", err)
			}
		})
	}
}
