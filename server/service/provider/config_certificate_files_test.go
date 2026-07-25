package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeCredentialFileCopiesConfiguredSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.key")
	canonical := filepath.Join(dir, "storage", "provider.key")
	if err := os.MkdirAll(filepath.Dir(canonical), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("source-key"), 0644); err != nil {
		t.Fatal(err)
	}

	resolved, err := materializeCredentialFile(canonical, source, "", 0600)
	if err != nil {
		t.Fatalf("materializeCredentialFile() error = %v", err)
	}
	if resolved != canonical {
		t.Fatalf("resolved path = %q, want %q", resolved, canonical)
	}
	content, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "source-key" {
		t.Fatalf("canonical content = %q", content)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("canonical mode = %o, want 600", info.Mode().Perm())
	}
}

func TestMaterializeCredentialFileRestoresContentAndUsesCanonicalFallback(t *testing.T) {
	dir := t.TempDir()
	canonical := filepath.Join(dir, "provider.crt")

	if _, err := materializeCredentialFile(canonical, "missing.crt", "certificate-data", 0644); err != nil {
		t.Fatalf("restore from content failed: %v", err)
	}
	if err := os.Chmod(canonical, 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := materializeCredentialFile(canonical, "missing.crt", "", 0644)
	if err != nil {
		t.Fatalf("canonical fallback failed: %v", err)
	}
	if resolved != canonical {
		t.Fatalf("resolved path = %q, want %q", resolved, canonical)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("canonical mode = %o, want 644", info.Mode().Perm())
	}
}

func TestMaterializeCredentialFileRejectsMissingSources(t *testing.T) {
	dir := t.TempDir()
	if _, err := materializeCredentialFile(filepath.Join(dir, "canonical.crt"), filepath.Join(dir, "source.crt"), "", 0644); err == nil {
		t.Fatal("materializeCredentialFile() accepted two missing files")
	}
}
