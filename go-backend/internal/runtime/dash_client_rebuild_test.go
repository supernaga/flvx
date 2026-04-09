package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"go-backend/internal/dashruntime"
	httpclient "go-backend/internal/http/client"
	"go-backend/internal/store/repo"
)

func TestDashRuntimeClientRebuildAllRuntimeUpsertsForwardRules(t *testing.T) {
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	const now int64 = 123
	if err := r.CreateNode("entry", "entry-secret", "198.51.100.10", nil, nil, "8080-8085", nil, nil, nil, nil, nil, 1, 1, 1, now, 1, "0.0.0.0", "0.0.0.0", 1, 0, nil, nil, nil, nil); err != nil {
		t.Fatalf("create entry node: %v", err)
	}

	var entryNodeID int64
	if err := r.DB().Raw("SELECT id FROM node WHERE name = ?", "entry").Scan(&entryNodeID).Error; err != nil {
		t.Fatalf("query entry node id: %v", err)
	}

	var tunnelID int64
	tx := r.DB().Begin()
	if tx.Error != nil {
		t.Fatalf("begin tx: %v", tx.Error)
	}
	tunnelID, err = r.CreateTunnelTx(tx, "ssh", 1, 1, 0, now, 1, nil, 1, "")
	if err != nil {
		tx.Rollback()
		t.Fatalf("create tunnel: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit tunnel: %v", err)
	}

	forwardID, err := r.CreateForwardTx(7, "alice", "web", tunnelID, "10.0.0.10:80", "round", now, 1, []int64{entryNodeID}, 8080, "", nil)
	if err != nil {
		t.Fatalf("create forward: %v", err)
	}

	fake := &fakeDashRuntimeAPI{}
	client := NewDashRuntimeClient(r, fake)

	progress, err := client.RebuildAllRuntime(context.Background())
	if err != nil {
		t.Fatalf("RebuildAllRuntime: %v", err)
	}
	if progress.Engine != EngineDash || progress.State != ProgressStateSucceeded || !progress.Complete {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	if len(fake.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fake.upserts))
	}
	call := fake.upserts[0]
	if call.node.ServerIP != "198.51.100.10" || call.node.Secret != "entry-secret" {
		t.Fatalf("unexpected target node: %+v", call.node)
	}
	if call.rule.ID != "forward-"+int64String(forwardID)+"-node-"+int64String(entryNodeID)+"-port-8080" {
		t.Fatalf("unexpected rule id: %s", call.rule.ID)
	}
	if call.rule.Listen != "0.0.0.0:8080" {
		t.Fatalf("unexpected listen address: %s", call.rule.Listen)
	}
	if len(call.rule.TargetPool.Backends) != 1 || call.rule.TargetPool.Backends[0].Address != "10.0.0.10:80" {
		t.Fatalf("unexpected target backends: %+v", call.rule.TargetPool.Backends)
	}
}

func TestDashRuntimeClientRebuildAllRuntimeUsesTunnelEntryPortForListen(t *testing.T) {
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	entryNodeID := createNodeForDashRebuildTest(t, r, "entry-tunnel", "entry-secret", "198.51.100.20")
	chainNodeID := createNodeForDashRebuildTest(t, r, "chain-tunnel", "chain-secret", "198.51.100.21")
	outNodeID := createNodeForDashRebuildTest(t, r, "out-tunnel", "out-secret", "198.51.100.22")

	tunnelID := createTunnelForDashRebuildTest(t, r, "tcp-tunnel", 2)
	addChainHopForDashRebuildTest(t, r, tunnelID, "1", entryNodeID, 2201, 0)
	addChainHopForDashRebuildTest(t, r, tunnelID, "2", chainNodeID, 3301, 1)
	addChainHopForDashRebuildTest(t, r, tunnelID, "3", outNodeID, 4401, 0)

	fake := &fakeDashRuntimeAPI{}
	client := NewDashRuntimeClient(r, fake)

	progress, err := client.RebuildAllRuntime(context.Background())
	if err != nil {
		t.Fatalf("RebuildAllRuntime: %v", err)
	}
	if progress.State != ProgressStateSucceeded {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	if len(fake.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fake.upserts))
	}
	call := fake.upserts[0]
	if call.rule.ID != "tunnel-"+int64String(tunnelID) {
		t.Fatalf("unexpected rule id: %s", call.rule.ID)
	}
	if call.rule.Listen != "0.0.0.0:2201" {
		t.Fatalf("expected tunnel listen to use entry port, got %s", call.rule.Listen)
	}
	if len(call.rule.TargetPool.Backends) != 1 || call.rule.TargetPool.Backends[0].Address != "198.51.100.22:4401" {
		t.Fatalf("unexpected tunnel target backends: %+v", call.rule.TargetPool.Backends)
	}
}

func TestDashRuntimeClientRebuildAllRuntimeSkipsUnresolvableTunnelAndContinues(t *testing.T) {
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	validEntryNodeID := createNodeForDashRebuildTest(t, r, "valid-entry", "valid-entry-secret", "198.51.100.30")
	validOutNodeID := createNodeForDashRebuildTest(t, r, "valid-out", "valid-out-secret", "198.51.100.31")

	staleTunnelID := createTunnelForDashRebuildTest(t, r, "stale-tunnel", 2)
	addChainHopForDashRebuildTest(t, r, staleTunnelID, "1", 999001, 5501, 0)
	addChainHopForDashRebuildTest(t, r, staleTunnelID, "3", validOutNodeID, 6601, 0)

	validTunnelID := createTunnelForDashRebuildTest(t, r, "valid-tunnel", 2)
	addChainHopForDashRebuildTest(t, r, validTunnelID, "1", validEntryNodeID, 2202, 0)
	addChainHopForDashRebuildTest(t, r, validTunnelID, "3", validOutNodeID, 4402, 0)

	fake := &fakeDashRuntimeAPI{}
	client := NewDashRuntimeClient(r, fake)

	progress, err := client.RebuildAllRuntime(context.Background())
	if err != nil {
		t.Fatalf("RebuildAllRuntime: %v", err)
	}
	if progress.State != ProgressStateSucceeded || !progress.Complete {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	if len(fake.upserts) != 1 {
		t.Fatalf("expected valid tunnel to still upsert, got %d upserts", len(fake.upserts))
	}
	if fake.upserts[0].rule.ID != "tunnel-"+int64String(validTunnelID) {
		t.Fatalf("unexpected rebuilt rule: %+v", fake.upserts[0].rule)
	}
	if progress.Message == "" {
		t.Fatal("expected skip details in rebuild progress message")
	}
	if len(progress.Warnings) == 0 {
		t.Fatal("expected warnings for skipped tunnels")
	}
	if !containsAll(progress.Message, []string{"skipped", int64String(staleTunnelID)}) {
		t.Fatalf("expected skipped tunnel details in message, got %q", progress.Message)
	}
	if !containsAll(strings.Join(progress.Warnings, "; "), []string{int64String(staleTunnelID), "stale-tunnel"}) {
		t.Fatalf("expected stale tunnel details in warnings, got %#v", progress.Warnings)
	}
}

type fakeDashRuntimeAPI struct {
	upserts []fakeDashUpsert
}

type fakeDashUpsert struct {
	node httpclient.DashRuntimeNode
	rule dashruntime.RelayRulePayload
}

func (f *fakeDashRuntimeAPI) UpsertRule(_ context.Context, node httpclient.DashRuntimeNode, rule dashruntime.RelayRulePayload) error {
	f.upserts = append(f.upserts, fakeDashUpsert{node: node, rule: rule})
	return nil
}

func (f *fakeDashRuntimeAPI) DeleteRule(context.Context, httpclient.DashRuntimeNode, string) error {
	return nil
}

func (f *fakeDashRuntimeAPI) PauseServices(context.Context, httpclient.DashRuntimeNode, []string) error {
	return nil
}

func (f *fakeDashRuntimeAPI) ResumeServices(context.Context, httpclient.DashRuntimeNode, []string) error {
	return nil
}

func (f *fakeDashRuntimeAPI) CheckService(context.Context, httpclient.DashRuntimeNode, httpclient.DashServiceCheckRequest) (httpclient.DashServiceCheckResponse, error) {
	return httpclient.DashServiceCheckResponse{}, nil
}

func int64String(v int64) string {
	return fmt.Sprintf("%d", v)
}

func createNodeForDashRebuildTest(t *testing.T, r *repo.Repository, name, secret, serverIP string) int64 {
	t.Helper()
	const now int64 = 123
	if err := r.CreateNode(name, secret, serverIP, nil, nil, "2000-9000", nil, nil, nil, nil, nil, 1, 1, 1, now, 1, "0.0.0.0", "0.0.0.0", 1, 0, nil, nil, nil, nil); err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	var nodeID int64
	if err := r.DB().Raw("SELECT id FROM node WHERE name = ?", name).Scan(&nodeID).Error; err != nil {
		t.Fatalf("query node id %s: %v", name, err)
	}
	return nodeID
}

func createTunnelForDashRebuildTest(t *testing.T, r *repo.Repository, name string, tunnelType int) int64 {
	t.Helper()
	const now int64 = 123
	tx := r.DB().Begin()
	if tx.Error != nil {
		t.Fatalf("begin tx: %v", tx.Error)
	}
	tunnelID, err := r.CreateTunnelTx(tx, name, 1, tunnelType, 0, now, 1, nil, 1, "")
	if err != nil {
		tx.Rollback()
		t.Fatalf("create tunnel %s: %v", name, err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit tunnel %s: %v", name, err)
	}
	return tunnelID
}

func addChainHopForDashRebuildTest(t *testing.T, r *repo.Repository, tunnelID int64, chainType string, nodeID int64, port int, inx int) {
	t.Helper()
	tx := r.DB().Begin()
	if tx.Error != nil {
		t.Fatalf("begin chain tx: %v", tx.Error)
	}
	if err := r.CreateChainTunnelTx(tx, tunnelID, chainType, nodeID, sql.NullInt64{Int64: int64(port), Valid: port > 0}, "round", inx, "tls", ""); err != nil {
		tx.Rollback()
		t.Fatalf("create chain hop: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit chain hop: %v", err)
	}
}

func containsAll(s string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
