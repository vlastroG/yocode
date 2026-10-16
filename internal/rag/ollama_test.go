package rag

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestEnsureModelPullsOnlyWhenMissing(t *testing.T) {
	var pulled []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			io.WriteString(w, `{"models":[{"name":"bge-m3:latest"}]}`)
		case "/api/pull":
			var b struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			pulled = append(pulled, b.Model)
			io.WriteString(w, `{"status":"success"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	if err := EnsureModel(ctx, srv.URL, "bge-m3", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := EnsureModel(ctx, srv.URL, "nomic-embed-text", io.Discard); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pulled, []string{"nomic-embed-text"}) {
		t.Errorf("pulled = %v", pulled)
	}
}
