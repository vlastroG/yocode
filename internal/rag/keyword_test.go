package rag

import "testing"

func TestKeywordSearchRanksExactTerm(t *testing.T) {
	k := newKeywordIndex([]string{"a", "b", "c"}, []string{
		"install with docker compose", "FooBarClient retries on timeout", "the api returns json"})
	got := k.search("how does FooBarClient retry?", 3)
	if len(got) == 0 || got[0] != "b" {
		t.Errorf("got %v, want b first", got)
	}
	if len(k.search("zzz", 3)) != 0 {
		t.Error("unknown term must match nothing")
	}
}

func TestFuseRewardsAgreement(t *testing.T) {
	got := fuse([]string{"x", "y", "z"}, []string{"z", "q"})
	if got[0] != "z" {
		t.Errorf("got %v, want z (in both lists) first", got)
	}
}
