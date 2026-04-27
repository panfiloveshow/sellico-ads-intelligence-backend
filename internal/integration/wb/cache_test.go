package wb

import (
	"strconv"
	"testing"
	"time"
)

func TestBoundedLRU_EvictsBeyondCapacity(t *testing.T) {
	c := newBoundedLRU[int](100, time.Hour)
	for i := 0; i < 10_000; i++ {
		c.Set(strconv.Itoa(i), i)
	}
	if got := c.Len(); got > 100 {
		t.Fatalf("expected size ≤ 100, got %d", got)
	}
	// First-inserted keys must be gone.
	if _, ok := c.Get("0"); ok {
		t.Fatal("key 0 should have been evicted")
	}
	// Most-recent keys must remain.
	if _, ok := c.Get("9999"); !ok {
		t.Fatal("key 9999 should still be present")
	}
}

func TestBoundedLRU_TTLExpires(t *testing.T) {
	c := newBoundedLRU[string](10, 10*time.Millisecond)
	c.Set("k", "v")
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Fatalf("expected hit, got ok=%v v=%q", ok, v)
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expected miss after TTL")
	}
	if c.Len() != 0 {
		t.Fatalf("expected size 0 after expiry, got %d", c.Len())
	}
}

func TestBoundedLRU_GetRefreshesRecency(t *testing.T) {
	c := newBoundedLRU[int](2, time.Hour)
	c.Set("a", 1)
	c.Set("b", 2)
	// Access "a" → "b" becomes LRU.
	_, _ = c.Get("a")
	c.Set("c", 3) // evicts "b"
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should still be present")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c should still be present")
	}
}

func TestBoundedLRU_SetUpdatesExisting(t *testing.T) {
	c := newBoundedLRU[int](3, time.Hour)
	c.Set("k", 1)
	c.Set("k", 2)
	if v, _ := c.Get("k"); v != 2 {
		t.Fatalf("expected value 2 after update, got %d", v)
	}
	if c.Len() != 1 {
		t.Fatalf("expected size 1 after update, got %d", c.Len())
	}
}
