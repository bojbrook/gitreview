package pr

import (
	"context"
	"testing"
)

func TestFetchReviewComments(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchReviewComments(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("count: got %d want 2", len(got))
	}
	if got[0].User != "alice" || got[0].Path != "src/a.go" || got[0].Line != 12 || got[0].Side != "RIGHT" {
		t.Errorf("comment 0: got %+v", got[0])
	}
	if got[1].InReplyTo != 1001 {
		t.Errorf("comment 1 reply: got %d want 1001", got[1].InReplyTo)
	}
}

func TestFetchIssueComments(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchIssueComments(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].User != "carol" {
		t.Errorf("got %+v", got)
	}
}

func TestFetchReviews(t *testing.T) {
	_, base := startMockGitHub(t)
	c, err := newClient("testtoken", base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fetchReviews(context.Background(), c, "foo", "bar", 89)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].State != "APPROVED" {
		t.Errorf("got %+v", got)
	}
}
