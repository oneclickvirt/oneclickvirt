package images

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
)

func TestImageDownloadMirrorSuccessAndFallbacks(t *testing.T) {
	original := "https://github.com/oneclickvirt/docker/releases/download/debian/spiritlhl_debian_amd64.tar.gz"
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previousLog }()
	for _, mode := range []string{"available", "404", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				kind := "mirror"
				if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/cdn/" {
					kind = "cdn"
				}
				mu.Lock()
				requests = append(requests, kind)
				mu.Unlock()
				if kind == "mirror" && mode == "404" {
					http.NotFound(w, r)
					return
				}
				if kind == "mirror" && mode == "timeout" {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(time.Second):
						return
					}
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write([]byte("verified-image-content"))
			}))
			defer server.Close()
			t.Setenv("ONECLICKVIRT_IMAGE_MIRROR_URL", server.URL)
			t.Setenv("ONECLICKVIRT_IMAGE_MIRROR_DISABLE", "false")
			service := &ImageDownloadService{cdnEndpoints: []string{server.URL + "/cdn/"}, httpClient: &http.Client{Timeout: 150 * time.Millisecond}}
			candidates := service.getDownloadURLs(original, "")
			if candidates[len(candidates)-1] != original {
				t.Fatal("original source lost", candidates)
			}
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if count != 0 {
				t.Fatal("candidate resolution contacted a fallback before the primary")
			}
			output := filepath.Join(t.TempDir(), "image.tar.gz")
			if err := service.downloadFromCandidates(candidates, output); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(output)
			if err != nil || string(data) != "verified-image-content" {
				t.Fatalf("download content: %q %v", data, err)
			}
			want := []string{"mirror"}
			if mode != "available" {
				want = append(want, "cdn")
			}
			mu.Lock()
			got := append([]string(nil), requests...)
			mu.Unlock()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("request order %v, want %v", got, want)
			}
		})
	}
}

func TestFailedMirrorTransferPreservesExistingFile(t *testing.T) {
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previousLog }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(output, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	service := &ImageDownloadService{}
	if err := service.downloadFromCandidates([]string{server.URL}, output); err == nil {
		t.Fatal("truncated image accepted")
	}
	data, _ := os.ReadFile(output)
	if string(data) != "previous" {
		t.Fatal("failed transfer replaced the prior image")
	}
	if _, err := os.Stat(output + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("partial file left behind")
	}
}
