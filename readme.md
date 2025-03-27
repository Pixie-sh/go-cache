# pcache - Pixie Cache

A high-performance, concurrent, and generic caching solution for Go applications.

## Overview

pcache provides a channel-based caching system with support for automatic refreshing of expired entries.
It's designed to be used in high-concurrency environments and leverages Go's type parameters for type safety.

## Architecture

```mermaid
classDiagram
    class Driver~T~ {
        <<interface>>
        +Set(ctx, key, value, expiration) error
        +Get(ctx, key) (T, bool, error)
        +Delete(ctx, key) error
        +GetExpiration(ctx, key) (time.Time, bool, error)
        +Keys(ctx) ([]string, error)
    }

    class CacheService~T~ {
        -provider Driver~T~
        -getChannel chan CacheRequest~T~
        -saveChannel chan CacheSaveRequest~T~
        -updateFunc UpdateFunc~T~
        -cleanupTicker *time.Ticker
        -wg sync.WaitGroup
        -quit chan struct{}
        +GetChannel() chan<- CacheRequest~T~
        +SaveChannel() chan<- CacheSaveRequest~T~
        -worker()
        -cleanupWorker()
        -checkExpiredEntries()
        +StartAsync(ctx)
        +Close()
    }

    class MemoryCache~T~ {
        -data map[string]MemoryCacheEntry~T~
        -mutex sync.RWMutex
        +Set(ctx, key, value, expiration) error
        +Get(ctx, key) (T, bool, error)
        +Delete(ctx, key) error
        +GetExpiration(ctx, key) (time.Time, bool, error)
        +Keys(ctx) ([]string, error)
    }

    class MemoryCacheEntry~T~ {
        +Value T
        +Expiration time.Time
    }

    class CacheRequest~T~ {
        +Key string
        +Response chan CacheResponse~T~
    }

    class CacheResponse~T~ {
        +Value T
        +Found bool
        +Error error
    }

    class CacheSaveRequest~T~ {
        +Key string
        +Value T
        +Expiration time.Duration
    }

    %% Type definitions
    class UpdateFunc~T~ {
        <<typedef>>
        function(key string) (T, time.Duration, error)
    }

    %% Relationships
    Driver <|.. MemoryCache : implements
    CacheService o-- Driver : uses
    MemoryCache *-- MemoryCacheEntry : contains
    CacheService --> CacheRequest : processes
    CacheService --> CacheSaveRequest : processes
    CacheRequest --> CacheResponse : returns via channel
    CacheService --> UpdateFunc : uses for updates

    %% Functions
    NewMemoryCache~T~ ..> MemoryCache : creates
    NewCacheService~T~ ..> CacheService : creates
```

## Key Components

### Driver Interface

The core abstraction that defines the operations a cache provider must implement:

```go

type Driver[T any] interface {
    Set(ctx context.Context, key string, value T, expiration time.Duration) error
    Get(ctx context.Context, key string) (T, bool, error)
    Delete(ctx context.Context, key string) error
    GetExpiration(ctx context.Context, key string) (time.Time, bool, error)
    Keys(ctx context.Context) ([]string, error)
}
```

### MemoryCache Implementation

An in-memory implementation of the Driver interface that stores values with their expiration times.

### CacheService

Provides a high-level interface for cache operations through channels:

- Asynchronous processing of get/set operations
- Automatic refresh of expired entries
- Periodic cleanup of expired entries

## Usage

### Creating a Cache Service

```go
import (
    "context"
    "time"
    "github.com/pixie-sh/pcache"
)

// Define an update function to refresh expired/missing entries
updateFunc := func(key string) (string, time.Duration, error) {
    // Fetch data from source when cache misses
    return "fresh value", 5*time.Minute, nil
}

// Create a memory cache provider
memCache := pcache.NewMemoryCache[string]()

// Create and start the cache service
cacheService := pcache.NewCacheService[string](
    memCache,
    updateFunc,
    1*time.Minute,  // Cleanup interval
)

ctx := context.Background()
cacheService.StartAsync(ctx)
defer cacheService.Close()
```

### Tests

````
Starting concurrency tests...

=== Running Low Concurrency Test ===

Results for Low Concurrency:
- Total operations: 88412
- Get operations: 53194 (60.2%)
- Save operations: 35218 (39.8%)
- Errors: 0 (0.0%)
- Operations per second: 17682.40
- Key statistics:
  - Unique keys accessed: 50
  - Average accesses per key: 1768.24
  - Min accesses to a key: 1668
  - Max accesses to a key: 1883
- Latency:
  - Average: 1.604µs
  - Min: 83ns
  - Max: 292.375µs

=== Running Medium Concurrency Test ===

Results for Medium Concurrency:
- Total operations: 429861
- Get operations: 258165 (60.1%)
- Save operations: 171696 (39.9%)
- Errors: 0 (0.0%)
- Operations per second: 85972.20
- Key statistics:
  - Unique keys accessed: 50
  - Average accesses per key: 8597.22
  - Min accesses to a key: 8384
  - Max accesses to a key: 8816
- Latency:
  - Average: 30.045µs
  - Min: 166ns
  - Max: 6.011709ms

=== Running High Concurrency Test ===

Results for High Concurrency:
- Total operations: 889292
- Get operations: 532831 (59.9%)
- Save operations: 356461 (40.1%)
- Errors: 0 (0.0%)
- Operations per second: 177858.40
- Key statistics:
  - Unique keys accessed: 50
  - Average accesses per key: 17785.84
  - Min accesses to a key: 17561
  - Max accesses to a key: 18155
- Latency:
  - Average: 30.504µs
  - Min: 125ns
  - Max: 9.897542ms

=== Running Very High Concurrency Test ===

Results for Very High Concurrency:
- Total operations: 1852496
- Get operations: 1112652 (60.1%)
- Save operations: 739844 (39.9%)
- Errors: 0 (0.0%)
- Operations per second: 370499.20
- Key statistics:
  - Unique keys accessed: 50
  - Average accesses per key: 37049.92
  - Min accesses to a key: 36617
  - Max accesses to a key: 37737
- Latency:
  - Average: 26.36µs
  - Min: 42ns
  - Max: 3.742625ms

=== Running Narrow Key Space Test ===

Results for Narrow Key Space:
- Total operations: 848292
- Get operations: 508834 (60.0%)
- Save operations: 339458 (40.0%)
- Errors: 0 (0.0%)
- Operations per second: 169658.40
- Key statistics:
  - Unique keys accessed: 10
  - Average accesses per key: 84829.20
  - Min accesses to a key: 84374
  - Max accesses to a key: 85289
- Latency:
  - Average: 57.424µs
  - Min: 125ns
  - Max: 72.810667ms

=== Running Wide Key Space Test ===

Results for Wide Key Space:
- Total operations: 889458
- Get operations: 533493 (60.0%)
- Save operations: 355965 (40.0%)
- Errors: 0 (0.0%)
- Operations per second: 177891.60
- Key statistics:
  - Unique keys accessed: 1000
  - Average accesses per key: 889.46
  - Min accesses to a key: 788
  - Max accesses to a key: 988
- Latency:
  - Average: 30.315µs
  - Min: 125ns
  - Max: 4.815625ms

All concurrency tests completed!
````