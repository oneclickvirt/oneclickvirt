package provider

import (
	"testing"

	providerModel "oneclickvirt/model/provider"
)

func TestBuildCopyInstanceResourceUpdatesSkipsUnsetLimits(t *testing.T) {
	updates := buildCopyInstanceResourceUpdates(2, 0, 1024)

	if updates["cpu"] != 2 {
		t.Fatalf("expected CPU limit to be copied, got %#v", updates["cpu"])
	}
	if _, ok := updates["memory"]; ok {
		t.Fatalf("expected unset memory limit to be skipped, got %#v", updates["memory"])
	}
	if updates["disk"] != int64(1024) {
		t.Fatalf("expected disk limit to be copied, got %#v", updates["disk"])
	}
}

func TestBuildCopyResourceUsageUpdatesUsesPositiveDeltas(t *testing.T) {
	instance := &providerModel.Instance{
		CPU:    2,
		Memory: 512,
		Disk:   2048,
	}

	updates := buildCopyResourceUsageUpdates(instance, 2, 1024, 0)

	if updates.cpuDelta != 0 {
		t.Fatalf("expected unchanged CPU to avoid duplicate usage, got %d", updates.cpuDelta)
	}
	if updates.memoryDelta != 512 {
		t.Fatalf("expected memory usage delta to be recorded, got %d", updates.memoryDelta)
	}
	if updates.diskDelta != 0 {
		t.Fatalf("expected unset disk limit to avoid subtracting usage, got %d", updates.diskDelta)
	}
}
