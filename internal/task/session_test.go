package task

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/agent"
)

const oneStep = `{"steps":[{"title":"Edit CHANGELOG","instructions":"add an entry","access":"edit"}]}`

func newSession(t *testing.T, plans ...string) (*Session, *fakeLLM) {
	llm := &fakeLLM{jsons: plans}
	s := &Session{
		Planner: newPlanner(llm),
		Executor: &Executor{LLM: llm, Model: "m", Status: &fakeTools{},
			ToolsFor: func(Access) agent.Tools { return &fakeTools{} }},
		Store: testStore(t),
	}
	return s, llm
}

func TestSessionRevisions(t *testing.T) {
	ctx := context.Background()
	s, llm := newSession(t, twoSteps, oneStep, oneStep, twoSteps)
	if _, err := s.Start(ctx, "document X"); err != nil {
		t.Fatal(err)
	}
	if !s.Pending() {
		t.Fatal("no pending plan after Start")
	}
	p, err := s.Revise(ctx, "only the changelog")
	if err != nil {
		t.Fatal(err)
	}
	if p.Goal != "document X" || len(p.Steps) != 1 || p.Steps[0].Title != "Edit CHANGELOG" {
		t.Errorf("revised plan = %+v", p)
	}
	if _, err := s.Revise(ctx, "write it in Russian"); err != nil {
		t.Fatal(err)
	}
	second := joined(llm.jsonCalls[2])
	for _, want := range []string{"only the changelog", "write it in Russian", "Edit CHANGELOG"} {
		if !strings.Contains(second, want) {
			t.Errorf("second revision missing %q", want)
		}
	}
	if !s.Cancel() || s.Pending() {
		t.Fatal("cancel did not drop the plan")
	}
	if _, err := s.Start(ctx, "another goal"); err != nil {
		t.Fatal(err)
	}
	if last := joined(llm.jsonCalls[3]); strings.Contains(last, "only the changelog") {
		t.Errorf("new task sees an old correction:\n%s", last)
	}
}

func TestSessionApprove(t *testing.T) {
	ctx := context.Background()
	s, llm := newSession(t, twoSteps, oneStep, twoSteps)
	if _, err := s.Approve(ctx); err == nil {
		t.Fatal("expected error without a pending plan")
	}
	if _, err := s.Start(ctx, "document X"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revise(ctx, "only the changelog"); err != nil {
		t.Fatal(err)
	}
	res, err := s.Approve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Pending() {
		t.Error("plan still pending after Approve")
	}
	id := res.Plan.ID
	if id != "20261015-143000-document-x" {
		t.Errorf("id = %q", id)
	}
	for _, ext := range []string{".json", ".md"} {
		if _, err := os.Stat(filepath.Join(s.Store.Dir, id+ext)); err != nil {
			t.Error(err)
		}
	}
	if _, err := s.Start(ctx, "another goal"); err != nil {
		t.Fatal(err)
	}
	if last := joined(llm.jsonCalls[len(llm.jsonCalls)-1]); strings.Contains(last, "only the changelog") {
		t.Errorf("new task sees an old correction:\n%s", last)
	}
}

func TestSessionReplay(t *testing.T) {
	s, _ := newSession(t)
	if err := s.Store.SavePlan(testPlan); err != nil {
		t.Fatal(err)
	}
	res, err := s.Replay(context.Background(), testPlan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 {
		t.Errorf("steps = %+v", res.Steps)
	}
	if _, err := os.Stat(filepath.Join(s.Store.Dir, testPlan.ID+".md")); err != nil {
		t.Error(err)
	}
}

func TestParseArgs(t *testing.T) {
	good := map[string]struct {
		in   []string
		want Args
	}{
		"goal":   {[]string{"update", "docs", "--yes"}, Args{Goal: "update docs", Yes: true}},
		"replay": {[]string{"--plan", "abc"}, Args{PlanID: "abc"}},
	}
	for name, c := range good {
		got, err := ParseArgs(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
	for _, in := range [][]string{{}, {"--plan"}, {"--yes"}, {"goal", "--plan", "abc"}} {
		if _, err := ParseArgs(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}
