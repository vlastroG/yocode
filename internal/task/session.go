package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/vlastroG/yocode/internal/ollama"
)

const usage = "usage: task <goal> [--yes] or task --plan <id>"

// Session holds the open task: its goal, the planning history and the plan awaiting approval.
// Model calls run outside the lock, so the UI can call Pending while a task runs.
type Session struct {
	Planner  *Planner
	Executor *Executor
	Store    Store

	mu      sync.Mutex
	goal    string
	history []ollama.Message // goal, notes, then plan/correction pairs
	pending *Plan
}

// Start drafts a plan for a new goal and replaces any pending plan.
func (s *Session) Start(ctx context.Context, goal string) (Plan, error) {
	plan, notes, err := s.Planner.Draft(ctx, goal)
	if err != nil {
		return Plan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.goal, s.history, s.pending = goal, taskHistory(goal, notes), &plan
	return plan, nil
}

// Revise replans the pending plan with the user's correction; all earlier corrections are kept.
func (s *Session) Revise(ctx context.Context, feedback string) (Plan, error) {
	s.mu.Lock()
	if s.pending == nil {
		s.mu.Unlock()
		return Plan{}, errors.New("no pending plan")
	}
	goal := s.goal
	history := append(append([]ollama.Message(nil), s.history...),
		ollama.Message{Role: "assistant", Content: s.pending.Markdown()})
	s.mu.Unlock()

	plan, err := s.Planner.Revise(ctx, history, feedback)
	if err != nil {
		return Plan{}, err
	}
	plan.Goal = goal
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(history, ollama.Message{Role: "user", Content: feedback})
	s.pending = &plan
	return plan, nil
}

func (s *Session) Pending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending != nil
}

// Approve saves the pending plan under a new id, runs it, saves the result and closes the task.
func (s *Session) Approve(ctx context.Context) (Result, error) {
	s.mu.Lock()
	if s.pending == nil {
		s.mu.Unlock()
		return Result{}, errors.New("no pending plan")
	}
	plan := *s.pending
	plan.ID = s.Store.NewID(plan.Goal)
	if err := s.Store.SavePlan(plan); err != nil {
		s.mu.Unlock()
		return Result{}, fmt.Errorf("save plan: %w", err)
	}
	s.clear()
	s.mu.Unlock()
	return s.run(ctx, plan)
}

// Cancel drops the pending plan and reports whether there was one.
func (s *Session) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	had := s.pending != nil
	s.clear()
	return had
}

// Replay runs a saved plan again without planning; it also closes any open task.
func (s *Session) Replay(ctx context.Context, id string) (Result, error) {
	plan, err := s.Store.LoadPlan(id)
	if err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	s.clear()
	s.mu.Unlock()
	return s.run(ctx, plan)
}

func (s *Session) run(ctx context.Context, plan Plan) (Result, error) {
	res, err := s.Executor.Run(ctx, plan)
	if err != nil {
		return res, err
	}
	if err := s.Store.SaveResult(res); err != nil {
		return res, fmt.Errorf("save result: %w", err)
	}
	return res, nil
}

// clear closes the task; the caller holds mu.
func (s *Session) clear() {
	s.goal, s.history, s.pending = "", nil, nil
}

// Args are the task command's arguments.
type Args struct {
	Goal   string
	PlanID string
	Yes    bool // run the plan without asking for corrections
}

// ParseArgs reads "<goal...> [--yes]" or "--plan <id>".
func ParseArgs(args []string) (Args, error) {
	if len(args) > 0 && args[0] == "--plan" {
		if len(args) != 2 {
			return Args{}, errors.New(usage)
		}
		return Args{PlanID: args[1]}, nil
	}
	var a Args
	var words []string
	for _, w := range args {
		switch w {
		case "--yes":
			a.Yes = true
		case "--plan":
			return Args{}, errors.New(usage)
		default:
			words = append(words, w)
		}
	}
	a.Goal = strings.TrimSpace(strings.Join(words, " "))
	if a.Goal == "" {
		return Args{}, errors.New(usage)
	}
	return a, nil
}
