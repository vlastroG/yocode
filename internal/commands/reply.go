package commands

import (
	"context"

	"github.com/vlastroG/yocode/internal/jev"
)

// Confidence a routed plan reply needs before yocode acts on it. Running or dropping the plan is
// harder to undo than answering a question; anything below falls back to revising the plan.
const (
	askConfidence = 0.6
	actConfidence = 0.85
)

// Router sorts a message typed while a plan is pending into one of the jev.Reply* options.
type Router func(ctx context.Context, goal, plan, message string) (choice string, confidence float64, err error)

// replyIntent applies the confidence policy to a routed choice.
func replyIntent(choice string, confidence float64) string {
	switch {
	case choice == jev.ReplyAsk && confidence >= askConfidence:
		return jev.ReplyAsk
	case (choice == jev.ReplyApprove || choice == jev.ReplyCancel) && confidence >= actConfidence:
		return choice
	}
	return jev.ReplyRevise
}

// route decides what a plain message means while a plan is pending; without a router, or when
// routing fails, it revises the plan as before.
func (d Deps) route(ctx context.Context, message string) string {
	if d.Router == nil || d.Task == nil {
		return jev.ReplyRevise
	}
	plan, ok := d.Task.PendingPlan()
	if !ok {
		return jev.ReplyRevise
	}
	choice, confidence, err := d.Router(ctx, plan.Goal, plan.Markdown(), message)
	if err != nil {
		return jev.ReplyRevise
	}
	return replyIntent(choice, confidence)
}
