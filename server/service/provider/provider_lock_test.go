package provider

import (
	"testing"
	"time"

	providerModel "oneclickvirt/model/provider"
	rootProvider "oneclickvirt/provider"
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

type connectionModeProvider struct {
	rootProvider.Provider
	reverse bool
}

func (p *connectionModeProvider) IsReverseAgentConnection() bool { return p.reverse }

func TestProviderConnectionModeRejectsStaleDirectConnectionForAgentNode(t *testing.T) {
	for _, tc := range []struct {
		connectionType, rule string
		reverse, want        bool
	}{
		{connectionType: "agent", rule: "auto", want: false},
		{connectionType: "agent", rule: "ssh_only", reverse: true, want: true},
		{connectionType: " AGENT ", rule: "auto", reverse: true, want: true},
		{connectionType: "ssh", rule: "auto", reverse: true, want: false},
		{connectionType: "agent", rule: "api_only", reverse: true, want: false},
		{connectionType: "agent", rule: " API_ONLY ", want: true},
	} {
		record := providerModel.Provider{ConnectionType: tc.connectionType, ExecutionRule: tc.rule}
		if got := ProviderConnectionModeMatches(&connectionModeProvider{reverse: tc.reverse}, record); got != tc.want {
			t.Fatalf("cached reverse=%v, record=%s/%s: matched=%v, want %v", tc.reverse, tc.connectionType, tc.rule, got, tc.want)
		}
	}
}
