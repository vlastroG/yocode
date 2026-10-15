package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestChatSendsRequestAndParsesToolCalls(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		io.WriteString(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"git_status","arguments":{}}}]},"done":true}`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, NumCtx: 4096}
	msg, err := c.Chat(context.Background(), "m",
		[]Message{{Role: "user", Content: "hi"}},
		[]Tool{{Type: "function", Function: ToolFunction{Name: "git_status"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "m" || got.Stream || got.Options["num_ctx"] != float64(4096) || len(got.Tools) != 1 {
		t.Errorf("request = %+v", got)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "git_status" {
		t.Errorf("message = %+v", msg)
	}
}

func TestEnsureModelsPullsOnlyMissing(t *testing.T) {
	var pulled []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			io.WriteString(w, `{"models":[{"name":"nomic-embed-text:latest"}]}`)
		case "/api/pull":
			var b struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			pulled = append(pulled, b.Model)
			io.WriteString(w, `{"status":"success"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	if err := c.EnsureModels(context.Background(), io.Discard, "qwen2.5:7b", "nomic-embed-text"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pulled, []string{"qwen2.5:7b"}) {
		t.Errorf("pulled = %v", pulled)
	}
}

func TestErrorStatusIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := (&Client{BaseURL: srv.URL}).Chat(context.Background(), "m", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

func TestChatJSONSendsSchemaAndDecodes(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		io.WriteString(w, `{"message":{"role":"assistant","content":"{\"findings\":[{\"line\":3}]}"},"done":true}`)
	}))
	defer srv.Close()

	var out struct {
		Findings []struct {
			Line int `json:"line"`
		} `json:"findings"`
	}
	c := &Client{BaseURL: srv.URL, NumCtx: 4096}
	err := c.ChatJSON(context.Background(), "m", []Message{{Role: "user", Content: "x"}},
		json.RawMessage(`{"type":"object"}`), &out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Format) != `{"type":"object"}` || len(got.Tools) != 0 {
		t.Errorf("request = %+v", got)
	}
	if got.Options["temperature"] != float64(0) || got.Options["num_ctx"] != float64(4096) {
		t.Errorf("options = %v", got.Options)
	}
	if len(out.Findings) != 1 || out.Findings[0].Line != 3 {
		t.Errorf("decoded = %+v", out)
	}
}

func TestChatJSONRejectsInvalidReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"message":{"role":"assistant","content":"not json"},"done":true}`)
	}))
	defer srv.Close()

	var out map[string]any
	err := (&Client{BaseURL: srv.URL}).ChatJSON(context.Background(), "m", nil, json.RawMessage(`{}`), &out)
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("err = %v", err)
	}
}

func recordChat(t *testing.T, c *Client) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		io.WriteString(w, `{"message":{"role":"assistant","content":"{}"},"done":true}`)
	}))
	defer srv.Close()
	c.BaseURL = srv.URL
	if _, err := c.Chat(context.Background(), "m", nil, nil); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestChatSeedAndThink(t *testing.T) {
	f := false
	body := recordChat(t, &Client{Seed: 42, Think: &f})
	opts, _ := body["options"].(map[string]any)
	if opts["seed"] != float64(42) || opts["temperature"] != float64(0) {
		t.Errorf("options = %v", opts)
	}
	if v, ok := body["think"]; !ok || v != false {
		t.Errorf("think = %v (present %v)", v, ok)
	}
}

func TestChatZeroClientSendsNoSeedOrThink(t *testing.T) {
	body := recordChat(t, &Client{})
	if _, ok := body["think"]; ok {
		t.Error("think should be absent")
	}
	if opts, ok := body["options"].(map[string]any); ok {
		if _, has := opts["seed"]; has {
			t.Error("seed should be absent")
		}
	}
}

func TestChatJSONSeedKeepsTemperatureZero(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"message":{"role":"assistant","content":"{}"},"done":true}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Seed: 7}
	var out map[string]any
	if err := c.ChatJSON(context.Background(), "m", nil, json.RawMessage(`{}`), &out); err != nil {
		t.Fatal(err)
	}
	opts := body["options"].(map[string]any)
	if opts["seed"] != float64(7) || opts["temperature"] != float64(0) {
		t.Errorf("options = %v", opts)
	}
}
