package rag

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"testing"

	"github.com/vlastroG/yocode/internal/docs"
)

func fakeEmbed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 64)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%64]++
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(n))
	}
	return v, nil
}

var chunks = []docs.Chunk{
	{Source: "project/install.md", Heading: "Install", Text: "install with docker compose up"},
	{Source: "project/api.md", Heading: "API", Text: "the http api returns json responses"},
}

func TestEnsureBuildsOnceAndSearches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s, err := Open(dir, fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	ix, built, err := s.Ensure(ctx, "k", false, func() ([]docs.Chunk, error) { return chunks, nil })
	if err != nil || !built || ix.Count() != 2 {
		t.Fatalf("built=%v count=%d err=%v", built, ix.Count(), err)
	}
	hits, err := ix.Search(ctx, "how do I install with docker", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Source != "project/install.md" {
		t.Errorf("hits = %+v", hits)
	}

	// A new process (reopened DB) reuses the persisted index.
	s2, err := Open(dir, fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	_, built, err = s2.Ensure(ctx, "k", false, func() ([]docs.Chunk, error) {
		t.Fatal("load must not be called for an existing index")
		return nil, nil
	})
	if err != nil || built {
		t.Fatalf("built=%v err=%v", built, err)
	}

	// Reindex forces a rebuild.
	_, built, err = s2.Ensure(ctx, "k", true, func() ([]docs.Chunk, error) { return chunks, nil })
	if err != nil || !built {
		t.Fatalf("reindex: built=%v err=%v", built, err)
	}
}

func TestSearchEmptyIndex(t *testing.T) {
	s, err := Open(t.TempDir(), fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	ix, _, err := s.Ensure(context.Background(), "empty", false, func() ([]docs.Chunk, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	hits, err := ix.Search(context.Background(), "anything", 5)
	if err != nil || len(hits) != 0 {
		t.Errorf("hits=%v err=%v", hits, err)
	}
}
