package pcache

import (
	"context"
	"time"
)

// NotSafeMemoryCacheEntry represents a value stored in memory cache
type NotSafeMemoryCacheEntry[T any] struct {
	Value      T
	Expiration time.Time
}

// NotSafeMemoryCache implements Driver using in-memory storage
type NotSafeMemoryCache[T any] struct {
	data map[string]NotSafeMemoryCacheEntry[T]
}

// NewMemoryCache creates a new in-memory cache provider
func NewMemoryCache[T any]() *NotSafeMemoryCache[T] {
	return &NotSafeMemoryCache[T]{
		data: make(map[string]NotSafeMemoryCacheEntry[T]),
	}
}

// Set stores a value in the cache with an expiration time
func (mc *NotSafeMemoryCache[T]) Set(_ context.Context, key string, value T, expiration time.Duration) error {
	var expirationTime time.Time
	if expiration > 0 {
		expirationTime = time.Now().UTC().Add(expiration)
	}

	mc.data[key] = NotSafeMemoryCacheEntry[T]{
		Value:      value,
		Expiration: expirationTime,
	}

	return nil
}

// Get retrieves a value from the cache
func (mc *NotSafeMemoryCache[T]) Get(_ context.Context, key string) (T, bool, error) {
	entry, exists := mc.data[key]
	if !exists {
		var zero T
		return zero, false, nil
	}

	// Check if the entry has expired
	if !entry.Expiration.IsZero() && time.Now().After(entry.Expiration) {
		var zero T
		return zero, false, nil
	}

	return entry.Value, true, nil
}

// Delete removes a value from the cache
func (mc *NotSafeMemoryCache[T]) Delete(_ context.Context, key string) error {
	delete(mc.data, key)
	return nil
}

// GetExpiration returns when a key will expire
func (mc *NotSafeMemoryCache[T]) GetExpiration(_ context.Context, key string) (time.Time, bool, error) {
	entry, exists := mc.data[key]
	if !exists {
		return time.Time{}, false, nil
	}

	return entry.Expiration, true, nil
}

// Keys returns all keys in the cache
func (mc *NotSafeMemoryCache[T]) Keys(_ context.Context) ([]string, error) {
	keys := make([]string, 0, len(mc.data))
	for key := range mc.data {
		keys = append(keys, key)
	}

	return keys, nil
}
