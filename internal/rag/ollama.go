package rag

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

// EnsureModel pulls the embedding model into the Ollama at baseURL unless it is there already.
func EnsureModel(ctx context.Context, baseURL, name string, log io.Writer) error {
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := ollamaCall(ctx, http.MethodGet, baseURL+"/api/tags", nil, &tags); err != nil {
		return err
	}
	for _, m := range tags.Models {
		if m.Name == name || m.Name == name+":latest" {
			return nil
		}
	}
	fmt.Fprintf(log, "pulling embedding model %s (first run only)...\n", name)
	var pull struct {
		Error string `json:"error"`
	}
	if err := ollamaCall(ctx, http.MethodPost, baseURL+"/api/pull", map[string]any{"model": name, "stream": false}, &pull); err != nil {
		return fmt.Errorf("pull %s: %w", name, err)
	}
	if pull.Error != "" {
		return fmt.Errorf("pull %s: %w", name, errors.New(pull.Error))
	}
	return nil
}

func ollamaCall(ctx context.Context, method, url string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("ollama %s %s: %s: %s", method, url, resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
