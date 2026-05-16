// Package infra provides infrastructure utilities: caching, circuit breakers, etc.
package infra

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// CacheEntry holds a cached value with expiration metadata.
type CacheEntry[T any] struct {
	Value     T
	ExpiresAt time.Time
}

// Cache is a generic TTL cache with bounded size.
type Cache[T any] struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry[T]
	ttl     time.Duration
	maxSize int
}

// NewCache creates a cache with the given TTL and max entries.
func NewCache[T any](ttl time.Duration, maxSize int) *Cache[T] {
	return &Cache[T]{
		entries: make(map[string]*CacheEntry[T], maxSize),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// Get retrieves a cached value. Returns the value and true if found and not expired.
func (c *Cache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		var zero T
		return zero, false
	}

	if time.Now().After(entry.ExpiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		var zero T
		return zero, false
	}

	return entry.Value, true
}

// Put stores a value in the cache. Evicts the oldest entry if at capacity.
func (c *Cache[T]) Put(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.evictExpired(now)
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[key] = &CacheEntry[T]{
		Value:     value,
		ExpiresAt: now.Add(c.ttl),
	}
}

// evictExpired removes every entry whose ExpiresAt is before now. Caller must
// hold c.mu.
func (c *Cache[T]) evictExpired(now time.Time) {
	for k, e := range c.entries {
		if now.After(e.ExpiresAt) {
			delete(c.entries, k)
		}
	}
}

// evictOldest removes the entry closest to expiration. Caller must hold c.mu.
func (c *Cache[T]) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, e := range c.entries {
		if first || e.ExpiresAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.ExpiresAt
			first = false
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// Invalidate removes a specific key from the cache.
func (c *Cache[T]) Invalidate(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// Clear removes all entries from the cache.
func (c *Cache[T]) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]*CacheEntry[T], c.maxSize)
	c.mu.Unlock()
}

// Len returns the number of entries (including possibly expired ones).
func (c *Cache[T]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// CacheKey builds a deterministic key from a path and options hash.
func CacheKey(absPath string, optsString string) string {
	h := sha256.Sum256([]byte(absPath + "|" + optsString))
	return fmt.Sprintf("%x", h[:16])
}
