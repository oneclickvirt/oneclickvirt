package resources

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupControllerCreateTest(t *testing.T, networkType string, privateIP string, defaultPortCount int) (*gorm.DB, providerModel.Provider, providerModel.Instance) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:controller_create_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_status", "idx_frozen", "idx_provider_id"} {
		if err := db.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP INDEX IF EXISTS idx_provider_id").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Port{}); err != nil {
		t.Fatal(err)
	}

	previousDB, previousLog := global.APP_DB, global.APP_LOG
	previousStart, previousStop := ControllerPortForwardFunc, StopControllerPortForwardFunc
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG = previousDB, previousLog
		ControllerPortForwardFunc, StopControllerPortForwardFunc = previousStart, previousStop
	})

	provider := providerModel.Provider{
		Name:              fmt.Sprintf("controller-create-%d", time.Now().UnixNano()),
		Type:              "docker",
		ConnectionType:    "agent",
		NetworkType:       networkType,
		DefaultPortCount:  defaultPortCount,
		PortRangeStart:    20000,
		PortRangeEnd:      21000,
		NextAvailablePort: 20000,
		FixedPorts:        []int{22},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{
		Name:        fmt.Sprintf("controller-instance-%d", time.Now().UnixNano()),
		Provider:    provider.Name,
		ProviderID:  provider.ID,
		Status:      "creating",
		PrivateIP:   privateIP,
		NetworkType: networkType,
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	return db, provider, instance
}

func TestReserveDefaultControllerMappingsBeforePrivateIPExists(t *testing.T) {
	db, provider, instance := setupControllerCreateTest(t, "nat_ipv4", "", 2)
	service := &PortMappingService{}
	if err := service.ReserveDefaultPortMappingsForCreate(context.Background(), instance.ID, provider.ID, instance.NetworkType); err != nil {
		t.Fatal(err)
	}

	var ports []providerModel.Port
	if err := db.Where("instance_id = ?", instance.ID).Order("is_ssh DESC, id ASC").Find(&ports).Error; err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 {
		t.Fatalf("reserved ports = %d, want 2", len(ports))
	}
	for _, port := range ports {
		if port.Status != "pending" || port.MappingType != "controller" || port.Protocol != "tcp" || port.InternalHost != "" {
			t.Fatalf("unexpected reserved controller port: %#v", port)
		}
	}
	active, err := service.GetActivePortMappingsForInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("pending controller reservations leaked into runtime mappings: %#v", active)
	}
}

func TestReserveDefaultControllerMappingsUsesPublishedRange(t *testing.T) {
	t.Setenv(utils.ControllerPortRangeStartEnv, "12000")
	t.Setenv(utils.ControllerPortRangeEndEnv, "12003")
	db, provider, instance := setupControllerCreateTest(t, "nat_ipv4", "", 2)
	if err := (&PortMappingService{}).ReserveDefaultPortMappingsForCreate(context.Background(), instance.ID, provider.ID, instance.NetworkType); err != nil {
		t.Fatal(err)
	}

	var ports []providerModel.Port
	if err := db.Where("instance_id = ?", instance.ID).Order("host_port ASC").Find(&ports).Error; err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 || ports[0].HostPort != 12000 || ports[1].HostPort != 12001 {
		t.Fatalf("controller ports = %#v, want 12000-12001", ports)
	}
}

func TestReserveDefaultControllerMappingsRejectsTooSmallPublishedRange(t *testing.T) {
	t.Setenv(utils.ControllerPortRangeStartEnv, "12000")
	t.Setenv(utils.ControllerPortRangeEndEnv, "12000")
	db, provider, instance := setupControllerCreateTest(t, "nat_ipv4", "", 2)
	err := (&PortMappingService{}).ReserveDefaultPortMappingsForCreate(context.Background(), instance.ID, provider.ID, instance.NetworkType)
	if err == nil {
		t.Fatal("controller reservation succeeded outside published capacity")
	}
	var count int64
	if err := db.Model(&providerModel.Port{}).Where("instance_id = ?", instance.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed reservation persisted %d ports", count)
	}
}

func TestReserveDefaultMappingsSkipsNoPortMapping(t *testing.T) {
	db, provider, instance := setupControllerCreateTest(t, "no_port_mapping", "", 2)
	if err := (&PortMappingService{}).ReserveDefaultPortMappingsForCreate(context.Background(), instance.ID, provider.ID, instance.NetworkType); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&providerModel.Port{}).Where("instance_id = ?", instance.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("no_port_mapping reserved %d mappings, want 0", count)
	}
}

func TestActivatePendingControllerMappingsStartsListenersAndIsIdempotent(t *testing.T) {
	db, provider, instance := setupControllerCreateTest(t, "nat_ipv4", "", 2)
	service := &PortMappingService{}
	if err := service.ReserveDefaultPortMappingsForCreate(context.Background(), instance.ID, provider.ID, instance.NetworkType); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&providerModel.Instance{}).Where("id = ?", instance.ID).Update("private_ip", "172.19.0.42").Error; err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var started []uint
	ControllerPortForwardFunc = func(portID, providerID uint, listenPort int, targetHost string, targetPort int) error {
		if providerID != provider.ID || listenPort <= 0 || targetPort <= 0 || targetHost != "172.19.0.42" {
			return fmt.Errorf("unexpected activation arguments")
		}
		mu.Lock()
		started = append(started, portID)
		mu.Unlock()
		return nil
	}
	StopControllerPortForwardFunc = func(uint) {}

	if err := service.ActivatePendingControllerPortMappings(context.Background(), instance.ID, provider.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.ActivatePendingControllerPortMappings(context.Background(), instance.ID, provider.ID); err != nil {
		t.Fatalf("repeated activation failed: %v", err)
	}
	mu.Lock()
	startedCount := len(started)
	mu.Unlock()
	if startedCount != 2 {
		t.Fatalf("listener starts = %d, want 2 after idempotent retry", startedCount)
	}

	var ports []providerModel.Port
	if err := db.Where("instance_id = ?", instance.ID).Find(&ports).Error; err != nil {
		t.Fatal(err)
	}
	for _, port := range ports {
		if port.Status != "active" || port.MappingMethod != "controller" || port.InternalHost != "172.19.0.42" {
			t.Fatalf("controller mapping was not activated: %#v", port)
		}
	}
	var updated providerModel.Instance
	if err := db.First(&updated, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	var sshMapping providerModel.Port
	if err := db.Where("instance_id = ? AND is_ssh = ?", instance.ID, true).First(&sshMapping).Error; err != nil {
		t.Fatal(err)
	}
	if updated.SSHPort != sshMapping.HostPort {
		t.Fatalf("instance ssh_port = %d, want %d", updated.SSHPort, sshMapping.HostPort)
	}
}

func TestActivatePendingControllerMappingsFailsClosedWithoutPrivateIP(t *testing.T) {
	db, provider, instance := setupControllerCreateTest(t, "nat_ipv4", "", 2)
	service := &PortMappingService{}
	if err := service.ReserveDefaultPortMappingsForCreate(context.Background(), instance.ID, provider.ID, instance.NetworkType); err != nil {
		t.Fatal(err)
	}
	ControllerPortForwardFunc = func(uint, uint, int, string, int) error {
		t.Fatal("listener must not start without a target IP")
		return nil
	}
	if err := service.ActivatePendingControllerPortMappings(context.Background(), instance.ID, provider.ID); err == nil {
		t.Fatal("activation succeeded without a target IP")
	}
	var failed int64
	if err := db.Model(&providerModel.Port{}).Where("instance_id = ? AND status = ?", instance.ID, "failed").Count(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if failed != 2 {
		t.Fatalf("failed mappings = %d, want 2", failed)
	}
}

func TestActivatePendingControllerMappingsPreservesPartialFailure(t *testing.T) {
	db, provider, instance := setupControllerCreateTest(t, "nat_ipv4", "10.44.0.8", 2)
	service := &PortMappingService{}
	if err := service.ReserveDefaultPortMappingsForCreate(context.Background(), instance.ID, provider.ID, instance.NetworkType); err != nil {
		t.Fatal(err)
	}
	var ports []providerModel.Port
	if err := db.Where("instance_id = ?", instance.ID).Order("id ASC").Find(&ports).Error; err != nil {
		t.Fatal(err)
	}
	failingID := ports[len(ports)-1].ID
	ControllerPortForwardFunc = func(portID, _ uint, _ int, _ string, _ int) error {
		if portID == failingID {
			return errors.New("listen failed")
		}
		return nil
	}
	var stopped []uint
	StopControllerPortForwardFunc = func(portID uint) { stopped = append(stopped, portID) }
	if err := service.ActivatePendingControllerPortMappings(context.Background(), instance.ID, provider.ID); err == nil {
		t.Fatal("partial listener failure was not returned")
	}

	var activeCount, failedCount int64
	if err := db.Model(&providerModel.Port{}).Where("instance_id = ? AND status = ?", instance.ID, "active").Count(&activeCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&providerModel.Port{}).Where("instance_id = ? AND status = ?", instance.ID, "failed").Count(&failedCount).Error; err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 || failedCount != 1 {
		t.Fatalf("active=%d failed=%d, want 1/1", activeCount, failedCount)
	}
	if len(stopped) != 1 || stopped[0] != failingID {
		t.Fatalf("stopped listeners = %#v, want failing port %d", stopped, failingID)
	}
}
