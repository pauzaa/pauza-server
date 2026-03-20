package revenuecat

import (
	"sync"
	"time"
)

// ttlCache is a generic single-value in-memory cache with TTL expiration.
type ttlCache[T any] struct {
	mu    sync.Mutex
	value T
	set   bool
	exp   time.Time
	ttl   time.Duration
}

// newTTLCache creates a ttlCache with the given TTL.
func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl}
}

// Get returns the cached value and true if it exists and has not expired.
func (c *ttlCache[T]) Get() (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set || time.Now().After(c.exp) {
		var zero T
		return zero, false
	}
	return c.value, true
}

// Set stores a value in the cache with the configured TTL.
func (c *ttlCache[T]) Set(value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = value
	c.set = true
	c.exp = time.Now().Add(c.ttl)
}

// ttlCacheMap is a keyed in-memory cache where each entry has TTL expiration.
type ttlCacheMap[T any] struct {
	mu    sync.Mutex
	items map[string]*ttlCacheEntry[T]
	ttl   time.Duration
}

type ttlCacheEntry[T any] struct {
	value T
	exp   time.Time
}

// newTTLCacheMap creates a ttlCacheMap with the given TTL.
func newTTLCacheMap[T any](ttl time.Duration) *ttlCacheMap[T] {
	return &ttlCacheMap[T]{
		items: make(map[string]*ttlCacheEntry[T]),
		ttl:   ttl,
	}
}

// Get returns the cached value for the key and true if it exists and has not expired.
func (c *ttlCacheMap[T]) Get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok || time.Now().After(entry.exp) {
		var zero T
		return zero, false
	}
	return entry.value, true
}

// Set stores a value in the cache under the given key with the configured TTL.
func (c *ttlCacheMap[T]) Set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = &ttlCacheEntry[T]{
		value: value,
		exp:   time.Now().Add(c.ttl),
	}
}
