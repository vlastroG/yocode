package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/agent"
	"github.com/vlastroG/yocode/internal/ollama"
	"github.com/vlastroG/yocode/internal/rag"
	"github.com/vlastroG/yocode/internal/task"
)

func TestParse(t *testing.T) {
	s := New(Deps{}) // handlers are not run, so no agent or pipeline is needed

	c, args, err := s.Parse("how do I install the platform?")
	if err != nil || c.Name != "ask" || !reflect.DeepEqual(args, []string{"how do I install the platform?"}) {
		t.Errorf("plain text: %q %v %v", c.Name, args, err)
	}
	c, args, err = s.Parse("/review main feature")
	if err != nil || c.Name != "review" || !reflect.DeepEqual(args, []string{"main", "feature"}) {
		t.Errorf("/review: %q %v %v", c.Name, args, err)
	}
	for input, wantErr := range map[string]string{
		"/review": "usage: review",
		"/ask":    "usage: ask",
		"/nope":   "unknown command /nope",
	} {
		if _, _, err := s.Parse(input); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("Parse(%q) err = %v, want %q", input, err, wantErr)
		}
	}
}

func TestLookupAndUsage(t *testing.T) {
	s := New(Deps{})
	if _, ok := s.Lookup("review"); !ok {
		t.Error("review not found")
	}
	if _, ok := s.Lookup("chat"); ok {
		t.Error("chat is not a command")
	}
	u := s.Usage("/")
	for _, want := range []string{"/ask <question>", "/review <base> [head]", "review the changes of head"} {
		if !strings.Contains(u, want) {
			t.Errorf("usage lacks %q:\n%s", want, u)
		}
	}
	if strings.Contains(s.Usage(""), "/ask") {
		t.Error("CLI usage must not have the / prefix")
	}
}

const testPlan = `{"steps":[{"title":"Write CHANGELOG","instructions":"create CHANGELOG.md","access":"edit"}]}`

// fakeLLM replies "ok" to every chat and returns testPlan for every plan request.
type fakeLLM struct {
	chats     [][]ollama.Message
	planCalls int
}

func (f *fakeLLM) Chat(_ context.Context, _ string, msgs []ollama.Message, _ []ollama.Tool) (ollama.Message, error) {
	f.chats = append(f.chats, msgs)
	return ollama.Message{Role: "assistant", Content: "ok"}, nil
}

func (f *fakeLLM) ChatJSON(_ context.Context, _ string, _ []ollama.Message, _ json.RawMessage, out any) error {
	f.planCalls++
	return json.Unmarshal([]byte(testPlan), out)
}

type fakeTools struct{}

func (fakeTools) Tools(context.Context) ([]ollama.Tool, error) { return nil, nil }
func (fakeTools) Call(context.Context, string, map[string]any) (string, error) {
	return "", nil
}

type fakeDocs struct{}

func (fakeDocs) Search(context.Context, string, int) ([]rag.Hit, error) { return nil, nil }

type taskRig struct {
	llm  *fakeLLM
	runs int // executor steps started
	deps Deps
}

func newTaskRig(t *testing.T) *taskRig {
	r := &taskRig{llm: &fakeLLM{}}
	r.deps = Deps{
		Agent: &agent.Agent{LLM: r.llm, Model: "m", Tools: fakeTools{}, Docs: fakeDocs{}},
		Task: &task.Session{
			Planner: &task.Planner{LLM: r.llm, Model: "m", Tools: fakeTools{}, Docs: fakeDocs{}},
			Executor: &task.Executor{LLM: r.llm, Model: "m", Status: fakeTools{},
				ToolsFor: func(task.Access) agent.Tools { r.runs++; return fakeTools{} }},
			Store: task.Store{Dir: t.TempDir()},
		},
	}
	return r
}

func runCmd(t *testing.T, s Set, name string, args ...string) (Command, string) {
	t.Helper()
	c, ok := s.Lookup(name)
	if !ok {
		t.Fatalf("no command %q", name)
	}
	out, err := c.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return c, out
}

func TestParseRoutesWhilePlanPending(t *testing.T) {
	for _, finish := range []string{"go", "cancel"} {
		r := newTaskRig(t)
		s := New(r.deps)
		if c, _, err := s.Parse("hello"); err != nil || c.Name != "ask" {
			t.Fatalf("before /task: %q %v", c.Name, err)
		}
		if _, _, err := s.Parse("/" + finish); err == nil || !strings.Contains(err.Error(), "no plan pending") {
			t.Fatalf("/%s without a plan: %v", finish, err)
		}
		c, out := runCmd(t, s, "task", "write", "a", "changelog")
		if !strings.Contains(out, "## Plan") || c.ResultLabel() != "plan" {
			t.Errorf("task: label %q, output %q", c.ResultLabel(), out)
		}
		c, args, err := s.Parse("also update README")
		if err != nil || c.Name != "revise" || !reflect.DeepEqual(args, []string{"also update README"}) {
			t.Fatalf("while pending: %q %v %v", c.Name, args, err)
		}
		if _, out := runCmd(t, s, "revise", args...); !strings.Contains(out, "## Plan") {
			t.Errorf("revise output %q", out)
		}
		c, _ = runCmd(t, s, finish)
		if r.deps.Task.Pending() {
			t.Errorf("%s: plan still pending", finish)
		}
		if c.ResultLabel() != "task" {
			t.Errorf("%s: label %q", finish, c.ResultLabel())
		}
		if c, _, err := s.Parse("hello"); err != nil || c.Name != "ask" {
			t.Errorf("after /%s: %q %v", finish, c.Name, err)
		}
		if ran := r.runs > 0; ran != (finish == "go") {
			t.Errorf("%s: executor ran = %v", finish, ran)
		}
	}
}

func TestGoRemembersResult(t *testing.T) {
	r := newTaskRig(t)
	s := New(r.deps)
	runCmd(t, s, "task", "write", "a", "changelog")
	runCmd(t, s, "go")
	runCmd(t, s, "ask", "what changed?")
	last := r.llm.chats[len(r.llm.chats)-1]
	var all strings.Builder
	for _, m := range last {
		all.WriteString(m.Content + "\n")
	}
	if !strings.Contains(all.String(), "/task write a changelog") || !strings.Contains(all.String(), "Write CHANGELOG") {
		t.Errorf("ask history lacks the task result:\n%s", all.String())
	}
}

func TestTaskYesRunsWithoutPrompt(t *testing.T) {
	r := newTaskRig(t)
	r.deps.Prompt = func(string) (string, error) { t.Fatal("prompted with --yes"); return "", nil }
	runCmd(t, New(r.deps), "task", "write", "a", "changelog", "--yes")
	if r.runs == 0 || r.deps.Task.Pending() {
		t.Errorf("runs = %d, pending = %v", r.runs, r.deps.Task.Pending())
	}
}

func TestTaskPromptEOFDoesNotRun(t *testing.T) {
	r := newTaskRig(t)
	r.deps.Prompt = func(string) (string, error) { return "", io.EOF }
	c, _ := New(r.deps).Lookup("task")
	_, err := c.Run(context.Background(), []string{"write", "a", "changelog"})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v", err)
	}
	if r.runs != 0 || r.deps.Task.Pending() {
		t.Errorf("runs = %d, pending = %v", r.runs, r.deps.Task.Pending())
	}
}

func TestTaskPromptLoop(t *testing.T) {
	r := newTaskRig(t)
	replies := []string{"add a step for the changelog", ""}
	var prompts []string
	r.deps.Prompt = func(text string) (string, error) {
		prompts = append(prompts, text)
		if len(replies) == 0 {
			return "", errors.New("prompted too often")
		}
		reply := replies[0]
		replies = replies[1:]
		return reply, nil
	}
	runCmd(t, New(r.deps), "task", "write", "a", "changelog")
	if r.llm.planCalls != 2 {
		t.Errorf("plan calls = %d, want draft + 1 revision", r.llm.planCalls)
	}
	if r.runs == 0 {
		t.Error("the plan did not run")
	}
	if len(prompts) != 2 || !strings.Contains(prompts[0], "## Plan") || !strings.Contains(prompts[0], "q = cancel") {
		t.Errorf("prompts = %q", prompts)
	}

	r = newTaskRig(t)
	r.deps.Prompt = func(string) (string, error) { return "q", nil }
	if _, out := runCmd(t, New(r.deps), "task", "write", "a", "changelog"); out != "cancelled" {
		t.Errorf("output = %q", out)
	}
	if r.runs != 0 || r.deps.Task.Pending() {
		t.Errorf("runs = %d, pending = %v", r.runs, r.deps.Task.Pending())
	}
}

func TestCLIHidesChatOnly(t *testing.T) {
	u := New(Deps{}).CLI().Usage("")
	if !strings.Contains(u, "task <goal>") {
		t.Errorf("CLI usage lacks task:\n%s", u)
	}
	for _, chatOnly := range []string{"revise", "go ", "cancel"} {
		if strings.Contains(u, chatOnly) {
			t.Errorf("CLI usage has %q:\n%s", chatOnly, u)
		}
	}
	if _, _, err := New(Deps{}).Parse("/revise more"); err == nil || !strings.Contains(err.Error(), "no plan pending") {
		t.Errorf("/revise without a session: %v", err)
	}
}
