package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFinalTrafficRequiresFreshAgentAcknowledgement(t *testing.T) {
	var fresh atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req BatchInfoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if !req.Refresh {
			t.Error("final request did not request fresh counters")
		}
		json.NewEncoder(w).Encode(BatchInfoResponse{Refreshed: fresh.Load(), Monitors: []InfoResponse{{ID: 1, UsedTrafficIn: 20, UsedTrafficOut: 30}}})
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := client.BatchGetInfoContext(ctx, []int64{1}, true); err == nil {
		t.Fatal("old agent falsely acknowledged final collection")
	}
	fresh.Store(true)
	samples, err := client.BatchGetInfoContext(context.Background(), []int64{1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if samples[1].UsedTrafficIn != 20 || samples[1].UsedTrafficOut != 30 {
		t.Fatal(samples)
	}
}

func TestFinalTrafficRequestHonorsCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := client.BatchGetInfoContext(ctx, []int64{1}, true); result <- err }()
	<-entered
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled sample accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("traffic collection ignored cancellation")
	}
}

func TestBatchGetInfoNilContextUsesBackground(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req BatchInfoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Refresh {
			t.Error("unexpected refresh request")
		}
		_ = json.NewEncoder(w).Encode(BatchInfoResponse{
			Monitors: []InfoResponse{{ID: 1, UsedTraffic: 42}},
		})
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	samples, err := client.BatchGetInfoContext(nil, []int64{1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if samples[1] == nil || samples[1].UsedTraffic != 42 {
		t.Fatalf("unexpected samples: %+v", samples)
	}
}

func TestLegacyFinalTrafficWaitsForEveryRequestedCounter(t *testing.T) {
	baseline := map[int64]int64{1: 100, 2: 200}
	if batchInfoAdvanced([]int64{1, 2}, baseline, []InfoResponse{{ID: 1, LastUpdateTime: 101}, {ID: 2, LastUpdateTime: 200}}) {
		t.Fatal("accepted one stale counter")
	}
	if !batchInfoAdvanced([]int64{1, 2}, baseline, []InfoResponse{{ID: 1, LastUpdateTime: 101}, {ID: 2, LastUpdateTime: 201}}) {
		t.Fatal("rejected fresh legacy snapshot")
	}
}
