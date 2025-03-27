package pcache

import (
	"context"
	"sync"
	"time"

	"github.com/pixie-sh/logger-go/logger"
)

// Driver is the interface that cache implementations must satisfy
type Driver[T any] interface {
	// Set stores a value in the cache with an expiration time
	Set(ctx context.Context, key string, value T, expiration time.Duration) error

	// Get retrieves a value from the cache
	Get(ctx context.Context, key string) (T, bool, error)

	// Delete removes a value from the cache
	Delete(ctx context.Context, key string) error

	// GetExpiration returns when a key will expire
	GetExpiration(ctx context.Context, key string) (time.Time, bool, error)

	// Keys returns all keys in the cache (used for cleanup)
	Keys(ctx context.Context) ([]string, error)
}

// Request represents a request to get data from cache
type Request[T any] struct {
	Ctx          context.Context
	Key          string
	ResponseChan chan Response[T]
}

// Response represents the response to a cache request
type Response[T any] struct {
	Value     T
	CacheMiss bool
	Error     error
}

// SetRequest represents a request to save data to cache
type SetRequest[T any] struct {
	Ctx          context.Context
	Key          string
	Value        T
	Expiration   time.Duration
	ResponseChan chan SetResponse[T]
}

// SetResponse represents the response to a save data to cache request
type SetResponse[T any] struct {
	Ctx        context.Context
	Key        string
	Value      T
	Expiration time.Duration
	Error      error
}

// UpdateFunc is the function signature for updating expired cache entries
type UpdateFunc[T any] func(key string) (T, time.Duration, error)

// Service manages caching operations using channels
type Service[T any] struct {
	provider      Driver[T]
	getChannel    chan Request[T]
	saveChannel   chan SetRequest[T]
	updateFunc    UpdateFunc[T]
	cleanupTicker *time.Ticker
	wg            sync.WaitGroup
	quit          chan struct{}
	config        ServiceConfiguration
}

type ServiceConfiguration struct {
	CleanupInterval   time.Duration
	UpdateOnCacheMiss bool
	UpdateOnGetError  bool
}

// NewService creates a new cache service with the given provider and update function
func NewService[T any](provider Driver[T], updateFunc UpdateFunc[T], config ServiceConfiguration) *Service[T] {
	service := &Service[T]{
		provider:      provider,
		getChannel:    make(chan Request[T]),
		saveChannel:   make(chan SetRequest[T], 100), // Buffer for async operations
		updateFunc:    updateFunc,
		cleanupTicker: time.NewTicker(config.CleanupInterval),
		quit:          make(chan struct{}),
		config:        config,
	}

	return service
}

// StartAsync starts worker in go routine
func (cs *Service[T]) StartAsync(ctx context.Context) {
	go func() {
		err := cs.worker(ctx)
		if err != nil {
			logger.Logger.
				With("error", err).
				Error("error starting cache service")
		}
	}()
}

// Start lockable call
func (cs *Service[T]) Start(ctx context.Context) error {
	return cs.worker(ctx)
}

// GetChan returns the channel for retrieving cache values
func (cs *Service[T]) GetChan() chan<- Request[T] {
	return cs.getChannel
}

// SetChan returns the channel for saving cache values
func (cs *Service[T]) SetChan() chan<- SetRequest[T] {
	return cs.saveChannel
}

// Get retrieves a value from the cache using the specified key
func (cs *Service[T]) Get(key string) (T, bool, error) {
	respChan := make(chan Response[T])
	cs.GetChan() <- Request[T]{
		Key:          key,
		ResponseChan: respChan,
	}

	resp := <-respChan
	return resp.Value, resp.CacheMiss, resp.Error
}

// Set writes a value into the cache using the specified key and expiration time
func (cs *Service[T]) Set(ctx context.Context, key string, val T, expiration time.Duration) (string, T, time.Duration, error) {
	respChan := make(chan SetResponse[T])
	cs.saveChannel <- SetRequest[T]{
		Ctx:          ctx,
		Key:          key,
		Value:        val,
		Expiration:   expiration,
		ResponseChan: respChan,
	}

	resp := <-respChan
	return resp.Key, resp.Value, resp.Expiration, resp.Error
}

// Close gracefully shuts down the cache service
func (cs *Service[T]) Close() {
	close(cs.quit)
	cs.wg.Wait()
	cs.cleanupTicker.Stop()
}

// worker synchronize processes requests from the channels
func (cs *Service[T]) worker(ctx context.Context) error {
	cs.wg.Add(1)
	defer cs.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-cs.quit:
			return nil
		case <-cs.cleanupTicker.C:
			cs.checkExpiredEntries(ctx)
		case request := <-cs.saveChannel:
			cs.handleSet(ctx, request)
		case request := <-cs.getChannel:
			cs.handleGet(ctx, request)
		}
	}
}

// checkExpiredEntries refreshes expired entries using the update function
func (cs *Service[T]) checkExpiredEntries(ctx context.Context) {
	keys, err := cs.provider.Keys(ctx)
	if err != nil {
		return
	}

	now := time.Now()
	for _, key := range keys {
		expiration, exists, err := cs.provider.GetExpiration(ctx, key)
		if err != nil || !exists {
			continue
		}

		if now.After(expiration) {
			newValue, newExpiration, err := cs.updateFunc(key)
			if err != nil {
				err := cs.provider.Delete(ctx, key)
				if err != nil {
					logger.Logger.
						With("error", err).
						Error("Error deleting expired key: %s, error: %v", key, err)
				}
			} else {
				err = cs.provider.Set(ctx, key, newValue, newExpiration)
				if err != nil {
					logger.Logger.
						With("error", err).
						Error("Error updating expired key: %s, error: %v", key, err)
				}
			}
		}
	}
}

// handleGet processes a request to retrieve a value from the cache.
// If the value is unavailable or an error occurs, it updates the cache
// with the help of the configured update function, if enabled in the configuration.
func (cs *Service[T]) handleGet(_ context.Context, request Request[T]) {
	var response Response[T]
	var value T
	var err error
	var exists bool

	value, exists, err = cs.provider.Get(request.Ctx, request.Key)
	response.CacheMiss = !exists
	response.Error = err

	if (err != nil && cs.config.UpdateOnGetError) || (!exists && cs.config.UpdateOnCacheMiss) {
		var duration time.Duration

		value, duration, err = cs.updateFunc(request.Key)
		if err != nil {
			response.Error = err
		}

		err = cs.provider.Set(request.Ctx, request.Key, value, duration)
		if err != nil {
			response.Error = err
		}
	}

	response.Value = value
	request.ResponseChan <- response
}

// handleSet processes a request to save a value into the cache.
// It uses the provided cache key, value, and expiration duration.
// If a response channel is provided, it sends a SetResponse containing the outcome of the operation.
func (cs *Service[T]) handleSet(ctx context.Context, request SetRequest[T]) {
	err := cs.provider.Set(ctx, request.Key, request.Value, request.Expiration)
	if request.ResponseChan != nil {
		request.ResponseChan <- SetResponse[T]{
			Ctx:        request.Ctx,
			Key:        request.Key,
			Value:      request.Value,
			Expiration: request.Expiration,
			Error:      err,
		}
	}
}
