package task

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/deepseek"
)

var testPlan = Plan{ID: "p1", Goal: "document X", Steps: []Step{
	{Title: "Find X", Instructions: "grep for X usages", Access: AccessRead},
	{Title: "Edit README", Instructions: "document X in README.md", Access: AccessEdit},
}}

type executorRig struct {
	llm      *fakeLLM
	accesses []Access
	status   *fakeTools
	progress []string
	exec     *Executor
}

func newRig(chat func(int, []deepseek.Message, []deepseek.Tool) (deepseek.Message, error)) *executorRig {
	r := &executorRig{llm: &fakeLLM{chat: chat}, status: &fakeTools{}}
	r.exec = &Executor{
		LLM: r.llm, Model: "m", Status: r.status,
		ToolsFor: func(a Access) agent.Tools {
			r.accesses = append(r.accesses, a)
			return &fakeTools{}
		},
		OnProgress: func(s string) { r.progress = append(r.progress, s) },
	}
	return r
}

func script(replies ...deepseek.Message) func(int, []deepseek.Message, []deepseek.Tool) (deepseek.Message, error) {
	return func(i int, _ []deepseek.Message, _ []deepseek.Tool) (deepseek.Message, error) {
		if i >= len(replies) {
			return deepseek.Message{}, errors.New("unexpected call")
		}
		return replies[i], nil
	}
}

func TestRunStepsWithSubagents(t *testing.T) {
	r := newRig(script(toolCall("read_file"), reply("summary one"), reply("summary two"), reply("all done")))
	res, err := r.exec.Run(context.Background(), testPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.accesses, []Access{AccessRead, AccessEdit}) {
		t.Errorf("accesses = %v", r.accesses)
	}
	if len(res.Steps) != 2 || res.Steps[0].Summary != "summary one" || res.Steps[1].Summary != "summary two" {
		t.Errorf("steps = %+v", res.Steps)
	}
	step2 := joined(r.llm.chatCalls[2])
	if !strings.Contains(step2, "summary one") || !strings.Contains(step2, "document X in README.md") {
		t.Errorf("step 2 prompt lacks the earlier summary or its instructions:\n%s", step2)
	}
	if strings.Contains(step2, "TOOL-OUTPUT") {
		t.Errorf("step 2 prompt contains step 1 tool output:\n%s", step2)
	}
	if !strings.Contains(step2, "→") {
		t.Errorf("step 2 prompt does not mark the current step:\n%s", step2)
	}
	if res.Answer != "all done" {
		t.Errorf("answer = %q", res.Answer)
	}
	if r.llm.toolDefs[3] != nil {
		t.Error("root call got tools")
	}
	if root := joined(r.llm.chatCalls[3]); !strings.Contains(root, "TOOL-OUTPUT git_status") {
		t.Errorf("root prompt lacks git status:\n%s", root)
	}
	if !reflect.DeepEqual(r.progress[:2], []string{"step 1/2: Find X", "step 2/2: Edit README"}) {
		t.Errorf("progress = %v", r.progress)
	}
}

func TestFailedStepStopsRun(t *testing.T) {
	r := newRig(func(i int, _ []deepseek.Message, _ []deepseek.Tool) (deepseek.Message, error) {
		if i == 0 {
			return deepseek.Message{}, errors.New("model crashed")
		}
		return reply("partial result"), nil
	})
	res, err := r.exec.Run(context.Background(), testPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 1 || !strings.Contains(res.Steps[0].Err, "model crashed") {
		t.Errorf("steps = %+v", res.Steps)
	}
	if res.Answer != "partial result" || len(r.llm.chatCalls) != 2 {
		t.Errorf("answer = %q, chat calls = %d", res.Answer, len(r.llm.chatCalls))
	}
	if root := joined(r.llm.chatCalls[1]); !strings.Contains(root, "Edit README") {
		t.Errorf("root prompt does not mention the step that did not run:\n%s", root)
	}
}

func TestSummaryIsCut(t *testing.T) {
	long := strings.Repeat("a", 2000)
	r := newRig(script(reply(long), reply("s2"), reply("done")))
	res, err := r.exec.Run(context.Background(), testPlan)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(res.Steps[0].Summary)); n > 1500 {
		t.Errorf("summary length = %d", n)
	}
}

func TestSingleMode(t *testing.T) {
	r := newRig(script(reply("did everything"), reply("done")))
	r.exec.Single = true
	res, err := r.exec.Run(context.Background(), testPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.accesses, []Access{AccessEdit}) {
		t.Errorf("accesses = %v", r.accesses)
	}
	if len(r.llm.chatCalls) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(r.llm.chatCalls))
	}
	prompt := joined(r.llm.chatCalls[0])
	for _, s := range testPlan.Steps {
		if !strings.Contains(prompt, s.Instructions) {
			t.Errorf("single prompt lacks %q", s.Instructions)
		}
	}
	if len(res.Steps) != 1 || res.Steps[0].Summary != "did everything" || res.Answer != "done" {
		t.Errorf("result = %+v", res)
	}
}

func TestResultMarkdown(t *testing.T) {
	res := Result{Plan: Plan{ID: "20261015-120000-x"}, Answer: "Done.", Steps: []StepResult{
		{Step: testPlan.Steps[0], Summary: "found 3 usages"},
		{Step: testPlan.Steps[1], Err: "no answer"},
	}}
	md := res.Markdown()
	for _, want := range []string{"Done.", "### Steps", "✓ **Find X**", "found 3 usages", "✗ **Edit README**", "no answer",
		"task --plan 20261015-120000-x"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestReadStepWrapsUpWhenOutOfCalls(t *testing.T) {
	replies := make([]deepseek.Message, stepMaxSteps)
	for i := range replies {
		replies[i] = toolCall("read_file")
	}
	replies = append(replies, reply("found X in a.go"), reply("done"))
	r := newRig(script(replies...))
	plan := Plan{Goal: "find X", Steps: testPlan.Steps[:1]}
	res, err := r.exec.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].Err != "" || res.Steps[0].Summary != "found X in a.go" {
		t.Errorf("step = %+v", res.Steps[0])
	}
}
