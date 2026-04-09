package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go-backend/internal/dashruntime"
)

type DashRuntimeNode struct {
	ServerIP string
	Secret   string
}

type DashRuntimeClientConfig struct {
	Scheme          string
	Port            string
	Timeout         time.Duration
	BaseURLOverride string
}

type DashRule struct {
	ID string `json:"id"`
}

type DashRuleListResponse []DashRule

type dashRuntimeServiceControlRequest struct {
	Services []string `json:"services"`
}

type DashServiceCheckRequest struct {
	Type       string `json:"type"`
	Target     string `json:"target"`
	TimeoutSec int    `json:"timeoutSec"`
}

type DashServiceCheckResponse struct {
	Success      bool    `json:"success"`
	LatencyMs    float64 `json:"latencyMs"`
	StatusCode   int     `json:"statusCode"`
	ErrorMessage string  `json:"errorMessage"`
}

type DashRuntimeClient struct {
	client *http.Client
	cfg    DashRuntimeClientConfig
}

func NewDashRuntimeClient(cfg DashRuntimeClientConfig) *DashRuntimeClient {
	return &DashRuntimeClient{
		client: &http.Client{Timeout: cfg.Timeout},
		cfg:    cfg,
	}
}

func (c *DashRuntimeClient) ListRules(ctx context.Context, node DashRuntimeNode) (DashRuleListResponse, error) {
	var out DashRuleListResponse
	if err := c.doJSON(ctx, node, http.MethodGet, "/api/relay/rules", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *DashRuntimeClient) PauseServices(ctx context.Context, node DashRuntimeNode, services []string) error {
	return c.controlServices(ctx, node, "/api/relay/services/pause", services)
}

func (c *DashRuntimeClient) ResumeServices(ctx context.Context, node DashRuntimeNode, services []string) error {
	return c.controlServices(ctx, node, "/api/relay/services/resume", services)
}

func (c *DashRuntimeClient) CheckService(ctx context.Context, node DashRuntimeNode, req DashServiceCheckRequest) (DashServiceCheckResponse, error) {
	var out DashServiceCheckResponse
	if err := c.doJSON(ctx, node, http.MethodPost, "/api/monitor/check", req, &out); err != nil {
		return DashServiceCheckResponse{}, err
	}
	return out, nil
}

func (c *DashRuntimeClient) UpsertRule(ctx context.Context, node DashRuntimeNode, rule dashruntime.RelayRulePayload) error {
	return c.doJSON(ctx, node, http.MethodPut, "/api/relay/rules/"+rule.ID, rule, nil)
}

func (c *DashRuntimeClient) DeleteRule(ctx context.Context, node DashRuntimeNode, ruleID string) error {
	return c.doJSON(ctx, node, http.MethodDelete, "/api/relay/rules/"+ruleID, nil, nil)
}

func (c *DashRuntimeClient) controlServices(ctx context.Context, node DashRuntimeNode, path string, services []string) error {
	return c.doJSON(ctx, node, http.MethodPost, path, dashRuntimeServiceControlRequest{Services: services}, nil)
}

func (c *DashRuntimeClient) doJSON(ctx context.Context, node DashRuntimeNode, method, path string, in, out interface{}) error {
	baseURL := c.cfg.BaseURLOverride
	if baseURL == "" {
		baseURL = fmt.Sprintf("%s://%s:%s", c.cfg.Scheme, node.ServerIP, c.cfg.Port)
	}
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(baseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+node.Secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dash runtime error %d: %s", resp.StatusCode, string(payload))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
