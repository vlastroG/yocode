package jev

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

	"github.com/vlastroG/yocode/internal/rag"
)

type request struct {
	State     map[string]any      `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

// server decodes each request into got and answers with reply(got).
func server(t *testing.T, reply func(request) string) (*Client, *request) {
	t.Helper()
	var got request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		io.WriteString(w, reply(got))
	}))
	t.Cleanup(srv.Close)
	return &Client{URL: srv.URL, APIKey: "key"}, &got
}

func TestPlanReplySendsStateAndChoice(t *testing.T) {
	c, got := server(t, func(request) string {
		return `{"model":"jev-1.13.0","answers":{"reply":{"type":"choice","choice":"ask","probabilities":{"ask":0.9},"confidence":0.87}}}`
	})
	choice, conf, err := c.PlanReply(context.Background(), "write a changelog", "## Plan", "what does step 2 do?")
	if err != nil || choice != ReplyAsk || conf != 0.87 {
		t.Fatalf("choice=%q conf=%v err=%v", choice, conf, err)
	}
	if got.Model != DefaultModel || got.State["message"] != "what does step 2 do?" || got.State["plan"] != "## Plan" {
		t.Errorf("request = %+v", got)
	}
	q := got.Questions["reply"]
	crit, _ := q.Criteria.(map[string]any)
	if q.Type != "choice" || len(crit) != 4 || crit[ReplyRevise] == nil {
		t.Errorf("question = %+v", q)
	}
}

type fixedRetriever struct {
	hits []rag.Hit
	n    int
}

func (f *fixedRetriever) Search(_ context.Context, _ string, n int) ([]rag.Hit, error) {
	f.n = n
	return f.hits[:min(n, len(f.hits))], nil
}

func TestRerankerKeepsRelevantChunksInOrder(t *testing.T) {
	c, got := server(t, func(request) string {
		return `{"answers":{"0":{"type":"noul","noul":0.2},"1":{"type":"noul","noul":0.9},"2":{"type":"noul","noul":0.6}}}`
	})
	inner := &fixedRetriever{hits: []rag.Hit{{Source: "a"}, {Source: "b"}, {Source: "c"}}}
	r := &Reranker{Inner: inner, Jev: c}
	hits, err := r.Search(context.Background(), "install", 5)
	if err != nil {
		t.Fatal(err)
	}
	if inner.n != 20 {
		t.Errorf("shortlist = %d, want 20", inner.n)
	}
	if len(hits) != 2 || hits[0].Source != "b" || hits[1].Source != "c" {
		t.Errorf("hits = %+v", hits)
	}
	if len(got.Questions) != 3 || got.Questions["1"].Type != "noul" ||
		!strings.Contains(got.Questions["1"].Instructions.(string), "`candidates[1]`") || got.State["query"] != "install" {
		t.Errorf("request = %+v", got)
	}
}

func TestRerankerReturnsNothingWhenNothingHelps(t *testing.T) {
	c, _ := server(t, func(request) string { return `{"answers":{"0":{"type":"noul","noul":0.1}}}` })
	hits, err := (&Reranker{Inner: &fixedRetriever{hits: []rag.Hit{{Source: "a"}}}, Jev: c}).Search(context.Background(), "q", 3)
	if err != nil || len(hits) != 0 {
		t.Errorf("hits=%v err=%v", hits, err)
	}
}

func TestRerankerFallsBackWhenJevFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer srv.Close()
	var reported error
	inner := &fixedRetriever{hits: []rag.Hit{{Source: "a"}, {Source: "b"}, {Source: "c"}}}
	r := &Reranker{Inner: inner, Jev: &Client{URL: srv.URL, APIKey: "key"}, OnError: func(err error) { reported = err }}
	hits, err := r.Search(context.Background(), "q", 2)
	if err != nil || len(hits) != 2 || hits[0].Source != "a" {
		t.Errorf("hits=%v err=%v", hits, err)
	}
	if reported == nil || !strings.Contains(reported.Error(), "bad key") {
		t.Errorf("reported = %v", reported)
	}
}

func TestAskRetriesOverload(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(529)
			return
		}
		io.WriteString(w, `{"answers":{"x":{"type":"noul","noul":0.7}}}`)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, APIKey: "key", Retries: 2, Backoff: time.Millisecond}
	answers, err := c.Ask(context.Background(), "s", map[string]Question{"x": {Type: "noul", Instructions: "?"}})
	if err != nil || answers["x"].Noul != 0.7 || calls.Load() != 2 {
		t.Errorf("answers=%v err=%v calls=%d", answers, err, calls.Load())
	}
}
