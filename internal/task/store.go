package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxSlug = 40

var (
	validID = regexp.MustCompile(`^[a-z0-9-]+$`)
	nonSlug = regexp.MustCompile(`[^a-z0-9]+`)
)

// Store keeps approved plans (<id>.json) and their results (<id>.md) so runs can be replayed.
type Store struct {
	Dir string
	Now func() time.Time // nil = time.Now
}

// NewID returns "<yyyymmdd-hhmmss>-<slug of the goal>"; a goal with no ASCII letters gets "task".
func (s Store) NewID(goal string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(goal), "-"), "-")
	if len(slug) > maxSlug {
		slug = strings.TrimRight(slug[:maxSlug], "-")
	}
	if slug == "" {
		slug = "task"
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	return now().Format("20060102-150405") + "-" + slug
}

func (s Store) SavePlan(p Plan) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return s.write(p.ID, ".json", data)
}

func (s Store) LoadPlan(id string) (Plan, error) {
	if err := checkID(id); err != nil {
		return Plan{}, err
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, id+".json"))
	if err != nil {
		return Plan{}, fmt.Errorf("load plan: %w", err)
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, fmt.Errorf("load plan %s: %w", id, err)
	}
	return p, nil
}

func (s Store) SaveResult(r Result) error {
	return s.write(r.Plan.ID, ".md", []byte(r.Plan.Markdown()+"\n"+r.Markdown()))
}

func (s Store) write(id, ext string, data []byte) error {
	if err := checkID(id); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, id+ext), data, 0o644)
}

// checkID keeps ids to [a-z0-9-] so they can't point outside Dir.
func checkID(id string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("invalid plan id %q", id)
	}
	return nil
}
