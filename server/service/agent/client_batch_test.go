package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestBatchDeleteMonitorsUsesBoundedRequests(t *testing.T) {
	var mu sync.Mutex
	requestSizes := make([]int, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/delete/batch" {
			t.Fatalf("path = %q, want batch delete path", r.URL.Path)
		}
		var request BatchDeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requestSizes = append(requestSizes, len(request.IDs))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(BatchDeleteResponse{
			DeletedIDs: request.IDs,
			Total:      len(request.IDs),
		})
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	ids := make([]int64, 205)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	deleted, err := client.BatchDeleteMonitors(ids)
	if err != nil {
		t.Fatalf("BatchDeleteMonitors() error = %v", err)
	}
	if len(deleted) != len(ids) {
		t.Fatalf("deleted count = %d, want %d", len(deleted), len(ids))
	}
	mu.Lock()
	defer mu.Unlock()
	wantSizes := []int{100, 100, 5}
	if len(requestSizes) != len(wantSizes) {
		t.Fatalf("request count = %d, want %d", len(requestSizes), len(wantSizes))
	}
	for index, want := range wantSizes {
		if requestSizes[index] != want {
			t.Fatalf("request %d size = %d, want %d", index, requestSizes[index], want)
		}
	}
}
