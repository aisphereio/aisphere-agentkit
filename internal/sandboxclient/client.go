package sandboxclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

func New(endpoint, token string) *Client {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	return &Client{endpoint: endpoint, token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

type EnsureRequest struct {
	RuntimeID   string                 `json:"runtimeId,omitempty"`
	SessionID   string                 `json:"sessionId"`
	RunID       string                 `json:"runId,omitempty"`
	OrgID       string                 `json:"orgId,omitempty"`
	ProjectID   string                 `json:"projectId,omitempty"`
	UserID      string                 `json:"userId,omitempty"`
	AgentID     string                 `json:"agentId"`
	SnapshotID  string                 `json:"snapshotId,omitempty"`
	Profile     string                 `json:"profile,omitempty"`
	TemplateRef string                 `json:"templateRef,omitempty"`
	WarmPoolRef string                 `json:"warmPoolRef,omitempty"`
	Reuse       bool                   `json:"reuse"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type Endpoint struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Workspace struct {
	MountPath string `json:"mountPath,omitempty"`
	PVC       string `json:"pvc,omitempty"`
}

// ToolSchema is the model-facing schema returned by the sandbox tool server.
// Runtime deliberately keeps the execution endpoint out of this structure.
type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type ToolList struct {
	SandboxID  string                   `json:"sandboxId,omitempty"`
	Endpoint   string                   `json:"endpoint,omitempty"`
	Tools      []ToolSchema             `json:"tools"`
	ModelTools []map[string]interface{} `json:"modelTools,omitempty"`
}

type ToolCallRequest struct {
	Tool          string                 `json:"tool"`
	Input         map[string]interface{} `json:"input,omitempty"`
	TraceID       string                 `json:"traceId,omitempty"`
	RunID         string                 `json:"runId,omitempty"`
	Attempt       int                    `json:"attempt,omitempty"`
	TimeoutMillis int64                  `json:"timeoutMillis,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

type ToolCallResult struct {
	OK             bool                   `json:"ok"`
	Tool           string                 `json:"tool"`
	Result         map[string]interface{} `json:"result,omitempty"`
	Error          map[string]interface{} `json:"error,omitempty"`
	DurationMillis int64                  `json:"durationMillis,omitempty"`
}

type Lease struct {
	SandboxID       string                 `json:"sandboxId"`
	Phase           string                 `json:"phase"`
	Driver          string                 `json:"driver,omitempty"`
	Profile         string                 `json:"profile,omitempty"`
	TemplateRef     string                 `json:"templateRef,omitempty"`
	ToolsEndpoint   string                 `json:"toolsEndpoint,omitempty"`
	BrowserEndpoint string                 `json:"browserEndpoint,omitempty"`
	Endpoints       []Endpoint             `json:"endpoints,omitempty"`
	LeaseToken      string                 `json:"leaseToken,omitempty"`
	ExpiresAt       string                 `json:"expiresAt,omitempty"`
	Workspace       Workspace              `json:"workspace,omitempty"`
	Raw             map[string]interface{} `json:"raw,omitempty"`
}

func (c *Client) Ensure(ctx context.Context, req EnsureRequest) (*Lease, error) {
	if c == nil || c.endpoint == "" {
		return nil, fmt.Errorf("sandbox adapter endpoint is empty")
	}
	var out Lease
	if err := c.doJSON(ctx, http.MethodPost, "/v1/sandboxes/ensure", req, &out); err != nil {
		return nil, err
	}
	normalizeLease(&out)
	return &out, nil
}

func (c *Client) Get(ctx context.Context, sandboxID string) (*Lease, error) {
	var out Lease
	if err := c.doJSON(ctx, http.MethodGet, "/v1/sandboxes/"+sandboxID, nil, &out); err != nil {
		return nil, err
	}
	normalizeLease(&out)
	return &out, nil
}

func (c *Client) WaitReady(ctx context.Context, sandboxID string, interval time.Duration) (*Lease, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		lease, err := c.Get(ctx, sandboxID)
		if err != nil {
			return nil, err
		}
		if (strings.EqualFold(lease.Phase, "ready") || strings.EqualFold(lease.Phase, "running")) && lease.ToolsEndpoint != "" {
			return lease, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// ListTools discovers the executor capabilities exposed by one sandbox.
// These schemas are transitional until the Sandbox capability contract is
// fully separated from Hub's model-facing Tool catalog.
func (c *Client) ListTools(ctx context.Context, sandboxID string) (*ToolList, error) {
	var out ToolList
	if err := c.doJSON(ctx, http.MethodGet, "/v1/sandboxes/"+sandboxID+"/tools", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CallTool invokes one executor capability through the Sandbox adapter.
// Business authorization, approval and credential policy belong to Runtime's
// Tool Broker and must be completed before this call.
func (c *Client) CallTool(ctx context.Context, sandboxID string, req ToolCallRequest) (*ToolCallResult, error) {
	var out ToolCallResult
	if err := c.doJSON(ctx, http.MethodPost, "/v1/sandboxes/"+sandboxID+"/tools/call", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out interface{}) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sandbox adapter %s %s failed: status=%d body=%s", method, path, resp.StatusCode, string(b))
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		var wrap struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(b, &wrap) == nil && len(wrap.Data) > 0 {
			return json.Unmarshal(wrap.Data, out)
		}
		return err
	}
	return nil
}

func normalizeLease(l *Lease) {
	for _, e := range l.Endpoints {
		switch strings.ToLower(e.Name) {
		case "tools", "tool", "tool-server":
			if l.ToolsEndpoint == "" {
				l.ToolsEndpoint = e.URL
			}
		case "browser":
			if l.BrowserEndpoint == "" {
				l.BrowserEndpoint = e.URL
			}
		}
	}
}
