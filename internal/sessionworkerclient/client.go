package sessionworkerclient

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
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), token: token, http: &http.Client{Timeout: 0}}
}

type MessageRequest struct {
	RunID    string                 `json:"runId,omitempty"`
	Message  string                 `json:"message"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type MessageResponse struct {
	RunID    string `json:"runId,omitempty"`
	Accepted bool   `json:"accepted"`
	Message  string `json:"message,omitempty"`
}

type Event struct {
	Type      string                 `json:"type"`
	RunID     string                 `json:"runId,omitempty"`
	Content   string                 `json:"content,omitempty"`
	Tool      string                 `json:"tool,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	CreatedAt int64                  `json:"createdAt,omitempty"`
}

func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/readyz", nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("worker not ready: %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) SendMessage(ctx context.Context, reqBody MessageRequest) (*MessageResponse, error) {
	var out MessageResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/session/messages", reqBody, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Events(ctx context.Context, runID string) ([]Event, error) {
	path := "/v1/events"
	if runID != "" {
		path += "?runId=" + runID
	}
	b, err := c.doRaw(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Items []Event `json:"items"`
	}
	if err := json.Unmarshal(b, &wrapped); err == nil && wrapped.Items != nil {
		return wrapped.Items, nil
	}
	var out []Event
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out interface{}) error {
	b, err := c.doRaw(ctx, method, path, in)
	if err != nil {
		return err
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (c *Client) doRaw(ctx context.Context, method, path string, in interface{}) ([]byte, error) {
	var bodyBytes []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		bodyBytes = b
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		var body io.Reader
		if bodyBytes != nil {
			body = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
		if err != nil {
			return nil, err
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
			if isTransientWorkerDialError(err) && time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Second):
					continue
				}
			}
			return nil, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("worker %s %s failed: status=%d body=%s", method, path, resp.StatusCode, string(b))
		}
		return b, nil
	}
}

func isTransientWorkerDialError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "temporary failure") ||
		strings.Contains(msg, "server misbehaving")
}
