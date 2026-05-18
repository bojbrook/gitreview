package pr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubmit_HappyPath(t *testing.T) {
	var capturedBody []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/reviews", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 5000})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := newClient("testtoken", srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	err = Submit(context.Background(), c, "foo", "bar", 89, "overall LGTM", []SubmitDraft{
		{Path: "src/a.go", Line: 12, Side: "RIGHT", Body: "nit"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(capturedBody, &got); err != nil {
		t.Fatalf("body unmarshal: %v\nraw: %s", err, capturedBody)
	}
	if got["event"] != "COMMENT" {
		t.Errorf("event: got %v want COMMENT", got["event"])
	}
	if got["body"] != "overall LGTM" {
		t.Errorf("body: got %v want overall LGTM", got["body"])
	}
	comments, _ := got["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("comments count: got %d want 1", len(comments))
	}
	c0, _ := comments[0].(map[string]any)
	if c0["path"] != "src/a.go" || c0["body"] != "nit" {
		t.Errorf("comment 0: got %+v", c0)
	}
	if c0["line"].(float64) != 12 || c0["side"] != "RIGHT" {
		t.Errorf("comment 0 anchor: got %+v", c0)
	}
}

func TestSubmit_RejectsEmptyAndNoBody(t *testing.T) {
	err := Submit(context.Background(), nil, "foo", "bar", 89, "", nil)
	if err == nil {
		t.Error("Submit with no drafts and no body should error")
	}
}

func TestSubmit_PostError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/foo/bar/pulls/89/reviews", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Resource not accessible by integration"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := newClient("t", srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	err = Submit(context.Background(), c, "foo", "bar", 89, "", []SubmitDraft{{Path: "a", Line: 1, Side: "RIGHT", Body: "x"}})
	if err == nil {
		t.Fatal("want error on 403")
	}
}
