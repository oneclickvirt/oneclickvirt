package provider

import (
	"testing"
	"time"
)

func TestProviderOperationLocksSerializeOnlySameProvider(t *testing.T) {
	service := &ProviderService{}
	providerOne := service.providerOperationMutex(1)
	providerOne.Lock()

	otherProviderAcquired := make(chan struct{})
	go func() {
		lock := service.providerOperationMutex(2)
		lock.Lock()
		close(otherProviderAcquired)
		lock.Unlock()
	}()
	select {
	case <-otherProviderAcquired:
	case <-time.After(time.Second):
		t.Fatal("an operation on provider 1 blocked unrelated provider 2")
	}

	sameProviderAcquired := make(chan struct{})
	go func() {
		lock := service.providerOperationMutex(1)
		lock.Lock()
		close(sameProviderAcquired)
		lock.Unlock()
	}()
	select {
	case <-sameProviderAcquired:
		t.Fatal("concurrent operations on the same provider were not serialized")
	case <-time.After(50 * time.Millisecond):
	}

	providerOne.Unlock()
	select {
	case <-sameProviderAcquired:
	case <-time.After(time.Second):
		t.Fatal("same-provider operation did not resume after lock release")
	}
}
