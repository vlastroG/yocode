// Package rag stores doc chunk embeddings and retrieves the most relevant ones.
package rag

import (
	"context"
	"fmt"
	"strconv"

	"github.com/philippgille/chromem-go"
	"github.com/vlastroG/yocode/internal/docs"
)

type Store struct {
	db    *chromem.DB
	embed chromem.EmbeddingFunc
}

type Index struct {
	col *chromem.Collection
}

type Hit struct {
	Source  string
	Heading string
	Text    string // includes a "Source: ..." header, ready to paste into a prompt
	Score   float32
}

func Open(dir string, embed chromem.EmbeddingFunc) (*Store, error) {
	db, err := chromem.NewPersistentDB(dir, false)
	if err != nil {
		return nil, fmt.Errorf("open vector db: %w", err)
	}
	return &Store{db: db, embed: embed}, nil
}

// Ensure returns the index named key. It calls load and embeds the chunks when the index is
// empty or reindex is set; built reports whether that happened.
func (s *Store) Ensure(ctx context.Context, key string, reindex bool, load func() ([]docs.Chunk, error)) (ix *Index, built bool, err error) {
	if reindex {
		if err := s.db.DeleteCollection(key); err != nil {
			return nil, false, err
		}
	}
	col, err := s.db.GetOrCreateCollection(key, nil, s.embed)
	if err != nil {
		return nil, false, err
	}
	if col.Count() > 0 {
		return &Index{col: col}, false, nil
	}

	chunks, err := load()
	if err != nil {
		return nil, true, err
	}
	documents := make([]chromem.Document, len(chunks))
	for i, c := range chunks {
		documents[i] = chromem.Document{
			ID:       strconv.Itoa(i),
			Content:  fmt.Sprintf("Source: %s\nSection: %s\n\n%s", c.Source, c.Heading, c.Text),
			Metadata: map[string]string{"source": c.Source, "heading": c.Heading},
		}
	}
	if len(documents) > 0 {
		if err := col.AddDocuments(ctx, documents, 4); err != nil {
			_ = s.db.DeleteCollection(key) // don't leave a half-built index to be reused next time
			return nil, true, fmt.Errorf("embed docs: %w", err)
		}
	}
	return &Index{col: col}, true, nil
}

func (ix *Index) Count() int { return ix.col.Count() }

func (ix *Index) Search(ctx context.Context, query string, n int) ([]Hit, error) {
	n = min(n, ix.col.Count()) // chromem rejects n > number of documents
	if n == 0 {
		return nil, nil
	}
	res, err := ix.col.Query(ctx, query, n, nil, nil)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, len(res))
	for i, r := range res {
		hits[i] = Hit{Source: r.Metadata["source"], Heading: r.Metadata["heading"], Text: r.Content, Score: r.Similarity}
	}
	return hits, nil
}
