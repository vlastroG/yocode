// Package ollama is a minimal client for the Ollama HTTP API.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	BaseURL string       // e.g. http://ollama:11434
	NumCtx  int          // context window passed to chat requests; 0 = Ollama default
	HTTP    *http.Client // nil = http.DefaultClient (no timeout: pulls are slow)
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON schema
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []Tool         `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

func (c *Client) Chat(ctx context.Context, model string, msgs []Message, tools []Tool) (Message, error) {
	req := chatRequest{Model: model, Messages: msgs, Tools: tools}
	if c.NumCtx > 0 {
		req.Options = map[string]any{"num_ctx": c.NumCtx}
	}
	var resp struct {
		Message Message `json:"message"`
	}
	err := c.do(ctx, http.MethodPost, "/api/chat", req, &resp)
	return resp.Message, err
}

func (c *Client) HasModel(ctx context.Context, name string) (bool, error) {
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/tags", nil, &resp); err != nil {
		return false, err
	}
	for _, m := range resp.Models {
		if m.Name == name || m.Name == name+":latest" {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) Pull(ctx context.Context, name string) error {
	var resp struct {
		Error string `json:"error"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/pull", map[string]any{"model": name, "stream": false}, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

// EnsureModels pulls every model that is not available locally yet.
func (c *Client) EnsureModels(ctx context.Context, log io.Writer, names ...string) error {
	for _, name := range names {
		ok, err := c.HasModel(ctx, name)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		fmt.Fprintf(log, "pulling model %s (first run only, this can take a while)...\n", name)
		if err := c.Pull(ctx, name); err != nil {
			return fmt.Errorf("pull %s: %w", name, err)
		}
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("ollama %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
