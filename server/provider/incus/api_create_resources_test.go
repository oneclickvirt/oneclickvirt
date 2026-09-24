package incus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"oneclickvirt/global"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

type resourceCreateTransport struct {
	t       *testing.T
	payload map[string]interface{}
	calls   int
}

func (tr *resourceCreateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls++
	switch req.Method + " " + req.URL.Path {
	case "POST /1.0/instances":
		if err := json.NewDecoder(req.Body).Decode(&tr.payload); err != nil {
			tr.t.Fatal(err)
		}
	case "PUT /1.0/instances/guest/state":
		var state map[string]string
		if err := json.NewDecoder(req.Body).Decode(&state); err != nil || state["action"] != "start" {
			tr.t.Fatalf("invalid start request: %#v, %v", state, err)
		}
	default:
		tr.t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"type":"sync","status_code":200,"metadata":{}}`)),
		Request:    req,
	}, nil
}

func TestIncusAPICreateResourceContract(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "images.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&systemModel.SystemImage{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG = previousDB, previousLog
		_ = sqlDB.Close()
	})
	enabled, disabled := true, false
	for _, kind := range []string{"container", "vm"} {
		for _, tc := range []struct {
			name, memory, disk, wantMemory, wantDisk string
			swap, nesting                            *bool
		}{
			{name: "runner units", memory: "1024m", disk: "20g", wantMemory: "1024MiB", wantDisk: "20GiB"},
			{name: "enabled container options", memory: "1024m", disk: "20g", wantMemory: "1024MiB", wantDisk: "20GiB", swap: &enabled, nesting: &enabled},
			{name: "disabled container options", memory: "1024m", disk: "20g", wantMemory: "1024MiB", wantDisk: "20GiB", swap: &disabled, nesting: &disabled},
			{name: "canonical units", memory: "2GiB", disk: "20GiB", wantMemory: "2GiB", wantDisk: "20GiB"},
			{name: "omitted limits"},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				p := NewIncusProvider().(*IncusProvider)
				p.config = provider.NodeConfig{Host: "127.0.0.1", StoragePool: "fixture-pool", ExecutionRule: "api_only"}
				// Existing local image + pool; no image downloads or real SSH.
				p.sshClient = utils.NewSafeShellExecutor(&recordingIncusIPv6Executor{outputs: []string{"cached-fingerprint", "yes"}})
				transport := &resourceCreateTransport{t: t}
				p.apiClient = &http.Client{Transport: transport}
				config := provider.InstanceConfig{
					Name: "guest", Image: "fixture-image", InstanceType: kind,
					CPU: "2", Memory: tc.memory, Disk: tc.disk, MemorySwap: tc.swap, AllowNesting: tc.nesting,
				}
				if err := p.apiCreateInstanceWithProgress(context.Background(), config, nil); err != nil {
					t.Fatal(err)
				}
				if transport.calls != 2 {
					t.Fatalf("create/start requests = %d, want 2", transport.calls)
				}
				body := transport.payload
				settings := body["config"].(map[string]interface{})
				if settings["limits.cpu"] != "2" {
					t.Fatalf("lost CPU limit: %#v", settings)
				}
				if tc.wantMemory == "" {
					if _, exists := settings["limits.memory"]; exists {
						t.Fatalf("omitted memory added to payload: %#v", settings)
					}
				} else if settings["limits.memory"] != tc.wantMemory {
					t.Fatalf("memory = %v, want %s", settings["limits.memory"], tc.wantMemory)
				}
				devices := body["devices"].(map[string]interface{})
				if tc.wantDisk == "" {
					if _, exists := devices["root"]; exists {
						t.Fatalf("omitted disk added to payload: %#v", devices)
					}
				} else {
					root := devices["root"].(map[string]interface{})
					if root["size"] != tc.wantDisk || root["pool"] != "fixture-pool" {
						t.Fatalf("incorrect disk size or pool: %#v", root)
					}
				}
				if kind == "vm" {
					if body["type"] != "virtual-machine" {
						t.Fatalf("wrong VM type: %v", body["type"])
					}
					for _, key := range []string{"security.nesting", "limits.cpu.priority", "limits.memory.swap", "limits.memory.swap.priority", "limits.cpu.allowance", "limits.processes"} {
						if _, exists := settings[key]; exists {
							t.Errorf("VM payload contains container-only %s: %#v", key, settings)
						}
					}
				} else {
					nesting := "true"
					if tc.nesting != nil && !*tc.nesting {
						nesting = "false"
					}
					if body["type"] != "container" || settings["security.nesting"] != nesting || settings["limits.cpu.priority"] != "0" {
						t.Fatalf("container options were lost: %#v", body)
					}
					swap, exists := settings["limits.memory.swap"]
					if tc.swap == nil {
						if exists {
							t.Fatalf("omitted swap option added to payload: %#v", settings)
						}
					} else if !exists || swap != map[bool]string{true: "true", false: "false"}[*tc.swap] {
						t.Fatalf("incorrect container swap option: %#v", settings)
					}
				}
			})
		}
	}
}

func TestIncusVMSecurityDoesNotSetContainerLimits(t *testing.T) {
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })
	executor := &recordingIncusIPv6Executor{}
	p := NewIncusProvider().(*IncusProvider)
	p.connected = true
	p.config.ExecutionRule = "ssh_only"
	p.sshClient = utils.NewSafeShellExecutor(executor)
	swap := true
	config := provider.InstanceConfig{Name: "guest", InstanceType: "vm", CPU: "2", MemorySwap: &swap}
	if err := p.configureInstanceSecurity(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if err := p.configureInstanceLimits(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], "security.secureboot") {
		t.Fatalf("VM received container-only configuration: %#v", executor.commands)
	}
}
