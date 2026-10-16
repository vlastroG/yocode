package jev

import (
	"context"
	"fmt"
	"sort"

	"github.com/vlastroG/yocode/internal/rag"
)

const (
	defaultShortlist    = 20
	defaultMinRelevance = 0.5
	maxCandidateChars   = 3000 // keeps a 20-chunk request well inside the 32k-token per-question limit
)

type Retriever interface {
	Search(ctx context.Context, query string, n int) ([]rag.Hit, error)
}

// Reranker takes a wider shortlist from Inner and keeps the chunks Jev judges relevant to the query,
// most relevant first. Fewer than n hits, or none, come back when the rest don't help.
type Reranker struct {
	Inner        Retriever
	Jev          *Client
	Shortlist    int     // candidates fetched from Inner; 0 means 20
	MinRelevance float64 // Noul a chunk needs to be kept; 0 means 0.5
	// OnError, if set, hears about Jev failures; Search then falls back to Inner's top n.
	OnError func(error)
}

func (r *Reranker) Search(ctx context.Context, query string, n int) ([]rag.Hit, error) {
	shortlist := r.Shortlist
	if shortlist <= 0 {
		shortlist = defaultShortlist
	}
	hits, err := r.Inner.Search(ctx, query, max(n, shortlist))
	if err != nil || len(hits) == 0 {
		return hits, err
	}
	scores, err := r.relevance(ctx, query, hits)
	if err != nil {
		if r.OnError != nil {
			r.OnError(err)
		}
		return hits[:min(n, len(hits))], nil
	}
	threshold := r.MinRelevance
	if threshold <= 0 {
		threshold = defaultMinRelevance
	}
	var kept []rag.Hit
	for i, h := range hits {
		if scores[i] >= threshold {
			h.Score = float32(scores[i])
			kept = append(kept, h)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Score > kept[j].Score })
	return kept[:min(n, len(kept))], nil
}

// relevance asks one Noul per candidate in a single request.
func (r *Reranker) relevance(ctx context.Context, query string, hits []rag.Hit) ([]float64, error) {
	candidates := make([]string, len(hits))
	questions := make(map[string]Question, len(hits))
	for i, h := range hits {
		text := h.Text
		if len(text) > maxCandidateChars {
			text = text[:maxCandidateChars]
		}
		candidates[i] = text
		questions[fmt.Sprint(i)] = Question{
			Type: "noul",
			Instructions: fmt.Sprintf("Does the documentation excerpt `candidates[%d]` contain information that "+
				"helps answer or carry out `query`?", i),
			Criteria: map[string]string{
				"true":  "It states facts, rules, steps or examples about what `query` asks or is about.",
				"false": "It is only on a loosely related topic, or doesn't help with `query` at all.",
			},
		}
	}
	answers, err := r.Jev.Ask(ctx, map[string]any{"query": query, "candidates": candidates}, questions)
	if err != nil {
		return nil, err
	}
	scores := make([]float64, len(hits))
	for i := range hits {
		a, ok := answers[fmt.Sprint(i)]
		if !ok {
			return nil, fmt.Errorf("jev: no answer for candidate %d", i)
		}
		scores[i] = a.Noul
	}
	return scores, nil
}
