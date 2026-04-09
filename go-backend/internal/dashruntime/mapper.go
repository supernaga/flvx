package dashruntime

import (
	"fmt"
	"strings"

	"go-backend/internal/store/model"
)

func BuildForwardRules(
	forward model.ForwardRecord,
	ports []model.ForwardPortRecord,
	entryNodes map[int64]model.NodeRecord,
	chain []model.ChainNodeRecord,
	nodes map[int64]model.NodeRecord,
	nodeSecrets map[int64]string,
	traffic *TrafficPayload,
) ([]RelayRulePayload, error) {
	stagePools, err := BuildStagePools(chain, nodes, nodeSecrets)
	if err != nil {
		return nil, err
	}
	targets := splitTargets(forward.RemoteAddr)
	rules := make([]RelayRulePayload, 0, len(ports))
	for _, port := range ports {
		entryNode, ok := entryNodes[port.NodeID]
		if !ok {
			return nil, fmt.Errorf("entry node %d not found", port.NodeID)
		}
		listenHost := strings.Trim(entryNode.TCPListenAddr, "[]")
		if listenHost == "" {
			listenHost = "0.0.0.0"
		}
		description := forward.Name
		rules = append(rules, RelayRulePayload{
			ID:          fmt.Sprintf("forward-%d-node-%d-port-%d", forward.ID, port.NodeID, port.Port),
			Protocol:    "tcp",
			Listen:      fmt.Sprintf("%s:%d", listenHost, port.Port),
			Enabled:     true,
			Description: &description,
			StagePools:  stagePools,
			TargetPool: TargetPoolPayload{
				Policy:   normalizeStrategy(forward.Strategy),
				Backends: buildTargetBackends(targets),
			},
			Traffic: traffic,
		})
	}
	return rules, nil
}

func BuildTunnelRule(
	tunnel model.TunnelRecord,
	listenPort int,
	target string,
	chain []model.ChainNodeRecord,
	nodes map[int64]model.NodeRecord,
	nodeSecrets map[int64]string,
	traffic *TrafficPayload,
) (RelayRulePayload, error) {
	stagePools, err := BuildStagePools(chain, nodes, nodeSecrets)
	if err != nil {
		return RelayRulePayload{}, err
	}
	description := fmt.Sprintf("tunnel-%d", tunnel.ID)
	return RelayRulePayload{
		ID:          fmt.Sprintf("tunnel-%d", tunnel.ID),
		Protocol:    "tcp",
		Listen:      fmt.Sprintf("0.0.0.0:%d", listenPort),
		Enabled:     true,
		Description: &description,
		StagePools:  stagePools,
		TargetPool: TargetPoolPayload{
			Policy:   "round_robin",
			Backends: buildTargetBackends([]string{target}),
		},
		Traffic: traffic,
	}, nil
}

func BuildStagePools(chain []model.ChainNodeRecord, nodes map[int64]model.NodeRecord, nodeSecrets map[int64]string) ([]StagePoolPayload, error) {
	stagePools := make([]StagePoolPayload, 0, len(chain))
	for _, hop := range chain {
		node, ok := nodes[hop.NodeID]
		if !ok {
			return nil, fmt.Errorf("chain node %d not found", hop.NodeID)
		}
		secret, ok := nodeSecrets[hop.NodeID]
		if !ok || strings.TrimSpace(secret) == "" {
			return nil, fmt.Errorf("secret for node %d not found", hop.NodeID)
		}
		var bindInterface *string
		if strings.TrimSpace(node.InterfaceName) != "" {
			value := node.InterfaceName
			bindInterface = &value
		}
		stagePools = append(stagePools, StagePoolPayload{
			Policy: normalizeStrategy(hop.Strategy),
			Backends: []StageBackendPayload{{
				ID:            fmt.Sprintf("node-%d", hop.NodeID),
				Server:        fmt.Sprintf("%s:8080", node.ServerIP),
				Token:         secret,
				Enabled:       true,
				Weight:        1,
				BindInterface: bindInterface,
			}},
		})
	}
	return stagePools, nil
}

func buildTargetBackends(targets []string) []TargetBackendPayload {
	backends := make([]TargetBackendPayload, 0, len(targets))
	for index, target := range targets {
		backends = append(backends, TargetBackendPayload{
			ID:      fmt.Sprintf("target-%d", index+1),
			Address: strings.TrimSpace(target),
			Enabled: true,
			Weight:  1,
		})
	}
	return backends
}

func splitTargets(remoteAddr string) []string {
	parts := strings.Split(remoteAddr, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return []string{strings.TrimSpace(remoteAddr)}
	}
	return result
}

func normalizeStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "primary", "primary_backup":
		return "primary_backup"
	case "round", "round_robin":
		return "round_robin"
	case "weighted_round_robin", "wrr":
		return "weighted_round_robin"
	default:
		return "round_robin"
	}
}
