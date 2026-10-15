package rag

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// keywordIndex is a small in-memory BM25-like index over chunk texts.
type keywordIndex struct {
	ids []string
	tf  []map[string]int
	df  map[string]int
}

func newKeywordIndex(ids, texts []string) *keywordIndex {
	k := &keywordIndex{ids: ids, tf: make([]map[string]int, len(ids)), df: map[string]int{}}
	for i, text := range texts {
		k.tf[i] = map[string]int{}
		for _, t := range tokenize(text) {
			if k.tf[i][t] == 0 {
				k.df[t]++
			}
			k.tf[i][t]++
		}
	}
	return k
}

// search returns up to n ids with a positive score, best first.
func (k *keywordIndex) search(query string, n int) []string {
	terms := map[string]bool{}
	for _, t := range tokenize(query) {
		terms[t] = true
	}
	type scored struct {
		id    string
		score float64
	}
	var res []scored
	N := float64(len(k.ids))
	for i, id := range k.ids {
		var s float64
		for t := range terms {
			tf := float64(k.tf[i][t])
			if tf == 0 {
				continue
			}
			df := float64(k.df[t])
			s += math.Log(1+(N-df+0.5)/(df+0.5)) * tf / (tf + 1.2)
		}
		if s > 0 {
			res = append(res, scored{id, s})
		}
	}
	sort.SliceStable(res, func(a, b int) bool { return res[a].score > res[b].score })
	if len(res) > n {
		res = res[:n]
	}
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.id
	}
	return out
}

func tokenize(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	out := f[:0]
	for _, t := range f {
		if len([]rune(t)) >= 2 {
			out = append(out, t)
		}
	}
	return out
}

// rrfScores are reciprocal rank fusion scores, keyed by id.
func rrfScores(lists ...[]string) (scores map[string]float64, order []string) {
	scores = map[string]float64{}
	for _, l := range lists {
		for rank, id := range l {
			if _, seen := scores[id]; !seen {
				order = append(order, id)
			}
			scores[id] += 1 / float64(60+rank+1)
		}
	}
	return scores, order
}

// fuse merges ranked id lists by reciprocal rank fusion. Ties keep the earlier first appearance.
func fuse(lists ...[]string) []string {
	scores, order := rrfScores(lists...)
	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })
	return order
}
