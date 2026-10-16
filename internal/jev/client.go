// Package jev asks TypeSafe's Jev model for small typed judgments: routing chat input while a plan
// is pending, and judging which docs chunks are relevant to a query.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultURL   = "https://api.typesafe.ai/v1/systemone"
	DefaultModel = "jev-1.13.0" // pinned: thresholds are tuned against it
)

type Client struct {
	URL     string // default DefaultURL
	APIKey  string // TYPESAFE_API_KEY
	Model   string // default DefaultModel
	HTTP    *http.Client
	Retries int           // 429/529 retries
	Backoff time.Duration // first wait, doubled each retry; retry-after wins when present
}

// Question is one typed judgment over the request's state. Instructions and Criteria may be strings,
// objects or arrays; Type is "choice", "noul" or "score".
type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
}

// Ask sends all questions in one request; they see the same state and are answered independently.
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, error) {
	model := c.Model
	if model == "" {
		model = DefaultModel
	}
	body, err := json.Marshal(map[string]any{"state": state, "model": model, "questions": questions})
	if err != nil {
		return nil, err
	}
	wait := c.Backoff
	if wait <= 0 {
		wait = time.Second
	}
	for attempt := 0; ; attempt++ {
		answers, retryAfter, err := c.try(ctx, body)
		if err == nil {
			return answers, nil
		}
		if retryAfter < 0 || attempt >= c.Retries {
			return nil, err
		}
		if retryAfter > 0 {
			wait = retryAfter
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// try sends one request. retryAfter is -1 when the error is not worth retrying.
func (c *Client) try(ctx context.Context, body []byte) (map[string]Answer, time.Duration, error) {
	url := c.URL
	if url == "" {
		url = DefaultURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err := fmt.Errorf("jev: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529 {
			secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			return nil, time.Duration(secs) * time.Second, err
		}
		return nil, -1, err
	}
	var out struct {
		Answers map[string]Answer `json:"answers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, -1, fmt.Errorf("jev: decode reply: %w", err)
	}
	return out.Answers, -1, nil
}
