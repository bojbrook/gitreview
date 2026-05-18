package pr

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	c, err := openCache(t.TempDir(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	in := map[string]any{"hello": "world", "n": float64(7)}
	if err := c.write("test.json", in); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out map[string]any
	if !c.read("test.json", &out) {
		t.Fatal("read returned false on fresh hit")
	}
	if out["hello"] != "world" || out["n"] != float64(7) {
		t.Errorf("round-trip mismatch: got %v", out)
	}
}

func TestCacheMissReturnsFalse(t *testing.T) {
	c, _ := openCache(t.TempDir(), "o", "r", 1)
	var out map[string]any
	if c.read("missing.json", &out) {
		t.Error("read on missing file should return false")
	}
}

func TestCacheStaleReturnsFalse(t *testing.T) {
	c, _ := openCache(t.TempDir(), "o", "r", 1)
	c.ttl = 25 * time.Millisecond
	if err := c.write("t.json", "x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	var out string
	if c.read("t.json", &out) {
		t.Error("stale entry: read should return false")
	}
}

func TestCacheCorruptReturnsFalse(t *testing.T) {
	c, _ := openCache(t.TempDir(), "o", "r", 1)
	if err := os.WriteFile(filepath.Join(c.dir, "bad.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if c.read("bad.json", &out) {
		t.Error("corrupt JSON: read should return false")
	}
}

func TestCacheClearRemovesEntries(t *testing.T) {
	c, _ := openCache(t.TempDir(), "o", "r", 1)
	_ = c.write("a.json", "x")
	_ = c.write("b.json", "y")
	if err := c.clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	var out string
	if c.read("a.json", &out) || c.read("b.json", &out) {
		t.Error("after clear: entries should be gone")
	}
}

func TestCacheNilReceiverIsSafe(t *testing.T) {
	var c *cache
	var out string
	if c.read("x", &out) {
		t.Error("nil read should return false")
	}
	if err := c.write("x", "y"); err != nil {
		t.Errorf("nil write: %v", err)
	}
	if err := c.clear(); err != nil {
		t.Errorf("nil clear: %v", err)
	}
}

func TestCacheEmptyStateDirYieldsNil(t *testing.T) {
	c, err := openCache("", "o", "r", 1)
	if err != nil {
		t.Fatalf("openCache empty stateDir: %v", err)
	}
	if c != nil {
		t.Error("empty stateDir should return nil cache")
	}
}

func TestCacheNoTempFilesLeft(t *testing.T) {
	c, _ := openCache(t.TempDir(), "o", "r", 1)
	if err := c.write("a.json", "x"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.json" {
		t.Errorf("expected only a.json, got %v", entries)
	}
}
