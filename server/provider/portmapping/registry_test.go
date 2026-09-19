package portmapping

import (
	"fmt"
	"sync"
	"testing"
)

func TestGlobalRegistryConcurrentAccess(t *testing.T) {
	factory := func(*ManagerConfig) PortMappingProvider { return nil }
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("registry-test-%d", i)
			RegisterProvider(name, factory)
			_, _ = GetProvider(name)
			_, _ = GetProviderWithConfig(name, nil)
			_ = ListProviders()
			_ = GetRegisteredProviders()
		}(i)
	}
	wg.Wait()
}

func TestManagerConcurrentRegistryAccess(t *testing.T) {
	m := NewManager(nil)
	factory := func(*ManagerConfig) PortMappingProvider { return nil }
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("manager-test-%d", i)
			m.RegisterProvider(name, factory)
			_, _ = m.GetProvider(name)
			_ = m.GetSupportedProviders()
		}(i)
	}
	wg.Wait()
}

func TestManagerRejectsNilRequests(t *testing.T) {
	m := NewManager(nil)
	if _, err := m.CreatePortMapping(nil, "missing", nil); err == nil {
		t.Fatal("expected nil create request to be rejected")
	}
	if err := m.DeletePortMapping(nil, "missing", nil); err == nil {
		t.Fatal("expected nil delete request to be rejected")
	}
	if _, err := m.UpdatePortMapping(nil, "missing", nil); err == nil {
		t.Fatal("expected nil update request to be rejected")
	}
}
