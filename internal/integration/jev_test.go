//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/vlastroG/yocode/internal/jev"
)

const samplePlan = `## Plan: write CHANGELOG.md from the git history

1. **Write CHANGELOG.md** (edit) - read the git log and write CHANGELOG.md with one entry per commit.
2. **Commit it** (git) - commit CHANGELOG.md on a new branch docs/changelog.`

func TestJevRoutesPlanReplies(t *testing.T) {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("TYPESAFE_API_KEY is not set")
	}
	c := &jev.Client{APIKey: key, Model: os.Getenv("JEV_MODEL"), Retries: 2}
	cases := map[string]string{
		"what does step 2 commit exactly?":          jev.ReplyAsk,
		"why does step 2 need git access?":          jev.ReplyAsk,
		"also add a section about breaking changes": jev.ReplyRevise,
		"skip the commit, just write the file":      jev.ReplyRevise,
		"looks good, run it":                        jev.ReplyApprove,
		"ok go ahead":                               jev.ReplyApprove,
		"never mind, drop it":                       jev.ReplyCancel,
		"forget this task":                          jev.ReplyCancel,
	}
	for msg, want := range cases {
		choice, conf, err := c.PlanReply(context.Background(), "write CHANGELOG.md from the git history", samplePlan, msg)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%-45q -> %s (confidence %.2f)", msg, choice, conf)
		if choice != want {
			t.Errorf("%q: got %s, want %s", msg, choice, want)
		}
	}
}
