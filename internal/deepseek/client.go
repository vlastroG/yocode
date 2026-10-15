// Package deepseek is a minimal client for the DeepSeek chat completions API (OpenAI format).
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL string       // e.g. https://api.deepseek.com
	APIKey  string       // DEEPSEEK_API_KEY
	HTTP    *http.Client // nil = http.DefaultClient
	// Retries is how often 429/500/503 replies are retried; Backoff is the first wait, doubled each time.
	Retries int
	Backoff time.Duration
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the decoded arguments. The API sends them as a JSON string; Raw keeps that
// string so the assistant message goes back unchanged, and ArgsErr is set when it isn't valid JSON.
type ToolCallFunction struct {
	Name      string
	Arguments map[string]any
	Raw       string
	ArgsErr   error
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (f ToolCallFunction) MarshalJSON() ([]byte, error) {
	raw := f.Raw
	if raw == "" {
		args := f.Arguments
		if args == nil {
			args = map[string]any{}
		}
		b, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = string(b)
	}
	return json.Marshal(wireFunction{Name: f.Name, Arguments: raw})
}

func (f *ToolCallFunction) UnmarshalJSON(b []byte) error {
	var w wireFunction
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*f = ToolCallFunction{Name: w.Name, Raw: w.Arguments, Arguments: map[string]any{}}
	if strings.TrimSpace(w.Arguments) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(w.Arguments), &f.Arguments); err != nil {
		f.Arguments, f.ArgsErr = nil, fmt.Errorf("arguments are not a valid JSON object: %w", err)
	}
	return nil
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

type thinking struct {
	Type string `json:"type"` // enabled | disabled
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Tools          []Tool          `json:"tools,omitempty"`
	Thinking       thinking        `json:"thinking"`
	Temperature    *float64        `json:"temperature,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
}

// Chat runs one completion with tools. Thinking is off: with tools on, the API would require every
// earlier reasoning_content to be sent back, and temperature 0 keeps runs repeatable.
func (c *Client) Chat(ctx context.Context, model string, msgs []Message, tools []Tool) (Message, error) {
	zero := 0.0
	req := chatRequest{Model: model, Messages: msgs, Tools: tools, Thinking: thinking{"disabled"}, Temperature: &zero}
	resp, err := c.complete(ctx, req)
	if err != nil {
		return Message{}, err
	}
	return resp.Message, nil
}

// ChatJSON asks for a JSON object following schema and decodes it into out. JSON mode only guarantees
// an object, so the schema goes into the prompt; thinking is on, since no tools are involved.
func (c *Client) ChatJSON(ctx context.Context, model string, msgs []Message, schema json.RawMessage, out any) error {
	sys := Message{Role: "system", Content: "Reply with one JSON object only, no other text. It must match this JSON schema:\n" + string(schema)}
	req := chatRequest{
		Model:          model,
		Messages:       append(append([]Message(nil), msgs...), sys),
		Thinking:       thinking{"enabled"},
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	resp, err := c.complete(ctx, req)
	if err != nil {
		return err
	}
	content := strings.TrimSpace(resp.Message.Content)
	if content == "" {
		return fmt.Errorf("model returned an empty reply (finish reason %q)", resp.FinishReason)
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("model reply is not valid JSON: %w: %.200s", err, content)
	}
	return nil
}

type choice struct {
	Message      Message
	FinishReason string
}

func (c *Client) complete(ctx context.Context, req chatRequest) (choice, error) {
	var resp chatResponse
	if err := c.post(ctx, "/chat/completions", req, &resp); err != nil {
		return choice{}, err
	}
	if len(resp.Choices) == 0 {
		return choice{}, errors.New("deepseek: reply has no choices")
	}
	ch := resp.Choices[0]
	return choice{Message: ch.Message, FinishReason: ch.FinishReason}, nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	wait := c.Backoff
	if wait <= 0 {
		wait = 2 * time.Second
	}
	for attempt := 0; ; attempt++ {
		status, err := c.try(ctx, path, b, out)
		if err == nil {
			return nil
		}
		if !retryable(status) || attempt >= c.Retries {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// try sends one request and returns the HTTP status (0 for transport errors).
func (c *Client) try(ctx context.Context, path string, body []byte, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(raw))
		return resp.StatusCode, fmt.Errorf("deepseek %s: %s%s: %s", path, resp.Status, hint(resp.StatusCode), msg)
	}
	// json.Decoder skips the blank keep-alive lines the API may send before the body.
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusInternalServerError || status == http.StatusServiceUnavailable
}

func hint(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return " (check DEEPSEEK_API_KEY)"
	case http.StatusPaymentRequired:
		return " (insufficient balance)"
	}
	return ""
}
