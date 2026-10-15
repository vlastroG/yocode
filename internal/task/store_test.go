package task

import (
	"reflect"
	"testing"
	"time"
)

func testStore(t *testing.T) Store {
	return Store{Dir: t.TempDir(), Now: func() time.Time { return time.Date(2026, 10, 15, 14, 30, 0, 0, time.UTC) }}
}

func TestNewID(t *testing.T) {
	s := testStore(t)
	if id := s.NewID("Update the docs for X!"); id != "20261015-143000-update-the-docs-for-x" {
		t.Errorf("id = %q", id)
	}
	if id := s.NewID("Обнови документацию"); id != "20261015-143000-task" {
		t.Errorf("non-ascii id = %q", id)
	}
	long := s.NewID("a very long goal that keeps going and going well past the slug limit")
	if slug := long[len("20261015-143000-"):]; len(slug) > 40 || slug[len(slug)-1] == '-' {
		t.Errorf("slug = %q", slug)
	}
}

func TestSaveLoadPlan(t *testing.T) {
	s := testStore(t)
	if err := s.SavePlan(testPlan); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPlan(testPlan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, testPlan) {
		t.Errorf("got %+v", got)
	}
}

func TestLoadPlanRejectsBadID(t *testing.T) {
	if _, err := testStore(t).LoadPlan("../x"); err == nil {
		t.Fatal("expected error")
	}
}
