package pr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func startMockGitHub(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   89,
			"title":    "Add caching",
			"body":     "Speeds up repeat lookups.",
			"state":    "open",
			"merged":   false,
			"html_url": "https://github.com/foo/bar/pull/89",
			"user":     map[string]any{"login": "alice"},
			"head":     map[string]any{"sha": "abc1234deadbeef"},
			"base":     map[string]any{"sha": "11112222ffffaaaa"},
		})
	})

	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"filename": "src/a.go",
				"status":   "modified",
				"patch":    "@@ -1,3 +1,4 @@\n package src\n \n-func Old() {}\n+func New() {}\n+func Extra() {}",
			},
			{
				"filename": "src/b.go",
				"status":   "added",
				"patch":    "@@ -0,0 +1,2 @@\n+package src\n+\n",
			},
		})
	})

	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/commits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"sha":    "abc1234deadbeef",
				"commit": map[string]any{"message": "Add caching\n\nFirst implementation."},
			},
		})
	})

	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":   1001,
				"user": map[string]any{"login": "alice"},
				"path": "src/a.go",
				"line": 12,
				"side": "RIGHT",
				"body": "can we add a context timeout?",
			},
			{
				"id":             1002,
				"user":           map[string]any{"login": "bob"},
				"path":           "src/a.go",
				"line":           12,
				"side":           "RIGHT",
				"body":           "yeah +1",
				"in_reply_to_id": 1001,
			},
		})
	})

	mux.HandleFunc("/api/v3/repos/foo/bar/issues/89/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":   2001,
				"user": map[string]any{"login": "carol"},
				"body": "Looks good overall, one nit below.",
			},
		})
	})

	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/reviews", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":    3001,
				"user":  map[string]any{"login": "carol"},
				"body":  "LGTM",
				"state": "APPROVED",
			},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.URL + "/"
}

func TestFetchPR(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := fetchPR(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if pr.GetNumber() != 89 || pr.GetTitle() != "Add caching" {
		t.Errorf("PR fields: got %+v", pr)
	}
}

func TestFetchFilesAndToDiff(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fetchFiles(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("file count: got %d want 2", len(files))
	}
	d, err := toDiff(files, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 2 {
		t.Fatalf("diff files: got %d want 2", len(d.Files))
	}
	if d.Files[0].Path != "src/a.go" {
		t.Errorf("file 0 path: got %q want src/a.go", d.Files[0].Path)
	}
	if d.Files[1].Path != "src/b.go" {
		t.Errorf("file 1 path: got %q", d.Files[1].Path)
	}
}

func TestFetchCommitsAndToCommits(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fetchCommits(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	cs := toCommits(raw)
	if len(cs) != 1 {
		t.Fatalf("commit count: got %d", len(cs))
	}
	if cs[0].Subject != "Add caching" {
		t.Errorf("subject: got %q", cs[0].Subject)
	}
	if !strings.Contains(cs[0].Body, "First implementation") {
		t.Errorf("body: got %q", cs[0].Body)
	}
}
