//go:build embed

package router

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Compare the actual production build tree with the compiled binary. This
// catches Go embed's default omission of Vite's underscored helper chunks.
func TestEmbeddedFrontendMatchesBuildOutput(t *testing.T) {
	files := 0
	err := fs.WalkDir(os.DirFS("."), "dist", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		files++
		built, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		embedded, err := FS.ReadFile(path)
		if err != nil {
			t.Errorf("release asset omitted from binary: %s: %v", path, err)
			return nil
		}
		if !bytes.Equal(built, embedded) {
			t.Errorf("release asset differs from binary: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("frontend build output is empty")
	}
	if _, err := FS.ReadFile("dist/index.html"); err != nil {
		t.Fatalf("frontend index missing: %v", err)
	}
}

func TestEmbeddedFrontendMissingAssetsDoNotBecomeHTML(t *testing.T) {
	router := gin.New()
	if err := setupStaticRoutes(router); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/js/stale-module.js", "/assets/stale.css", "/favicon-missing.ico", "/controller-agent/missing.tar.gz"} {
		t.Run(path, func(t *testing.T) {
			reply := httptest.NewRecorder()
			router.ServeHTTP(reply, httptest.NewRequest(http.MethodGet, path, nil))
			if reply.Code != http.StatusNotFound || strings.Contains(reply.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("missing static asset returned %d %q", reply.Code, reply.Header().Get("Content-Type"))
			}
		})
	}
	for _, path := range []string{"/", "/admin/instances"} {
		reply := httptest.NewRecorder()
		router.ServeHTTP(reply, httptest.NewRequest(http.MethodGet, path, nil))
		if reply.Code != http.StatusOK || !strings.Contains(reply.Header().Get("Content-Type"), "text/html") {
			t.Errorf("SPA path %s returned %d %q", path, reply.Code, reply.Header().Get("Content-Type"))
		}
		if got := reply.Header().Get("Cache-Control"); got != "no-cache, max-age=0, must-revalidate" {
			t.Errorf("SPA path %s has unsafe entry caching: %q", path, got)
		}
	}
	reply := httptest.NewRecorder()
	router.ServeHTTP(reply, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if reply.Code != http.StatusMovedPermanently || reply.Header().Get("Cache-Control") != "no-cache, max-age=0, must-revalidate" {
		t.Fatalf("index redirect lost entry revalidation: %d %v", reply.Code, reply.Header())
	}
}
