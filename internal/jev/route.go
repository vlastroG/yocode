package jev

import (
	"context"
	"fmt"
)

// Plan replies, as Choice options. The first option is the safe default (revising the plan).
const (
	ReplyRevise  = "revise"
	ReplyApprove = "approve"
	ReplyCancel  = "cancel"
	ReplyAsk     = "ask"
)

var planReply = Question{
	Type: "choice",
	Instructions: "yocode proposed the plan in `plan` for the goal in `goal` and waits for the user to " +
		"approve, correct or drop it. The user typed `message`. What does `message` ask yocode to do?",
	Criteria: map[string]any{
		ReplyRevise: "Change the plan: add, remove, reorder or reword steps, or any other instruction about " +
			"what the task should do differently (e.g. 'also update the README', 'skip step 2').",
		ReplyApprove: "Accept the plan as it is and run it now, with no changes requested " +
			"(e.g. 'ok', 'go ahead', 'looks good, run it').",
		ReplyCancel: "Drop the plan without running it (e.g. 'never mind', 'cancel', 'forget it').",
		ReplyAsk: "Ask a question about the plan, the code or the project and expect an answer, " +
			"without asking to change, run or drop the plan (e.g. 'what does step 2 change?').",
	},
}

// PlanReply sorts a message typed while a plan is pending into one of the Reply* options.
func (c *Client) PlanReply(ctx context.Context, goal, plan, message string) (string, float64, error) {
	answers, err := c.Ask(ctx, map[string]string{"goal": goal, "plan": plan, "message": message},
		map[string]Question{"reply": planReply})
	if err != nil {
		return "", 0, err
	}
	a, ok := answers["reply"]
	if !ok || a.Choice == "" {
		return "", 0, fmt.Errorf("jev: no answer for the plan reply")
	}
	return a.Choice, a.Confidence, nil
}
