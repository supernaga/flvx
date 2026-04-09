package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	backendruntime "go-backend/internal/runtime"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func TestApplyForwardRuntimeUsesDashApplierForSelectedEngine(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	if err := r.SetRuntimeEngine(repo.RuntimeEngineDash, 10); err != nil {
		t.Fatalf("set runtime engine: %v", err)
	}

	dashClient := &stubMutationRuntimeClient{}
	h := New(r, "secret")
	h.runtimeClients = map[backendruntime.Engine]backendruntime.RuntimeClient{
		backendruntime.EngineGost: &stubForwardRuntimeClient{},
		backendruntime.EngineDash: dashClient,
	}

	called := false
	err = h.applyForwardRuntimeForCurrentEngine(context.Background(), 42, func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("applyForwardRuntimeForCurrentEngine: %v", err)
	}
	if called {
		t.Fatal("expected gost fallback to be bypassed in dash mode")
	}
	if dashClient.forwardApplies != 1 {
		t.Fatalf("expected 1 dash forward apply, got %d", dashClient.forwardApplies)
	}
	if dashClient.lastTunnelID != 42 {
		t.Fatalf("expected tunnel id 42, got %d", dashClient.lastTunnelID)
	}
}

func TestApplyTunnelRuntimeUsesDashApplierForSelectedEngine(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	if err := r.SetRuntimeEngine(repo.RuntimeEngineDash, 10); err != nil {
		t.Fatalf("set runtime engine: %v", err)
	}

	dashClient := &stubMutationRuntimeClient{}
	h := New(r, "secret")
	h.runtimeClients = map[backendruntime.Engine]backendruntime.RuntimeClient{
		backendruntime.EngineGost: &stubForwardRuntimeClient{},
		backendruntime.EngineDash: dashClient,
	}

	called := false
	state := &tunnelCreateState{TunnelID: 77, Type: 2}
	chains, services, err := h.applyTunnelRuntimeForCurrentEngine(context.Background(), state, func() ([]int64, []int64, error) {
		called = true
		return []int64{1}, []int64{2}, nil
	})
	if err != nil {
		t.Fatalf("applyTunnelRuntimeForCurrentEngine: %v", err)
	}
	if called {
		t.Fatal("expected gost fallback to be bypassed in dash mode")
	}
	if len(chains) != 0 || len(services) != 0 {
		t.Fatalf("expected dash path to return no gost rollback ids, got chains=%v services=%v", chains, services)
	}
	if dashClient.tunnelApplies != 1 {
		t.Fatalf("expected 1 dash tunnel apply, got %d", dashClient.tunnelApplies)
	}
	if dashClient.lastTunnelID != 77 {
		t.Fatalf("expected tunnel id 77, got %d", dashClient.lastTunnelID)
	}
}

func TestReconcileForwardRuntimeDeletesStaleDashRulesBeforeApply(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	if err := r.SetRuntimeEngine(repo.RuntimeEngineDash, 10); err != nil {
		t.Fatalf("set runtime engine: %v", err)
	}

	dashClient := &stubMutationRuntimeClient{}
	h := New(r, "secret")
	h.runtimeClients = map[backendruntime.Engine]backendruntime.RuntimeClient{
		backendruntime.EngineGost: &stubForwardRuntimeClient{},
		backendruntime.EngineDash: dashClient,
	}

	oldPorts := []forwardPortRecord{{NodeID: 11, Port: 8080}, {NodeID: 12, Port: 8081}}
	newPorts := []forwardPortRecord{{NodeID: 12, Port: 8081}, {NodeID: 13, Port: 9090}}
	called := false
	err = h.reconcileForwardRuntimeForCurrentEngine(context.Background(), 55, 88, oldPorts, newPorts, func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("reconcileForwardRuntimeForCurrentEngine: %v", err)
	}
	if called {
		t.Fatal("expected gost fallback to be bypassed in dash mode")
	}
	if len(dashClient.deletedRules) != 1 {
		t.Fatalf("expected 1 stale forward rule delete, got %d", len(dashClient.deletedRules))
	}
	if dashClient.deletedRules[0].nodeID != 11 || dashClient.deletedRules[0].ruleID != "forward-55-node-11-port-8080" {
		t.Fatalf("unexpected deleted rule: %+v", dashClient.deletedRules[0])
	}
	if dashClient.forwardApplies != 1 || dashClient.lastTunnelID != 88 {
		t.Fatalf("expected forward apply for tunnel 88, got applies=%d lastTunnel=%d", dashClient.forwardApplies, dashClient.lastTunnelID)
	}
	if len(dashClient.operations) < 2 || dashClient.operations[0] != "apply-forward:88" || dashClient.operations[1] != "delete:11:forward-55-node-11-port-8080" {
		t.Fatalf("expected apply before stale delete, got %v", dashClient.operations)
	}
}

func TestReconcileForwardRuntimeLogsWarningWhenDashDeleteFails(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	if err := r.SetRuntimeEngine(repo.RuntimeEngineDash, 10); err != nil {
		t.Fatalf("set runtime engine: %v", err)
	}

	dashClient := &stubMutationRuntimeClient{deleteRuleErr: fmt.Errorf("delete failed")}
	h := New(r, "secret")
	h.runtimeClients = map[backendruntime.Engine]backendruntime.RuntimeClient{
		backendruntime.EngineGost: &stubForwardRuntimeClient{},
		backendruntime.EngineDash: dashClient,
	}

	oldOutput := log.Writer()
	defer log.SetOutput(oldOutput)
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)

	oldPorts := []forwardPortRecord{{NodeID: 11, Port: 8080}}
	newPorts := []forwardPortRecord{{NodeID: 13, Port: 9090}}
	err = h.reconcileForwardRuntimeForCurrentEngine(context.Background(), 55, 88, oldPorts, newPorts, nil)
	if err != nil {
		t.Fatalf("reconcileForwardRuntimeForCurrentEngine: %v", err)
	}
	if dashClient.forwardApplies != 1 {
		t.Fatalf("expected apply to succeed despite delete error, got %d applies", dashClient.forwardApplies)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "forward-55-node-11-port-8080") || !strings.Contains(logged, "delete failed") {
		t.Fatalf("expected warning log with stale rule id and cause, got %q", logged)
	}
}

func TestReconcileTunnelRuntimeDeletesStaleDashRuleBeforeApply(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	if err := r.SetRuntimeEngine(repo.RuntimeEngineDash, 10); err != nil {
		t.Fatalf("set runtime engine: %v", err)
	}

	dashClient := &stubMutationRuntimeClient{}
	h := New(r, "secret")
	h.runtimeClients = map[backendruntime.Engine]backendruntime.RuntimeClient{
		backendruntime.EngineGost: &stubForwardRuntimeClient{},
		backendruntime.EngineDash: dashClient,
	}

	oldChainRows := []chainNodeRecord{{NodeID: 21, ChainType: 1, Port: 2201}}
	state := &tunnelCreateState{TunnelID: 77, Type: 2, InNodes: []tunnelRuntimeNode{{NodeID: 21, Port: 2202, ChainType: 1}}}
	called := false
	chains, services, err := h.reconcileTunnelRuntimeForCurrentEngine(context.Background(), oldChainRows, state, func() ([]int64, []int64, error) {
		called = true
		return []int64{1}, []int64{2}, nil
	})
	if err != nil {
		t.Fatalf("reconcileTunnelRuntimeForCurrentEngine: %v", err)
	}
	if called {
		t.Fatal("expected gost fallback to be bypassed in dash mode")
	}
	if len(chains) != 0 || len(services) != 0 {
		t.Fatalf("expected dash path to return no gost rollback ids, got chains=%v services=%v", chains, services)
	}
	if len(dashClient.deletedRules) != 1 {
		t.Fatalf("expected 1 stale tunnel rule delete, got %d", len(dashClient.deletedRules))
	}
	if dashClient.deletedRules[0].nodeID != 21 || dashClient.deletedRules[0].ruleID != "tunnel-77" {
		t.Fatalf("unexpected deleted tunnel rule: %+v", dashClient.deletedRules[0])
	}
	if dashClient.tunnelApplies != 1 || dashClient.lastTunnelID != 77 {
		t.Fatalf("expected tunnel apply for 77, got applies=%d lastTunnel=%d", dashClient.tunnelApplies, dashClient.lastTunnelID)
	}
	if len(dashClient.operations) < 2 || dashClient.operations[0] != "apply-tunnel:77" || dashClient.operations[1] != "delete:21:tunnel-77" {
		t.Fatalf("expected apply before stale delete, got %v", dashClient.operations)
	}
}

func TestForwardUpdateInDashModeDeletesStaleRuleWhenMovingToDifferentTunnel(t *testing.T) {
	r := openRuntimeApplyTestRepo(t)
	defer r.Close()

	if err := r.SetRuntimeEngine(repo.RuntimeEngineDash, 10); err != nil {
		t.Fatalf("set runtime engine: %v", err)
	}

	seedRuntimeApplyTestNode(t, r, model.Node{ID: 1, Name: "entry-a", Secret: "secret-a", ServerIP: "10.0.0.1", Port: "17000-17010", CreatedTime: 1, Status: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0"})
	seedRuntimeApplyTestNode(t, r, model.Node{ID: 2, Name: "entry-b", Secret: "secret-b", ServerIP: "10.0.0.2", Port: "18000-18010", CreatedTime: 1, Status: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0"})
	seedRuntimeApplyTestTunnel(t, r, model.Tunnel{ID: 1, Name: "tunnel-a", Type: 1, Protocol: "tls", Flow: 1, TrafficRatio: 1, CreatedTime: 1, UpdatedTime: 1, Status: 1})
	seedRuntimeApplyTestTunnel(t, r, model.Tunnel{ID: 2, Name: "tunnel-b", Type: 1, Protocol: "tls", Flow: 1, TrafficRatio: 1, CreatedTime: 1, UpdatedTime: 1, Status: 1})
	seedRuntimeApplyTestChainTunnel(t, r, model.ChainTunnel{TunnelID: 1, ChainType: "1", NodeID: 1, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}})
	seedRuntimeApplyTestChainTunnel(t, r, model.ChainTunnel{TunnelID: 2, ChainType: "1", NodeID: 2, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}})
	seedRuntimeApplyTestForward(t, r, model.Forward{ID: 9, UserID: 1, UserName: "admin", Name: "fwd", TunnelID: 1, RemoteAddr: "203.0.113.10:80", Strategy: "fifo", CreatedTime: 1, UpdatedTime: 1, Status: 1, Inx: 1})
	seedRuntimeApplyTestForwardPort(t, r, model.ForwardPort{ForwardID: 9, NodeID: 1, Port: 17001})

	dashClient := &stubMutationRuntimeClient{}
	h := New(r, "secret")
	h.runtimeClients = map[backendruntime.Engine]backendruntime.RuntimeClient{
		backendruntime.EngineGost: &stubForwardRuntimeClient{},
		backendruntime.EngineDash: dashClient,
	}

	req := runtimeApplyAdminJSONRequest(t, http.MethodPost, "/api/v1/forward/update", map[string]interface{}{
		"id":         int64(9),
		"tunnelId":   int64(2),
		"name":       "fwd",
		"remoteAddr": "203.0.113.10:80",
		"strategy":   "fifo",
		"inPort":     18001,
	})
	res := httptest.NewRecorder()

	h.forwardUpdate(res, req)

	assertRuntimeApplySuccessResponse(t, res)
	if len(dashClient.deletedRules) != 1 {
		t.Fatalf("expected 1 stale dash delete, got %d", len(dashClient.deletedRules))
	}
	if dashClient.deletedRules[0].nodeID != 1 || dashClient.deletedRules[0].ruleID != "forward-9-node-1-port-17001" {
		t.Fatalf("unexpected stale dash delete: %+v", dashClient.deletedRules[0])
	}
	if len(dashClient.operations) < 2 || dashClient.operations[0] != "apply-forward:2" || dashClient.operations[1] != "delete:1:forward-9-node-1-port-17001" {
		t.Fatalf("expected apply before stale delete, got %v", dashClient.operations)
	}
	ports, err := r.ListForwardPorts(9)
	if err != nil {
		t.Fatalf("list forward ports: %v", err)
	}
	if len(ports) != 1 || ports[0].NodeID != 2 || ports[0].Port != 18001 {
		t.Fatalf("unexpected forward ports after update: %+v", ports)
	}
}

func TestTunnelUpdateInDashModeDeletesStaleRuleWhenEntryNodeChanges(t *testing.T) {
	r := openRuntimeApplyTestRepo(t)
	defer r.Close()

	if err := r.SetRuntimeEngine(repo.RuntimeEngineDash, 10); err != nil {
		t.Fatalf("set runtime engine: %v", err)
	}

	seedRuntimeApplyTestNode(t, r, model.Node{ID: 1, Name: "entry-a", Secret: "secret-a", ServerIP: "10.0.0.1", Port: "2200-2210", CreatedTime: 1, Status: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0"})
	seedRuntimeApplyTestNode(t, r, model.Node{ID: 2, Name: "entry-b", Secret: "secret-b", ServerIP: "10.0.0.2", Port: "2300-2310", CreatedTime: 1, Status: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0"})
	seedRuntimeApplyTestNode(t, r, model.Node{ID: 3, Name: "exit", Secret: "secret-c", ServerIP: "10.0.0.3", Port: "3300-3310", CreatedTime: 1, Status: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0"})
	seedRuntimeApplyTestTunnel(t, r, model.Tunnel{ID: 7, Name: "dash-tunnel", Type: 2, Protocol: "tls", Flow: 1, TrafficRatio: 1, CreatedTime: 1, UpdatedTime: 1, Status: 1})
	seedRuntimeApplyTestChainTunnel(t, r, model.ChainTunnel{TunnelID: 7, ChainType: "1", NodeID: 1, Port: sql.NullInt64{Int64: 2201, Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}})
	seedRuntimeApplyTestChainTunnel(t, r, model.ChainTunnel{TunnelID: 7, ChainType: "3", NodeID: 3, Port: sql.NullInt64{Int64: 3301, Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}})

	dashClient := &stubMutationRuntimeClient{}
	h := New(r, "secret")
	h.runtimeClients = map[backendruntime.Engine]backendruntime.RuntimeClient{
		backendruntime.EngineGost: &stubForwardRuntimeClient{},
		backendruntime.EngineDash: dashClient,
	}

	req := runtimeApplyAdminJSONRequest(t, http.MethodPost, "/api/v1/tunnel/update", map[string]interface{}{
		"id":           int64(7),
		"name":         "dash-tunnel",
		"type":         2,
		"flow":         int64(1),
		"trafficRatio": 1.0,
		"status":       1,
		"inNodeId": []map[string]interface{}{{
			"nodeId":   int64(2),
			"protocol": "tls",
		}},
		"chainNodes": []interface{}{},
		"outNodeId": []map[string]interface{}{{
			"nodeId":   int64(3),
			"protocol": "tls",
			"port":     3301,
		}},
	})
	res := httptest.NewRecorder()

	h.tunnelUpdate(res, req)

	assertRuntimeApplySuccessResponse(t, res)
	if len(dashClient.deletedRules) != 1 {
		t.Fatalf("expected 1 stale dash delete, got %d", len(dashClient.deletedRules))
	}
	if dashClient.deletedRules[0].nodeID != 1 || dashClient.deletedRules[0].ruleID != "tunnel-7" {
		t.Fatalf("unexpected stale tunnel delete: %+v", dashClient.deletedRules[0])
	}
	if len(dashClient.operations) < 2 || dashClient.operations[0] != "apply-tunnel:7" || dashClient.operations[1] != "delete:1:tunnel-7" {
		t.Fatalf("expected apply before stale delete, got %v", dashClient.operations)
	}
	entryIDs, err := r.TunnelEntryNodeIDs(7)
	if err != nil {
		t.Fatalf("tunnel entry ids: %v", err)
	}
	if len(entryIDs) != 1 || entryIDs[0] != 2 {
		t.Fatalf("unexpected tunnel entry ids after update: %v", entryIDs)
	}
}

func openRuntimeApplyTestRepo(t *testing.T) *repo.Repository {
	t.Helper()
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	return r
}

func seedRuntimeApplyTestNode(t *testing.T, r *repo.Repository, node model.Node) {
	t.Helper()
	if err := r.DB().Create(&node).Error; err != nil {
		t.Fatalf("seed node %d: %v", node.ID, err)
	}
}

func seedRuntimeApplyTestTunnel(t *testing.T, r *repo.Repository, tunnel model.Tunnel) {
	t.Helper()
	if err := r.DB().Create(&tunnel).Error; err != nil {
		t.Fatalf("seed tunnel %d: %v", tunnel.ID, err)
	}
}

func seedRuntimeApplyTestChainTunnel(t *testing.T, r *repo.Repository, ct model.ChainTunnel) {
	t.Helper()
	if err := r.DB().Create(&ct).Error; err != nil {
		t.Fatalf("seed chain tunnel: %v", err)
	}
}

func seedRuntimeApplyTestForward(t *testing.T, r *repo.Repository, forward model.Forward) {
	t.Helper()
	if err := r.DB().Create(&forward).Error; err != nil {
		t.Fatalf("seed forward %d: %v", forward.ID, err)
	}
}

func seedRuntimeApplyTestForwardPort(t *testing.T, r *repo.Repository, port model.ForwardPort) {
	t.Helper()
	if err := r.DB().Create(&port).Error; err != nil {
		t.Fatalf("seed forward port: %v", err)
	}
}

func runtimeApplyAdminJSONRequest(t *testing.T, method, path string, payload interface{}) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func assertRuntimeApplySuccessResponse(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d with body %s", res.Code, res.Body.String())
	}
	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Code != 0 {
		t.Fatalf("expected success code 0, got %d with msg %q and body %s", payload.Code, payload.Msg, res.Body.String())
	}
}

type stubMutationRuntimeClient struct {
	stubForwardRuntimeClient
	forwardApplies int
	tunnelApplies  int
	lastTunnelID   int64
	deletedRules   []stubDeletedRule
	operations     []string
	deleteRuleErr  error
}

type stubDeletedRule struct {
	nodeID int64
	ruleID string
}

func (c *stubMutationRuntimeClient) ApplyForwards(_ context.Context, tunnelID int64) error {
	c.forwardApplies++
	c.lastTunnelID = tunnelID
	c.operations = append(c.operations, "apply-forward:"+int64StringForTest(tunnelID))
	return nil
}

func (c *stubMutationRuntimeClient) ApplyTunnel(_ context.Context, tunnelID int64) error {
	c.tunnelApplies++
	c.lastTunnelID = tunnelID
	c.operations = append(c.operations, "apply-tunnel:"+int64StringForTest(tunnelID))
	return nil
}

func (c *stubMutationRuntimeClient) DeleteRule(_ context.Context, nodeID int64, ruleID string) error {
	c.deletedRules = append(c.deletedRules, stubDeletedRule{nodeID: nodeID, ruleID: ruleID})
	c.operations = append(c.operations, "delete:"+int64StringForTest(nodeID)+":"+ruleID)
	return c.deleteRuleErr
}

func int64StringForTest(v int64) string {
	return strconv.FormatInt(v, 10)
}
