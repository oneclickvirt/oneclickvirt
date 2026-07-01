package lxd

import "testing"

func TestLXDExecReadyTimeout(t *testing.T) {
	if got := lxdExecReadyTimeout("vm"); got != 1800 {
		t.Fatalf("vm timeout = %d, want 1800", got)
	}
	if got := lxdExecReadyTimeout("container"); got != 30 {
		t.Fatalf("container timeout = %d, want 30", got)
	}
}

func TestLXDStoragePoolArg(t *testing.T) {
	if got := lxdStoragePoolArg(""); got != "default" {
		t.Fatalf("empty pool storage arg = %q", got)
	}
	if got := lxdStoragePoolArg("local"); got != "local" {
		t.Fatalf("custom pool storage arg = %q", got)
	}
}
