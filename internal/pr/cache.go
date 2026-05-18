package pr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const defaultCacheTTL = 60 * time.Second

// cache is an on-disk JSON cache for one PR's GitHub API responses.
// Directory layout: <stateDir>/pr/<owner>-<repo>-<num>/<name>.
// Freshness is determined by file mtime + ttl.
type cache struct {
	dir string
	ttl time.Duration
}

// openCache returns a cache rooted at <stateDir>/pr/<owner>-<repo>-<num>/.
// Creates the directory if missing. Returns (nil, nil) when stateDir is
// empty so callers can opt out of caching by passing "".
func openCache(stateDir, owner, repo string, num int) (*cache, error) {
	if stateDir == "" {
		return nil, nil
	}
	dir := filepath.Join(stateDir, "pr", fmt.Sprintf("%s-%s-%d", owner, repo, num))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache mkdir: %w", err)
	}
	return &cache{dir: dir, ttl: defaultCacheTTL}, nil
}

// read attempts to load name into dst. Returns true only on a fresh,
// well-formed hit; misses, stale entries, and unmarshal errors all return
// false (and never surface an error — the caller refetches).
func (c *cache) read(name string, dst any) bool {
	if c == nil {
		return false
	}
	p := filepath.Join(c.dir, name)
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) > c.ttl {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

// write atomically replaces name with the JSON encoding of src via a
// temp file + rename. Nil receiver is a no-op for caller convenience.
func (c *cache) write(name string, src any) error {
	if c == nil {
		return nil
	}
	b, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("cache marshal %s: %w", name, err)
	}
	final := filepath.Join(c.dir, name)
	tmp, err := os.CreateTemp(c.dir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("cache temp %s: %w", name, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("cache write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("cache close %s: %w", name, err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("cache rename %s: %w", name, err)
	}
	return nil
}

// clear removes the cached entries for this PR. Missing dir is not an error.
func (c *cache) clear() error {
	if c == nil {
		return nil
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(c.dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
