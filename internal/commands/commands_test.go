package commands

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	s := New(nil, nil) // handlers are not run, so no agent or pipeline is needed

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
	s := New(nil, nil)
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
