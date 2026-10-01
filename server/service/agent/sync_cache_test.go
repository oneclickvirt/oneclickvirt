package agent

import (
	"context"
	"testing"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	"oneclickvirt/service/cache"
)

func TestInvalidateTrafficSyncCachesClearsAffectedUsersAndInstances(t *testing.T) {
	trafficCache := &cache.UserCacheService{}
	for _, userID := range []uint{5, 7, 9} {
		trafficCache.Set(cache.MakeUserTrafficOverviewKey(userID), userID, time.Minute)
		trafficCache.Set(cache.MakeUserTrafficSummaryKey(userID, 2026, 9), userID, time.Minute)
	}
	for _, instanceID := range []uint{42, 43} {
		trafficCache.Set(cache.MakeInstanceTrafficDetailKey(instanceID), instanceID, time.Minute)
	}

	invalidateTrafficSyncCaches(
		trafficCache,
		map[uint]bool{7: true},
		[]trafficUserIDBackfill{{oldUserID: 5, newUserID: 7}},
		[]trafficSyncItem{{monitor: &monitoringModel.AgentMonitor{InstanceID: 42}}},
	)

	for _, userID := range []uint{5, 7} {
		if _, ok := trafficCache.Get(cache.MakeUserTrafficOverviewKey(userID)); ok {
			t.Fatalf("user %d overview cache was not invalidated", userID)
		}
		if _, ok := trafficCache.Get(cache.MakeUserTrafficSummaryKey(userID, 2026, 9)); ok {
			t.Fatalf("user %d summary cache was not invalidated", userID)
		}
	}
	if _, ok := trafficCache.Get(cache.MakeInstanceTrafficDetailKey(42)); ok {
		t.Fatal("synced instance detail cache was not invalidated")
	}
	if _, ok := trafficCache.Get(cache.MakeUserTrafficOverviewKey(9)); !ok {
		t.Fatal("unaffected user cache was invalidated")
	}
	if _, ok := trafficCache.Get(cache.MakeInstanceTrafficDetailKey(43)); !ok {
		t.Fatal("unaffected instance cache was invalidated")
	}
}

func TestTrafficSyncLocksSerializeWaitersAndReleaseEntries(t *testing.T) {
	const providerID uint = 91001
	firstRelease, err := acquireTrafficSyncLock(context.Background(), providerID)
	if err != nil {
		t.Fatal(err)
	}
	secondAcquired := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		release, err := acquireTrafficSyncLock(context.Background(), providerID)
		if err != nil {
			secondDone <- err
			return
		}
		close(secondAcquired)
		release()
		secondDone <- nil
	}()

	deadline := time.Now().Add(time.Second)
	for {
		trafficSyncLocks.Lock()
		entry := trafficSyncLocks.entries[providerID]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		trafficSyncLocks.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			firstRelease()
			t.Fatal("second traffic sync did not register as a waiter")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-secondAcquired:
		t.Fatal("second traffic sync entered while the first held the lock")
	default:
	}

	firstRelease()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second traffic sync remained blocked after release")
	}

	trafficSyncLocks.Lock()
	_, retained := trafficSyncLocks.entries[providerID]
	trafficSyncLocks.Unlock()
	if retained {
		t.Fatal("idle provider traffic lock was not reclaimed")
	}
}

func TestTrafficSyncLockCancelledWaiterReleasesReference(t *testing.T) {
	const providerID uint = 91002
	firstRelease, err := acquireTrafficSyncLock(context.Background(), providerID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := acquireTrafficSyncLock(ctx, providerID)
		waiterDone <- err
	}()

	deadline := time.Now().Add(time.Second)
	for {
		trafficSyncLocks.Lock()
		entry := trafficSyncLocks.entries[providerID]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		trafficSyncLocks.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			firstRelease()
			t.Fatal("cancelled traffic sync did not register as a waiter")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-waiterDone:
		if err != context.Canceled {
			t.Fatalf("waiter error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		firstRelease()
		t.Fatal("cancelled traffic sync remained blocked")
	}
	firstRelease()

	trafficSyncLocks.Lock()
	_, retained := trafficSyncLocks.entries[providerID]
	trafficSyncLocks.Unlock()
	if retained {
		t.Fatal("cancelled waiter left an idle provider traffic lock")
	}
}
