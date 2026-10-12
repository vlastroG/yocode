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
