package utils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseAsyncOperationResponse(t *testing.T) {
	operation, asynchronous, err := ParseAsyncOperationResponse([]byte(`{"type":"async","status":"Operation created","status_code":100,"operation":"/1.0/operations/abc"}`), http.StatusAccepted)
	if err != nil || !asynchronous || operation.Operation != "/1.0/operations/abc" {
		t.Fatalf("async response = %#v, asynchronous=%v, err=%v", operation, asynchronous, err)
	}

	_, asynchronous, err = ParseAsyncOperationResponse([]byte(`null`), http.StatusOK)
	if err != nil || asynchronous {
		t.Fatalf("synchronous null response = asynchronous %v, err=%v", asynchronous, err)
	}

	if _, _, err = ParseAsyncOperationResponse([]byte(`{"type":"async","status_code":100}`), http.StatusAccepted); err == nil {
		t.Fatal("202 response without operation was accepted")
	}
}

func TestWaitForAsyncOperationPollsUntilSuccess(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.0/operations/abc" {
			t.Fatalf("operation path = %q", r.URL.Path)
		}
		count := polls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			_, _ = w.Write([]byte(`{"type":"async","status":"Running","status_code":103}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"async","status":"Success","status_code":200}`))
	}))
	defer server.Close()

	err := WaitForAsyncOperation(context.Background(), server.Client(), "/1.0/operations/abc", server.URL+"/", AsyncOperationWaitOptions{
		Timeout:      time.Second,
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("WaitForAsyncOperation() error = %v", err)
	}
	if polls.Load() != 2 {
		t.Fatalf("poll count = %d, want 2", polls.Load())
	}
}

func TestWaitForAsyncOperationReturnsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"async","status":"Failure","status_code":400,"error":"permission denied"}`))
	}))
	defer server.Close()

	err := WaitForAsyncOperation(context.Background(), server.Client(), "/1.0/operations/fail", server.URL+"/", AsyncOperationWaitOptions{Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("failure error = %v", err)
	}
}

func TestWaitForAsyncOperationReturnsOperationErrorField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"sync","status":"Success","status_code":200,"err":"chpasswd failed","err_code":1}`))
	}))
	defer server.Close()

	err := WaitForAsyncOperation(context.Background(), server.Client(), "/1.0/operations/failed-command", server.URL+"/", AsyncOperationWaitOptions{Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "chpasswd failed") {
		t.Fatalf("operation error = %v, want chpasswd failure", err)
	}
}
