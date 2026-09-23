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

func TestWaitForAsyncOperationUnwrapsLookupEnvelope(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"type":"sync","status":"Success","status_code":200,"metadata":{"id":"abc","status":"Running","status_code":103,"err":"","metadata":null}}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"sync","status":"Success","status_code":200,"metadata":{"id":"abc","status":"Success","status_code":200,"err":"","metadata":null}}`))
	}))
	defer server.Close()
	if err := WaitForAsyncOperation(context.Background(), server.Client(), server.URL, "", AsyncOperationWaitOptions{Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 2 {
		t.Fatalf("lookup success was mistaken for operation completion: polls=%d", polls.Load())
	}
}

func TestWaitForAsyncOperationRejectsNestedFailureAndInvalidEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"failed create", `{"type":"sync","status_code":200,"metadata":{"status":"Failure","status_code":400,"err":"image import failed"}}`, "image import failed"},
		{"cancelled", `{"type":"sync","status_code":200,"metadata":{"status":"Cancelled","status_code":401}}`, "Cancelled"},
		{"exec failure", `{"type":"sync","status_code":200,"metadata":{"status":"Success","status_code":200,"metadata":{"return":1}}}`, "exit status 1"},
		{"outer failure", `{"type":"error","error_code":403,"error":"forbidden","metadata":{"status_code":200}}`, "forbidden"},
		{"missing metadata", `{"type":"sync","status_code":200}`, "metadata"},
		{"null metadata", `{"type":"sync","status_code":200,"metadata":null}`, "缺少状态"},
		{"empty metadata", `{"type":"sync","status_code":200,"metadata":{}}`, "缺少状态"},
		{"invalid metadata", `{"type":"sync","status_code":200,"metadata":[]}`, "metadata"},
		{"empty response", `{}`, "缺少状态"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			err := WaitForAsyncOperation(context.Background(), server.Client(), server.URL, "", AsyncOperationWaitOptions{Timeout: time.Second})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestWaitForAsyncOperationHonorsDeadlineWhileNestedOperationRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"sync","status_code":200,"metadata":{"status":"Running","status_code":103}}`))
	}))
	defer server.Close()
	err := WaitForAsyncOperation(context.Background(), server.Client(), server.URL, "", AsyncOperationWaitOptions{
		Timeout: 20 * time.Millisecond, PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error=%v, want deadline exceeded", err)
	}
}
