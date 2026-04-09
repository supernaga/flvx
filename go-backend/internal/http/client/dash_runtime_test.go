package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/dashruntime"
)

func TestDashRuntimeClientAddsBearerAuth(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := NewDashRuntimeClient(DashRuntimeClientConfig{
		BaseURLOverride: server.URL,
		Timeout:         time.Second,
	})

	_, err := client.ListRules(context.Background(), DashRuntimeNode{
		ServerIP: server.URL,
		Secret:   "node-secret",
	})
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if authHeader != "Bearer node-secret" {
		t.Fatalf("expected bearer header, got %q", authHeader)
	}
}

func TestDashRuntimeClientPauseServices(t *testing.T) {
	var (
		method string
		path   string
		body   dashRuntimeServiceControlRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewDashRuntimeClient(DashRuntimeClientConfig{
		BaseURLOverride: server.URL,
		Timeout:         time.Second,
	})

	err := client.PauseServices(context.Background(), DashRuntimeNode{ServerIP: server.URL, Secret: "node-secret"}, []string{"svc-a_tcp"})
	if err != nil {
		t.Fatalf("PauseServices: %v", err)
	}
	if method != http.MethodPost {
		t.Fatalf("expected POST, got %s", method)
	}
	if path != "/api/relay/services/pause" {
		t.Fatalf("expected pause path, got %s", path)
	}
	if len(body.Services) != 1 || body.Services[0] != "svc-a_tcp" {
		t.Fatalf("unexpected pause body: %+v", body)
	}
}

func TestDashRuntimeClientCheckService(t *testing.T) {
	var reqBody DashServiceCheckRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/monitor/check" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"latencyMs":12,"statusCode":200}`))
	}))
	defer server.Close()

	client := NewDashRuntimeClient(DashRuntimeClientConfig{
		BaseURLOverride: server.URL,
		Timeout:         time.Second,
	})

	resp, err := client.CheckService(context.Background(), DashRuntimeNode{ServerIP: server.URL, Secret: "node-secret"}, DashServiceCheckRequest{
		Type:       "tcp",
		Target:     "example.com:443",
		TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("CheckService: %v", err)
	}
	if reqBody.Type != "tcp" || reqBody.Target != "example.com:443" || reqBody.TimeoutSec != 5 {
		t.Fatalf("unexpected request body: %+v", reqBody)
	}
	if !resp.Success || resp.LatencyMs != 12 || resp.StatusCode != 200 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestDashRuntimeClientUpsertRule(t *testing.T) {
	var (
		method string
		path   string
		body   dashruntime.RelayRulePayload
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewDashRuntimeClient(DashRuntimeClientConfig{
		BaseURLOverride: server.URL,
		Timeout:         time.Second,
	})

	rule := dashruntime.RelayRulePayload{
		ID:       "forward-11-node-3-port-8080",
		Protocol: "tcp",
		Listen:   "0.0.0.0:8080",
		Enabled:  true,
	}
	if err := client.UpsertRule(context.Background(), DashRuntimeNode{ServerIP: server.URL, Secret: "node-secret"}, rule); err != nil {
		t.Fatalf("UpsertRule: %v", err)
	}
	if method != http.MethodPut {
		t.Fatalf("expected PUT, got %s", method)
	}
	if path != "/api/relay/rules/forward-11-node-3-port-8080" {
		t.Fatalf("unexpected path: %s", path)
	}
	if body.ID != rule.ID || body.Listen != rule.Listen || body.Protocol != rule.Protocol || !body.Enabled {
		t.Fatalf("unexpected rule payload: %+v", body)
	}
}
