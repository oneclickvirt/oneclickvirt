package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyDeploymentMode(t *testing.T) {
	tests := []struct {
		name                        string
		goos                        string
		inContainer, composeHint    bool
		serverExists, serviceExists bool
		serviceMatches              bool
		want                        string
	}{
		{name: "systemd", goos: "linux", serverExists: true, serviceExists: true, serviceMatches: true, want: ModeSystemd},
		{name: "source", goos: "linux", serverExists: true, want: ModeSource},
		{name: "docker", goos: "linux", inContainer: true, want: ModeDocker},
		{name: "compose", goos: "linux", inContainer: true, composeHint: true, want: ModeCompose},
		{name: "non-linux-source", goos: "darwin", serverExists: true, serviceExists: true, serviceMatches: true, want: ModeSource},
		{name: "unknown", goos: "linux", want: ModeUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyDeploymentMode(test.goos, test.inContainer, test.composeHint, test.serverExists, test.serviceExists, test.serviceMatches, "/opt/oneclickvirt")
			if got != test.want {
				t.Fatalf("mode = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeDeploymentMode(t *testing.T) {
	if got := normalizeDeploymentMode(" COMPOSE "); got != ModeCompose {
		t.Fatalf("normalized mode = %q, want %q", got, ModeCompose)
	}
	if got := normalizeDeploymentMode("unsafe-mode"); got != ModeUnknown {
		t.Fatalf("invalid mode = %q, want %q", got, ModeUnknown)
	}
}

func TestEnsureUpdateDirSkipsUnmanagedWebPath(t *testing.T) {
	root := t.TempDir()
	cfg := runtimeConfig{
		InstallRoot: root,
		ServerPath:  filepath.Join(root, "server", "oneclickvirt-server"),
		WebPath:     "/var/www/unrelated-panel",
		UpdateWeb:   false,
	}
	if _, err := ensureUpdateDir(cfg); err != nil {
		t.Fatalf("all-in-one update directory should not validate an unmanaged web path: %v", err)
	}
	cfg.UpdateWeb = true
	if _, err := ensureUpdateDir(cfg); err == nil {
		t.Fatal("standalone update directory accepted a web path outside its install root")
	}
}

func TestCreateUpdateTempDirCreatesMissingRoot(t *testing.T) {
	root := t.TempDir()
	cfg := runtimeConfig{
		InstallRoot: root,
		ServerPath:  filepath.Join(root, "server", "oneclickvirt-server"),
		UpdateWeb:   false,
	}
	updateRoot := filepath.Join(root, ".oneclickvirt-update")
	if _, err := os.Stat(updateRoot); !os.IsNotExist(err) {
		t.Fatalf("update root unexpectedly exists before staging: %v", err)
	}
	stage, err := createUpdateTempDir(cfg, "regression-")
	if err != nil {
		t.Fatalf("create update temp dir: %v", err)
	}
	defer os.RemoveAll(stage)
	if !strings.HasPrefix(stage, updateRoot+string(os.PathSeparator)) {
		t.Fatalf("stage directory escaped update root: %q", stage)
	}
	if info, err := os.Stat(updateRoot); err != nil || !info.IsDir() {
		t.Fatalf("update root was not created: info=%v err=%v", info, err)
	}
}

func TestSafeBackupIDAcceptsGeneratedTimestampIDs(t *testing.T) {
	if !safeBackupID("1787403406386311104-v0.3.0") {
		t.Fatal("generated timestamp backup ID was rejected")
	}
	for _, value := range []string{"../escape", "a/b", "a\\b", "contains\x00nul"} {
		if safeBackupID(value) {
			t.Fatalf("unsafe backup ID accepted: %q", value)
		}
	}
}

func TestRuntimeConfigFallsBackFromUnsafeRepository(t *testing.T) {
	t.Setenv("ONECLICKVIRT_UPDATE_REPO", "../../unsafe")
	cfg := loadRuntimeConfig()
	if cfg.Repo != "oneclickvirt/oneclickvirt" {
		t.Fatalf("repo = %q, want safe default", cfg.Repo)
	}
}

func TestWorkerEnvironmentPinsValidatedSettings(t *testing.T) {
	cfg := runtimeConfig{
		Flavor:          FlavorAllInOne,
		UpdateWeb:       true,
		InstallRoot:     "/opt/oneclickvirt",
		ServerPath:      "/opt/oneclickvirt/server/oneclickvirt-server",
		WebPath:         "/opt/oneclickvirt/web",
		ServiceName:     "oneclickvirt",
		ServiceFile:     "/etc/systemd/system/oneclickvirt.service",
		ProxyServices:   []string{"caddy"},
		CDNEndpoints:    []string{"https://cdn.example.invalid"},
		APIEndpoints:    []string{"https://api.example.invalid"},
		HealthPort:      9443,
		AllowUnverified: true,
		Repo:            "oneclickvirt/oneclickvirt",
	}
	values := cfg.workerEnvironment()
	joined := "\n" + strings.Join(values, "\n") + "\n"
	for _, expected := range []string{
		"ONECLICKVIRT_UPDATE_MODE=systemd",
		"ONECLICKVIRT_UPDATE_WEB=true",
		"ONECLICKVIRT_PROXY_SERVICES=caddy",
		"ONECLICKVIRT_UPDATE_HEALTH_PORT=9443",
		"ONECLICKVIRT_UPDATE_REPO=oneclickvirt/oneclickvirt",
	} {
		if !strings.Contains(joined, "\n"+expected+"\n") {
			t.Fatalf("worker environment missing %q: %q", expected, values)
		}
	}
}

func TestNoSymlinkWithinRejectsSymlinkedChild(t *testing.T) {
	root := t.TempDir()
	serverDir := filepath.Join(root, "server")
	if err := os.MkdirAll(serverDir, 0750); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(serverDir, "oneclickvirt-server")
	if err := os.WriteFile(serverPath, []byte("binary"), 0750); err != nil {
		t.Fatal(err)
	}
	if !noSymlinkWithin(root, serverPath) {
		t.Fatal("regular managed path was rejected")
	}
	outside := t.TempDir()
	linkedDir := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linkedDir); err != nil {
		t.Fatal(err)
	}
	if noSymlinkWithin(root, filepath.Join(linkedDir, "server")) {
		t.Fatal("symlinked managed path was accepted")
	}
}
