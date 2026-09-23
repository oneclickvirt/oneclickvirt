package incus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"oneclickvirt/provider"
)

type incusOperationCaptureTransport struct {
	requests []string
	hosts    []string
	polls    int
}

type incusListResponseTransport struct {
	status int
	body   string
}

func (tr incusListResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: tr.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(tr.body)),
		Request:    req,
	}, nil
}

func TestIncusAPIListInstancesRejectsNonSuccessAndMalformedMetadata(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "http failure", status: http.StatusBadGateway, body: `{"error":"upstream unavailable"}`},
		{name: "missing metadata", status: http.StatusOK, body: `{"type":"sync"}`},
		{name: "wrong metadata type", status: http.StatusOK, body: `{"metadata":{"guest":{}}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewIncusProvider().(*IncusProvider)
			p.config = provider.NodeConfig{Host: "127.0.0.1"}
			p.apiClient = &http.Client{Transport: incusListResponseTransport{status: tt.status, body: tt.body}}
			instances, err := p.apiListInstances(context.Background())
			if err == nil {
				t.Fatalf("apiListInstances() = %#v, nil error; malformed/non-success response must fail closed", instances)
			}
		})
	}
}

type incusIPv4MetadataTransport struct {
	t                 *testing.T
	requests          []string
	cancelOnUpdate    context.CancelFunc
	recovered         bool
	stopped           bool
	cancelAfterStop   context.CancelFunc
	cancelOnStart     context.CancelFunc
	failRefresh       bool
	conflictAfterStop bool
	conflictOnUpdate  bool
	failureInjected   bool
	expectIPv6        bool
	profileConflict   bool
}

func (tr *incusIPv4MetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.requests = append(tr.requests, req.Method+" "+req.URL.Path)
	if err := req.Context().Err(); err != nil {
		tr.failureInjected = true
		return nil, err
	}
	payload := ""
	headers := make(http.Header)
	switch req.Method + " " + req.URL.Path {
	case "GET /1.0/instances/guest/state":
		payload = "{\"metadata\":{\"network\":{\"lo\":{\"addresses\":[{\"family\":\"inet\",\"scope\":\"host\",\"address\":\"127.0.0.1\"}]},\"enp5s0\":{\"hwaddr\":\"00:16:3e:11:22:33\",\"addresses\":[{\"family\":\"inet\",\"scope\":\"global\",\"address\":\"192.0.2.10\"}]}}}}"
		if tr.expectIPv6 {
			var envelope map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
				tr.t.Fatal(err)
			}
			iface := envelope["metadata"].(map[string]interface{})["network"].(map[string]interface{})["enp5s0"].(map[string]interface{})
			iface["addresses"] = append(iface["addresses"].([]interface{}), map[string]string{"family": "inet6", "scope": "global", "address": "2001:db8::10"})
			body, err := json.Marshal(envelope)
			if err != nil {
				tr.t.Fatal(err)
			}
			payload = string(body)
		}
	case "GET /1.0/instances/guest":
		// The real InstanceGet schema contains no state.network.
		payload = "{\"metadata\":{\"architecture\":\"x86_64\",\"config\":{\"security.nesting\":\"true\",\"limits.memory\":\"512MiB\",\"volatile.uplink.hwaddr\":\"00:16:3e:11:22:33\"},\"expanded_devices\":{\"uplink\":{\"type\":\"nic\",\"network\":\"incusbr0\",\"name\":\"eth0\",\"mtu\":\"1400\",\"limits.ingress\":\"10Mbit\"}},\"devices\":{\"root\":{\"type\":\"disk\",\"path\":\"/\",\"pool\":\"local\"}},\"profiles\":[\"default\",\"custom\"],\"description\":\"keep me\",\"ephemeral\":false,\"stateful\":false}}"
		headers.Set("ETag", "\"config-v1\"")
		if tr.stopped {
			if tr.failRefresh {
				tr.failureInjected = true
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
			}
			var envelope map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
				tr.t.Fatal(err)
			}
			metadata := envelope["metadata"].(map[string]interface{})
			config := metadata["config"].(map[string]interface{})
			config["volatile.last_state.power"] = "STOPPED"
			config["user.concurrent-edit"] = "preserve"
			if tr.conflictAfterStop {
				devices := metadata["devices"].(map[string]interface{})
				if tr.profileConflict {
					devices = metadata["expanded_devices"].(map[string]interface{})
				}
				devices["proxy-tcp-22000"] = map[string]string{"type": "proxy", "nat": "true", "listen": "tcp:198.51.100.10:22000", "connect": "tcp:192.0.2.99:22"}
				tr.failureInjected = true
			}
			body, err := json.Marshal(envelope)
			if err != nil {
				tr.t.Fatal(err)
			}
			payload = string(body)
			headers.Set("ETag", "\"config-v2\"")
		}
	case "PUT /1.0/instances/guest/state":
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			tr.t.Fatal(err)
		}
		if body.Action == "stop" {
			tr.stopped = true
			if tr.cancelAfterStop != nil {
				tr.cancelAfterStop()
			}
		} else if body.Action == "start" {
			if tr.cancelOnStart != nil {
				tr.cancelOnStart()
				tr.cancelOnStart = nil
				tr.failureInjected = true
				return nil, context.Canceled
			}
			tr.stopped = false
			tr.recovered = tr.failureInjected
		} else {
			tr.t.Fatalf("unexpected instance action %q", body.Action)
		}
		payload = "{\"type\":\"sync\",\"status_code\":200,\"metadata\":{}}"
	case "PUT /1.0/instances/guest":
		if tr.cancelOnUpdate != nil {
			tr.cancelOnUpdate()
			tr.failureInjected = true
			return nil, context.Canceled
		}
		if tr.conflictOnUpdate {
			tr.failureInjected = true
			return &http.Response{StatusCode: http.StatusPreconditionFailed, Body: io.NopCloser(strings.NewReader(`{"error":"configuration changed concurrently"}`)), Request: req}, nil
		}
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			tr.t.Fatal(err)
		}
		if req.Header.Get("If-Match") != "\"config-v2\"" {
			tr.t.Fatal("device update used a stale ETag after stopping the instance")
		}
		config, _ := body["config"].(map[string]interface{})
		if config["volatile.last_state.power"] != "STOPPED" || config["user.concurrent-edit"] != "preserve" {
			tr.t.Fatalf("device update lost configuration changes made while stopping: %#v", config)
		}
		devices, _ := body["devices"].(map[string]interface{})
		proxy, _ := devices["proxy-tcp-22000"].(map[string]interface{})
		if config["security.nesting"] != "true" || config["limits.memory"] != "512MiB" ||
			body["description"] != "keep me" || body["architecture"] != "x86_64" ||
			body["ephemeral"] != false || body["stateful"] != false ||
			!reflect.DeepEqual(body["profiles"], []interface{}{"default", "custom"}) || devices["root"] == nil {
			tr.t.Fatalf("PUT discarded existing instance configuration: %#v", body)
		}
		if proxy["listen"] != "tcp:198.51.100.10:22000" || proxy["connect"] != "tcp:192.0.2.10:22" || proxy["nat"] != "true" {
			tr.t.Fatalf("incorrect guest NAT proxy: %#v", proxy)
		}
		nic, _ := devices["uplink"].(map[string]interface{})
		if nic["type"] != "nic" || nic["ipv4.address"] != "192.0.2.10" || nic["mtu"] != "1400" || nic["limits.ingress"] != "10Mbit" || nic["network"] != "incusbr0" {
			tr.t.Fatalf("NAT target must be reserved on the original profile NIC without losing its settings: %#v", nic)
		}
		if tr.expectIPv6 {
			v6, _ := devices["proxy-v6-tcp-22000"].(map[string]interface{})
			if v6["listen"] != "tcp:[2001:db8::1]:22000" || v6["connect"] != "tcp:[2001:db8::10]:22" || v6["nat"] != "true" || nic["ipv6.address"] != "2001:db8::10" {
				tr.t.Fatalf("automatic dual-stack port lost IPv6 proxy or NIC reservation: %#v", devices)
			}
		}
		if body["_etag"] != nil || body["_network_state"] != nil || body["expanded_devices"] != nil {
			tr.t.Fatal("internal ETag leaked into API payload")
		}
		payload = "{\"type\":\"sync\",\"status_code\":200,\"metadata\":{}}"
	default:
		tr.t.Fatalf("unexpected API request: %s %s", req.Method, req.URL.Path)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
}

func TestIncusAPIWaitForInstanceIPv4UsesGuestStateAddress(t *testing.T) {
	p := NewIncusProvider().(*IncusProvider)
	p.config = provider.NodeConfig{Host: "127.0.0.1"}
	transport := &incusIPv4MetadataTransport{t: t}
	p.apiClient = &http.Client{Transport: transport}
	ip, metadata, err := p.apiWaitForInstanceIPv4(context.Background(), "guest")
	if err != nil || ip != "192.0.2.10" || metadata["devices"] == nil {
		t.Fatalf("apiWaitForInstanceIPv4() = %q, %#v, %v", ip, metadata, err)
	}
	if !reflect.DeepEqual(transport.requests, []string{"GET /1.0/instances/guest/state", "GET /1.0/instances/guest"}) {
		t.Fatalf("incorrect instance API resources: %v", transport.requests)
	}
}

func (t *incusOperationCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req.Method+" "+req.URL.Path)
	t.hosts = append(t.hosts, req.URL.Host)
	response := func(status int, payload string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    req,
		}, nil
	}

	switch {
	case req.Method == http.MethodPut && req.URL.Path == "/1.0/instances/guest/state":
		return response(http.StatusAccepted, `{"type":"async","status":"Operation created","status_code":100,"operation":"/1.0/operations/start-guest"}`)
	case req.Method == http.MethodGet && req.URL.Path == "/1.0/operations/start-guest":
		t.polls++
		if t.polls == 1 {
			return response(http.StatusOK, `{"type":"sync","status":"Success","status_code":200,"metadata":{"status":"Running","status_code":103}}`)
		}
		return response(http.StatusOK, `{"type":"sync","status":"Success","status_code":200,"metadata":{"status":"Success","status_code":200}}`)
	default:
		return response(http.StatusNotFound, `{"error":"unexpected request"}`)
	}
}

func TestIncusAPIMutationWaitsForOperationAndBracketsIPv6Host(t *testing.T) {
	oldPoll := incusAPIOperationPollInterval
	incusAPIOperationPollInterval = 0
	t.Cleanup(func() { incusAPIOperationPollInterval = oldPoll })

	capture := &incusOperationCaptureTransport{}
	p := NewIncusProvider().(*IncusProvider)
	p.config = provider.NodeConfig{Host: "2001:db8::20"}
	p.apiClient = &http.Client{Transport: capture}

	if err := p.apiStartInstance(context.Background(), "guest"); err != nil {
		t.Fatalf("apiStartInstance() error = %v", err)
	}
	wantRequests := []string{
		"PUT /1.0/instances/guest/state",
		"GET /1.0/operations/start-guest",
		"GET /1.0/operations/start-guest",
	}
	if strings.Join(capture.requests, ",") != strings.Join(wantRequests, ",") {
		t.Fatalf("requests = %v, want %v", capture.requests, wantRequests)
	}
	for _, host := range capture.hosts {
		if host != "[2001:db8::20]:8443" {
			t.Fatalf("request host = %q, want bracketed IPv6 API host", host)
		}
	}
}
