package deepseek

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// server answers /chat/completions with reply and records the decoded request body.
func server(t *testing.T, reply string, body *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("auth = %q", got)
		}
		if body != nil {
			if err := json.NewDecoder(r.Body).Decode(body); err != nil {
				t.Error(err)
			}
		}
		io.WriteString(w, "\n\n"+reply) // keep-alive blank lines come first
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChatSendsToolsAndParsesToolCalls(t *testing.T) {
	var body map[string]any
	srv := server(t, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[
		{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}]},
		"finish_reason":"tool_calls"}]}`, &body)

	c := &Client{BaseURL: srv.URL, APIKey: "key"}
	msg, err := c.Chat(context.Background(), "deepseek-flash",
		[]Message{{Role: "user", Content: "hi"}},
		[]Tool{{Type: "function", Function: ToolFunction{Name: "read_file"}}})
	if err != nil {
		t.Fatal(err)
	}
	if body["model"] != "deepseek-flash" || body["stream"] != false || body["temperature"] != float64(0) {
		t.Errorf("request = %v", body)
	}
	if th, _ := body["thinking"].(map[string]any); th["type"] != "disabled" {
		t.Errorf("thinking = %v", body["thinking"])
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("message = %+v", msg)
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "c1" || tc.Function.Name != "read_file" || tc.Function.Arguments["path"] != "a.go" || tc.Function.ArgsErr != nil {
		t.Errorf("tool call = %+v", tc)
	}
}

func TestToolCallRoundTripsArgumentsAsString(t *testing.T) {
	m := Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Type: "function",
		Function: ToolCallFunction{Name: "grep", Arguments: map[string]any{"pattern": "x"}}}}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"arguments":"{\"pattern\":\"x\"}"`) {
		t.Errorf("json = %s", b)
	}
	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ToolCalls[0].Function.Arguments["pattern"] != "x" {
		t.Errorf("back = %+v", back)
	}
}

func TestInvalidArgumentsAreReportedNotFatal(t *testing.T) {
	var f ToolCallFunction
	if err := json.Unmarshal([]byte(`{"name":"grep","arguments":"{pattern: x"}`), &f); err != nil {
		t.Fatal(err)
	}
	if f.ArgsErr == nil || f.Raw != "{pattern: x" {
		t.Errorf("f = %+v", f)
	}
	// the original string is sent back unchanged
	b, _ := json.Marshal(f)
	if !strings.Contains(string(b), `{pattern: x`) {
		t.Errorf("json = %s", b)
	}
}

func TestChatJSONUsesJSONModeAndSchemaPrompt(t *testing.T) {
	var body map[string]any
	srv := server(t, `{"choices":[{"message":{"role":"assistant","content":"{\"findings\":[{\"line\":3}]}"},"finish_reason":"stop"}]}`, &body)

	var out struct {
		Findings []struct {
			Line int `json:"line"`
		} `json:"findings"`
	}
	c := &Client{BaseURL: srv.URL, APIKey: "key"}
	err := c.ChatJSON(context.Background(), "m", []Message{{Role: "user", Content: "x"}},
		json.RawMessage(`{"type":"object"}`), &out)
	if err != nil {
		t.Fatal(err)
	}
	if rf, _ := body["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Errorf("response_format = %v", body["response_format"])
	}
	msgs, _ := body["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	if !strings.Contains(last["content"].(string), `{"type":"object"}`) || !strings.Contains(last["content"].(string), "JSON") {
		t.Errorf("schema prompt = %v", last)
	}
	if len(out.Findings) != 1 || out.Findings[0].Line != 3 {
		t.Errorf("decoded = %+v", out)
	}
}

func TestChatJSONRejectsEmptyAndInvalidReplies(t *testing.T) {
	for reply, want := range map[string]string{
		`{"choices":[{"message":{"role":"assistant","content":"not json"}}]}`: "not valid JSON",
		`{"choices":[{"message":{"role":"assistant","content":"  "}}]}`:       "empty reply",
	} {
		srv := server(t, reply, nil)
		var out map[string]any
		err := (&Client{BaseURL: srv.URL, APIKey: "key"}).ChatJSON(context.Background(), "m", nil, json.RawMessage(`{}`), &out)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

func TestRetriesOverloadAndExplainsAuthErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "key", Retries: 2, Backoff: time.Millisecond}
	msg, err := c.Chat(context.Background(), "m", nil, nil)
	if err != nil || msg.Content != "ok" || calls.Load() != 2 {
		t.Errorf("msg=%+v err=%v calls=%d", msg, err, calls.Load())
	}

	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer auth.Close()
	_, err = (&Client{BaseURL: auth.URL, APIKey: "key", Retries: 2}).Chat(context.Background(), "m", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") || !strings.Contains(err.Error(), "bad key") {
		t.Errorf("err = %v", err)
	}
}
