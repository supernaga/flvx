package dashruntime

import (
	"testing"

	"go-backend/internal/store/model"
)

func TestBuildForwardRulesMapsPortForwardToDashRelayRule(t *testing.T) {
	forward := model.ForwardRecord{ID: 11, Name: "web", RemoteAddr: "10.0.0.10:80,10.0.0.11:80", Strategy: "round"}
	ports := []model.ForwardPortRecord{{NodeID: 3, Port: 8080}}
	entryNodes := map[int64]model.NodeRecord{
		3: {ID: 3, TCPListenAddr: "0.0.0.0", ServerIP: "203.0.113.10"},
	}
	chain := []model.ChainNodeRecord{{NodeID: 9, Strategy: "primary"}}
	nodes := map[int64]model.NodeRecord{
		9: {ID: 9, ServerIP: "198.51.100.20", InterfaceName: "eth0"},
	}
	secrets := map[int64]string{9: "edge-secret"}

	rules, err := BuildForwardRules(forward, ports, entryNodes, chain, nodes, secrets, nil)
	if err != nil {
		t.Fatalf("BuildForwardRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].StagePools[0].Backends[0].Server != "198.51.100.20:8080" {
		t.Fatalf("unexpected stage backend: %+v", rules[0].StagePools[0].Backends[0])
	}
	if len(rules[0].TargetPool.Backends) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(rules[0].TargetPool.Backends))
	}
}

func TestBuildTunnelRuleMapsChainNodesToStagePools(t *testing.T) {
	tunnel := model.TunnelRecord{ID: 7}
	chain := []model.ChainNodeRecord{{NodeID: 11, Strategy: "round"}, {NodeID: 12, Strategy: "primary"}}
	nodes := map[int64]model.NodeRecord{
		11: {ID: 11, ServerIP: "203.0.113.10"},
		12: {ID: 12, ServerIP: "198.51.100.20", InterfaceName: "eth1"},
	}
	secrets := map[int64]string{11: "edge-secret", 12: "core-secret"}

	rule, err := BuildTunnelRule(tunnel, 2201, "10.0.0.10:22", chain, nodes, secrets, nil)
	if err != nil {
		t.Fatalf("BuildTunnelRule: %v", err)
	}
	if len(rule.StagePools) != 2 {
		t.Fatalf("expected 2 stage pools, got %d", len(rule.StagePools))
	}
	if rule.StagePools[1].Backends[0].BindInterface == nil || *rule.StagePools[1].Backends[0].BindInterface != "eth1" {
		t.Fatalf("expected bind interface on second stage: %+v", rule.StagePools[1].Backends[0])
	}
}
