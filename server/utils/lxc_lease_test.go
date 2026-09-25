package utils

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLXCIPv4FromNetworkLeasesBindsOnlyMatchingNIC(t *testing.T) {
	metadata := map[string]interface{}{
		"config": map[string]interface{}{"volatile.uplink.hwaddr": "00:16:3e:11:22:33"},
		"expanded_devices": map[string]interface{}{
			"uplink": map[string]interface{}{"type": "nic", "network": "lxdbr0", "name": "enp5s0"},
			"other":  map[string]interface{}{"type": "nic", "network": "otherbr0", "name": "enp6s0", "hwaddr": "00:16:3e:44:55:66"},
		},
	}
	lookedUp := []string{}
	lookup := func(_ context.Context, network string) ([]map[string]interface{}, error) {
		lookedUp = append(lookedUp, network)
		switch network {
		case "lxdbr0":
			return []map[string]interface{}{
				{"hwaddr": "00:16:3e:99:99:99", "address": "192.0.2.99"},
				{"hwaddr": "00:16:3e:11:22:33", "address": "192.0.2.10"},
			}, nil
		case "otherbr0":
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected network %s", network)
		}
	}
	ip, state, err := LXCIPv4FromNetworkLeases(context.Background(), metadata, lookup)
	if err != nil || ip != "192.0.2.10" {
		t.Fatalf("lease IPv4 = %q, %v", ip, err)
	}
	if !reflect.DeepEqual(lookedUp, []string{"otherbr0", "lxdbr0"}) {
		t.Fatalf("network lookups = %#v", lookedUp)
	}
	if got := state["network"].(map[string]interface{})["enp5s0"].(map[string]interface{})["hwaddr"]; got != "00:16:3e:11:22:33" {
		t.Fatalf("synthetic state has wrong NIC MAC: %v", got)
	}
	metadata["_network_state"] = state
	devices := map[string]interface{}{"proxy-tcp-22000": map[string]interface{}{
		"type": "proxy", "nat": "true", "listen": "tcp:198.51.100.10:22000", "connect": "tcp:192.0.2.10:22",
	}}
	changed, err := BindLXCProxyNICs(metadata, devices)
	if err != nil || !changed {
		t.Fatalf("bind lease address = %t, %v", changed, err)
	}
	if got := devices["uplink"].(map[string]interface{})["ipv4.address"]; got != ip {
		t.Fatalf("bound address = %v, want %s", got, ip)
	}
}

func TestLXCIPv4FromNetworkLeasesRejectsUnrelatedAndAmbiguousLeases(t *testing.T) {
	metadata := map[string]interface{}{
		"config":           map[string]interface{}{"volatile.eth0.hwaddr": "00:16:3e:11:22:33"},
		"expanded_devices": map[string]interface{}{"eth0": map[string]interface{}{"type": "nic", "network": "lxdbr0", "name": "eth0"}},
	}
	for _, tc := range []struct {
		name    string
		leases  []map[string]interface{}
		wantErr bool
	}{
		{name: "unrelated MAC", leases: []map[string]interface{}{{"hwaddr": "00:16:3e:99:99:99", "address": "192.0.2.10"}}},
		{name: "loopback", leases: []map[string]interface{}{{"hwaddr": "00:16:3e:11:22:33", "address": "127.0.0.1"}}},
		{name: "ambiguous", leases: []map[string]interface{}{{"hwaddr": "00:16:3e:11:22:33", "address": "192.0.2.10"}, {"hwaddr": "00:16:3e:11:22:33", "address": "192.0.2.11"}}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip, state, err := LXCIPv4FromNetworkLeases(context.Background(), metadata, func(context.Context, string) ([]map[string]interface{}, error) { return tc.leases, nil })
			if (err != nil) != tc.wantErr || ip != "" || state != nil {
				t.Fatalf("lease result = %q, %#v, %v", ip, state, err)
			}
		})
	}
	metadata["status"] = "Stopped"
	ip, state, err := LXCIPv4FromNetworkLeases(context.Background(), metadata, func(context.Context, string) ([]map[string]interface{}, error) {
		t.Fatal("stopped instance must not query stale leases")
		return nil, nil
	})
	if err != nil || ip != "" || state != nil {
		t.Fatalf("stopped lease result = %q, %#v, %v", ip, state, err)
	}
}

func TestFetchLXCNetworkLeasesRejectsMalformedAndFailedResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"valid", http.StatusOK, `{"metadata":[{"hwaddr":"00:16:3e:11:22:33","address":"192.0.2.10"}]}`, true},
		{"bad status", http.StatusForbidden, `{"error":"denied"}`, false},
		{"missing metadata", http.StatusOK, `{}`, false},
		{"malformed lease", http.StatusOK, `{"metadata":["unexpected"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/leases") {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			leases, err := FetchLXCNetworkLeases(context.Background(), server.Client(), server.URL+"/1.0/networks/lxdbr0/leases")
			if (err == nil) != tc.ok || (tc.ok && len(leases) != 1) {
				t.Fatalf("leases = %#v, err = %v", leases, err)
			}
		})
	}
}
