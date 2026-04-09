package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
)

type dashForwardRuntimeApplier interface {
	ApplyForwards(ctx context.Context, tunnelID int64) error
}

type dashTunnelRuntimeApplier interface {
	ApplyTunnel(ctx context.Context, tunnelID int64) error
}

type dashRuleDeleter interface {
	DeleteRule(ctx context.Context, nodeID int64, ruleID string) error
}

func (h *Handler) applyForwardRuntimeForCurrentEngine(ctx context.Context, tunnelID int64, fallback func() error) error {
	if h == nil {
		return errors.New("handler not initialized")
	}
	client, err := h.currentRuntimeClient()
	if err != nil {
		return err
	}
	if applier, ok := client.(dashForwardRuntimeApplier); ok {
		return applier.ApplyForwards(ctx, tunnelID)
	}
	if fallback == nil {
		return nil
	}
	return fallback()
}

func (h *Handler) applyTunnelRuntimeForCurrentEngine(ctx context.Context, state *tunnelCreateState, fallback func() ([]int64, []int64, error)) ([]int64, []int64, error) {
	if h == nil {
		return nil, nil, errors.New("handler not initialized")
	}
	if state == nil {
		return nil, nil, errors.New("invalid tunnel runtime state")
	}
	client, err := h.currentRuntimeClient()
	if err != nil {
		return nil, nil, err
	}
	if applier, ok := client.(dashTunnelRuntimeApplier); ok {
		if err := applier.ApplyTunnel(ctx, state.TunnelID); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	if fallback == nil {
		return nil, nil, nil
	}
	return fallback()
}

func (h *Handler) reconcileForwardRuntimeForCurrentEngine(ctx context.Context, forwardID, tunnelID int64, oldPorts, newPorts []forwardPortRecord, fallback func() error) error {
	if h == nil {
		return errors.New("handler not initialized")
	}
	client, err := h.currentRuntimeClient()
	if err != nil {
		return err
	}
	applier, ok := client.(dashForwardRuntimeApplier)
	if !ok {
		if fallback == nil {
			return nil
		}
		return fallback()
	}
	if err := applier.ApplyForwards(ctx, tunnelID); err != nil {
		return err
	}
	if deleter, ok := client.(dashRuleDeleter); ok {
		for _, stale := range staleForwardDashRules(forwardID, oldPorts, newPorts) {
			if err := deleter.DeleteRule(ctx, stale.nodeID, stale.ruleID); err != nil {
				log.Printf("runtime_apply warning: failed to delete stale dash forward rule node=%d rule=%s: %v", stale.nodeID, stale.ruleID, err)
			}
		}
	}
	return nil
}

func (h *Handler) reconcileTunnelRuntimeForCurrentEngine(ctx context.Context, oldChainRows []chainNodeRecord, state *tunnelCreateState, fallback func() ([]int64, []int64, error)) ([]int64, []int64, error) {
	if h == nil {
		return nil, nil, errors.New("handler not initialized")
	}
	if state == nil {
		return nil, nil, errors.New("invalid tunnel runtime state")
	}
	client, err := h.currentRuntimeClient()
	if err != nil {
		return nil, nil, err
	}
	applier, ok := client.(dashTunnelRuntimeApplier)
	if !ok {
		if fallback == nil {
			return nil, nil, nil
		}
		return fallback()
	}
	if err := applier.ApplyTunnel(ctx, state.TunnelID); err != nil {
		return nil, nil, err
	}
	if deleter, ok := client.(dashRuleDeleter); ok {
		for _, stale := range staleTunnelDashRules(state.TunnelID, oldChainRows, state) {
			if err := deleter.DeleteRule(ctx, stale.nodeID, stale.ruleID); err != nil {
				log.Printf("runtime_apply warning: failed to delete stale dash tunnel rule node=%d rule=%s: %v", stale.nodeID, stale.ruleID, err)
			}
		}
	}
	return nil, nil, nil
}

type dashRuleTarget struct {
	nodeID int64
	ruleID string
}

func staleForwardDashRules(forwardID int64, oldPorts, newPorts []forwardPortRecord) []dashRuleTarget {
	if forwardID <= 0 || len(oldPorts) == 0 {
		return nil
	}
	active := make(map[string]struct{}, len(newPorts))
	for _, port := range newPorts {
		if port.NodeID <= 0 || port.Port <= 0 {
			continue
		}
		active[forwardRuleKey(port.NodeID, port.Port)] = struct{}{}
	}
	stale := make([]dashRuleTarget, 0)
	seen := make(map[string]struct{}, len(oldPorts))
	for _, port := range oldPorts {
		if port.NodeID <= 0 || port.Port <= 0 {
			continue
		}
		key := forwardRuleKey(port.NodeID, port.Port)
		if _, ok := active[key]; ok {
			continue
		}
		ruleID := fmt.Sprintf("forward-%d-node-%d-port-%d", forwardID, port.NodeID, port.Port)
		if _, ok := seen[ruleID]; ok {
			continue
		}
		seen[ruleID] = struct{}{}
		stale = append(stale, dashRuleTarget{nodeID: port.NodeID, ruleID: ruleID})
	}
	return stale
}

func staleTunnelDashRules(tunnelID int64, oldChainRows []chainNodeRecord, state *tunnelCreateState) []dashRuleTarget {
	if tunnelID <= 0 || state == nil || len(oldChainRows) == 0 {
		return nil
	}
	oldEntry, oldOK := firstTunnelEntry(oldChainRows)
	newEntry, newOK := firstRuntimeEntry(state)
	if !oldOK || !newOK {
		return nil
	}
	if oldEntry.NodeID == newEntry.NodeID {
		if oldEntry.Port <= 0 || newEntry.Port <= 0 || oldEntry.Port == newEntry.Port {
			return nil
		}
	}
	if oldEntry.NodeID == newEntry.NodeID && oldEntry.Port == newEntry.Port {
		return nil
	}
	return []dashRuleTarget{{nodeID: oldEntry.NodeID, ruleID: fmt.Sprintf("tunnel-%d", tunnelID)}}
}

func forwardRuleKey(nodeID int64, port int) string {
	return fmt.Sprintf("%d:%d", nodeID, port)
}

func firstTunnelEntry(rows []chainNodeRecord) (chainNodeRecord, bool) {
	for _, row := range rows {
		if row.ChainType == 1 && row.NodeID > 0 && row.Port > 0 {
			return row, true
		}
	}
	return chainNodeRecord{}, false
}

func firstRuntimeEntry(state *tunnelCreateState) (tunnelRuntimeNode, bool) {
	if state == nil {
		return tunnelRuntimeNode{}, false
	}
	for _, node := range state.InNodes {
		if node.NodeID > 0 {
			return node, true
		}
	}
	return tunnelRuntimeNode{}, false
}
