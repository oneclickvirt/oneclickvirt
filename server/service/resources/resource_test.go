package resources

import (
	"testing"

	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
)

func TestApplyReservationAggregatesRespectsResourceLimitFlags(t *testing.T) {
	provider := providerModel.Provider{
		UsedCPUCores:          2,
		UsedMemory:            1024,
		UsedDisk:              10240,
		ContainerCount:        1,
		VMCount:               1,
		ContainerLimitCPU:     false,
		ContainerLimitMemory:  true,
		ContainerLimitDisk:    false,
		VMLimitCPU:            true,
		VMLimitMemory:         true,
		VMLimitDisk:           true,
		MaxContainerInstances: 5,
		MaxVMInstances:        5,
	}

	applyReservationAggregates(&provider, []providerReservationAggregate{
		{InstanceType: "container", Count: 2, CPU: 4, Memory: 2048, Disk: 20480},
		{InstanceType: "vm", Count: 1, CPU: 2, Memory: 4096, Disk: 40960},
	})

	if provider.ContainerCount != 3 {
		t.Fatalf("expected container reservations to count toward container total, got %d", provider.ContainerCount)
	}
	if provider.VMCount != 2 {
		t.Fatalf("expected VM reservations to count toward VM total, got %d", provider.VMCount)
	}
	if provider.UsedCPUCores != 4 {
		t.Fatalf("expected only VM CPU reservation to be counted, got %d", provider.UsedCPUCores)
	}
	if provider.UsedMemory != 7168 {
		t.Fatalf("expected container and VM memory reservations to be counted, got %d", provider.UsedMemory)
	}
	if provider.UsedDisk != 51200 {
		t.Fatalf("expected only VM disk reservation to be counted, got %d", provider.UsedDisk)
	}
}

func TestReservationsAffectProviderAvailability(t *testing.T) {
	service := &ResourceService{}

	countLimitedProvider := providerModel.Provider{
		ContainerEnabled:      true,
		VirtualMachineEnabled: true,
		NodeCPUCores:          8,
		NodeMemoryTotal:       8192,
		NodeDiskTotal:         102400,
		MaxContainerInstances: 2,
	}
	applyReservationAggregates(&countLimitedProvider, []providerReservationAggregate{
		{InstanceType: "container", Count: 2},
	})
	countResult := service.checkProviderResourceAvailability(&countLimitedProvider, resourceModel.ResourceCheckRequest{
		InstanceType: "container",
		CPU:          1,
		Memory:       512,
		Disk:         5120,
	})
	if countResult.Allowed {
		t.Fatalf("expected active reservations to block container count limit")
	}

	resourceLimitedProvider := providerModel.Provider{
		ContainerEnabled:      true,
		VirtualMachineEnabled: true,
		NodeCPUCores:          4,
		NodeMemoryTotal:       4096,
		NodeDiskTotal:         20480,
		ContainerLimitCPU:     true,
		ContainerLimitMemory:  true,
		ContainerLimitDisk:    true,
	}
	applyReservationAggregates(&resourceLimitedProvider, []providerReservationAggregate{
		{InstanceType: "container", Count: 1, CPU: 4, Memory: 1024, Disk: 1024},
	})
	resourceResult := service.checkProviderResourceAvailability(&resourceLimitedProvider, resourceModel.ResourceCheckRequest{
		InstanceType: "container",
		CPU:          1,
		Memory:       512,
		Disk:         5120,
	})
	if resourceResult.Allowed {
		t.Fatalf("expected active reservations to block CPU availability")
	}
}

func TestExistingInstanceUsageUpdatesOnlyRecordExplicitLimits(t *testing.T) {
	provider := providerModel.Provider{
		UsedCPUCores:         4,
		UsedMemory:           1024,
		UsedDisk:             2048,
		NodeCPUCores:         4,
		NodeMemoryTotal:      1024,
		NodeDiskTotal:        2048,
		ContainerLimitCPU:    true,
		ContainerLimitMemory: true,
		ContainerLimitDisk:   true,
	}

	updates := buildExistingInstanceResourceUsageUpdates(&provider, "container", 1, 0, 512, provider.UpdatedAt)
	if updates["used_cpu_cores"] != 5 {
		t.Fatalf("expected CPU usage to be recorded without capacity blocking, got %#v", updates["used_cpu_cores"])
	}
	if _, ok := updates["used_memory"]; ok {
		t.Fatalf("expected missing memory limit to be skipped, got %#v", updates["used_memory"])
	}
	if updates["used_disk"] != int64(2560) {
		t.Fatalf("expected disk usage to be recorded, got %#v", updates["used_disk"])
	}

	provider.ContainerLimitCPU = false
	updates = buildExistingInstanceResourceUsageUpdates(&provider, "container", 1, 512, 512, provider.UpdatedAt)
	if _, ok := updates["used_cpu_cores"]; ok {
		t.Fatalf("expected CPU usage to be skipped when provider does not count container CPU")
	}
}
