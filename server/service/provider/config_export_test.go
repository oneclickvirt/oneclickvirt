package provider

import (
	"path/filepath"
	"strings"
	"testing"

	providerModel "oneclickvirt/model/provider"
)

func TestProviderExportPathStaysInsideExportDirectory(t *testing.T) {
	exportDir := t.TempDir()
	provider := providerModel.Provider{ID: 42, Name: "../../node/配置", Type: "../docker"}
	path := providerExportPath(exportDir, provider)

	relative, err := filepath.Rel(exportDir, path)
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("export path escaped directory: %q", path)
	}
	if filepath.Dir(path) != exportDir {
		t.Fatalf("export path has nested directory: %q", path)
	}
}
