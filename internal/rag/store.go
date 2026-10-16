// Package rag stores doc chunk embeddings and retrieves the most relevant ones.
package rag

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/philippgille/chromem-go"
	"github.com/vlastroG/yocode/internal/docs"
)

type Store struct {
	db    *chromem.DB
	embed chromem.EmbeddingFunc
}

type Index struct {
	col *chromem.Collection

	once  sync.Once // loads the keyword side from the collection on first search
	kw    *keywordIndex
	texts map[string]string
	meta  map[string]map[string]string
	err   error
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
			Content:  fmt.Sprintf("Source: %s\nPage: %s\nSection: %s\n\n%s", c.Source, c.Title, c.Heading, c.Text),
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

// load reads all chunks back from the collection; ids are sequential ("0".."n-1", see Ensure).
func (ix *Index) load() {
	n := ix.col.Count()
	ids := make([]string, n)
	texts := make([]string, n)
	ix.texts = make(map[string]string, n)
	ix.meta = make(map[string]map[string]string, n)
	for i := range n {
		id := strconv.Itoa(i)
		d, err := ix.col.GetByID(context.Background(), id)
		if err != nil {
			ix.err = fmt.Errorf("load chunk %s: %w", id, err)
			return
		}
		ids[i], texts[i] = id, d.Content
		ix.texts[id], ix.meta[id] = d.Content, d.Metadata
	}
	ix.kw = newKeywordIndex(ids, texts)
}

// Search merges embedding and keyword results by reciprocal rank fusion; Hit.Score is the fused score.
func (ix *Index) Search(ctx context.Context, query string, n int) ([]Hit, error) {
	n = min(n, ix.col.Count()) // chromem rejects n > number of documents
	if n == 0 {
		return nil, nil
	}
	ix.once.Do(ix.load)
	if ix.err != nil {
		return nil, ix.err
	}
	k := max(2*n, 10)
	res, err := ix.col.Query(ctx, query, min(k, ix.col.Count()), nil, nil)
	if err != nil {
		return nil, err
	}
	semantic := make([]string, len(res))
	for i, r := range res {
		semantic[i] = r.ID
	}
	keyword := ix.kw.search(query, k)

	scores, _ := rrfScores(semantic, keyword)
	ids := fuse(semantic, keyword)
	hits := make([]Hit, 0, n)
	for _, id := range ids[:min(n, len(ids))] {
		hits = append(hits, Hit{Source: ix.meta[id]["source"], Heading: ix.meta[id]["heading"], Text: ix.texts[id], Score: float32(scores[id])})
	}
	return hits, nil
}
