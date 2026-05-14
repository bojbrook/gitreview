package ctxpane

import "testing"

func TestLRU_PutGet(t *testing.T) {
	c := newLRU(3)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Errorf("get a: got %v, %v", v, ok)
	}
}

func TestLRU_Eviction(t *testing.T) {
	c := newLRU(2)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3) // evicts "a"
	if _, ok := c.Get("a"); ok {
		t.Error("a should have been evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("b should still be present")
	}
}

func TestLRU_LRUOrdering(t *testing.T) {
	c := newLRU(2)
	c.Put("a", 1)
	c.Put("b", 2)
	_, _ = c.Get("a") // touches a
	c.Put("c", 3)     // should evict "b", not "a"
	if _, ok := c.Get("a"); !ok {
		t.Error("a should have been kept (most recently used)")
	}
	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted")
	}
}

func TestLRU_UpdateInPlace(t *testing.T) {
	c := newLRU(2)
	c.Put("a", 1)
	c.Put("a", 2) // overwrite, should not evict anything
	c.Put("b", 3)
	if v, ok := c.Get("a"); !ok || v != 2 {
		t.Errorf("get a after overwrite: got %v, %v", v, ok)
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("b should still be present after overwriting a")
	}
}
