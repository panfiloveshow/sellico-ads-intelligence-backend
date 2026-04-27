package wb

import (
	"container/list"
	"sync"
	"time"
)

// boundedLRU is a thread-safe least-recently-used cache with a max size and
// a per-entry time-to-live. It avoids the unbounded growth of a plain
// map[string]V — which is what was leaking memory in Client.limiters and
// Client.breakers when each new WB token created a permanent entry.
//
// Eviction policy:
//   - On Get: if the entry exists but its lastAccess + ttl is in the past,
//     it is treated as missing (lazy expiration).
//   - On Set: if at capacity and the key is new, the LRU tail is evicted.
//
// Trade-offs:
//   - rate.Limiter and circuit-breaker state is lost on eviction. For limiters
//     this is fine — the new bucket starts full, which matches what an idle
//     token expects. For circuit breakers, evicting an "open" breaker means
//     we re-test the upstream; given the TTL (1h) the upstream may have
//     recovered anyway, so the lost state is rarely load-bearing.
//   - No janitor goroutine: idle entries persist in memory until the next
//     Set call evicts them. This keeps the cache dependency-free and
//     trivially shut-downable.
type boundedLRU[V any] struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	items map[string]*list.Element
	order *list.List // front = most recent, back = least recent
}

type lruEntry[V any] struct {
	key        string
	value      V
	lastAccess time.Time
}

func newBoundedLRU[V any](capacity int, ttl time.Duration) *boundedLRU[V] {
	if capacity <= 0 {
		capacity = 1
	}
	return &boundedLRU[V]{
		cap:   capacity,
		ttl:   ttl,
		items: make(map[string]*list.Element, capacity),
		order: list.New(),
	}
}

// Get returns the value for key and reports whether it was present and not
// expired. A hit moves the entry to the front of the LRU list.
func (c *boundedLRU[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	entry := el.Value.(*lruEntry[V])
	if c.ttl > 0 && time.Since(entry.lastAccess) > c.ttl {
		c.removeElement(el)
		return zero, false
	}
	entry.lastAccess = time.Now()
	c.order.MoveToFront(el)
	return entry.value, true
}

// Set inserts or replaces the value for key. If insertion exceeds capacity,
// the least-recently-used entry is evicted.
func (c *boundedLRU[V]) Set(key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		entry := el.Value.(*lruEntry[V])
		entry.value = value
		entry.lastAccess = time.Now()
		c.order.MoveToFront(el)
		return
	}
	entry := &lruEntry[V]{key: key, value: value, lastAccess: time.Now()}
	el := c.order.PushFront(entry)
	c.items[key] = el
	if c.order.Len() > c.cap {
		c.removeElement(c.order.Back())
	}
}

// Len returns the current number of entries (including any expired ones not
// yet swept by a Get).
func (c *boundedLRU[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func (c *boundedLRU[V]) removeElement(el *list.Element) {
	if el == nil {
		return
	}
	entry := el.Value.(*lruEntry[V])
	c.order.Remove(el)
	delete(c.items, entry.key)
}
