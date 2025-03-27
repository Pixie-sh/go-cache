package pcache

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Define your cache value type
type UserData struct {
	ID      int
	Name    string
	Profile string
	Counter int // Used to verify data consistency
}

// TestResult tracks results of concurrent operations
type TestResult struct {
	// General stats
	totalOperations int64
	errors          int64

	// Specific operation counts
	getCount  int64
	saveCount int64

	// Data consistency tracking
	keyAccesses    map[string]int  // Tracks how many times each key was accessed
	keyConsistency map[string]bool // Tracks whether any key had consistency issues
	keyMutex       sync.RWMutex

	// Performance metrics
	operationLatencies []time.Duration
	latencyMutex       sync.Mutex
}

func TestConcurrency(t *testing.T) {
	// Set random seed
	rand.Seed(time.Now().UnixNano())

	// Create an update function for refreshing expired cache entries
	updateFunc := func(key string) (UserData, time.Duration, error) {
		// Simulate some work
		time.Sleep(5 * time.Millisecond)

		// Extract user ID from key for demonstration
		var userID int
		fmt.Sscanf(key, "user:%d", &userID)

		return UserData{
			ID:      userID,
			Name:    fmt.Sprintf("Updated User %d", userID),
			Profile: fmt.Sprintf("Updated Profile %d", userID),
			Counter: -1, // Special value to indicate it was updated via updateFunc
		}, 30 * time.Second, nil
	}

	// Create the memory cache provider
	memCache := NewMemoryCache[UserData]()

	// Create the cache service with 500ms cleanup interval
	cacheService := NewService[UserData](memCache, updateFunc, ServiceConfiguration{
		CleanupInterval:   500 * time.Millisecond,
		UpdateOnCacheMiss: false,
		UpdateOnGetError:  false,
	})
	defer cacheService.Close()
	cacheService.StartAsync(context.Background())

	fmt.Println("Starting concurrency tests...")

	// Run different concurrency test scenarios
	testScenarios := []struct {
		name        string
		concurrency int
		duration    time.Duration
		keySpace    int // Number of unique keys to use
	}{
		{"Low Concurrency", 10, 5 * time.Second, 50},
		{"Medium Concurrency", 50, 5 * time.Second, 50},
		{"High Concurrency", 100, 5 * time.Second, 50},
		{"Very High Concurrency", 200, 5 * time.Second, 50},
		{"Narrow Key Space", 100, 5 * time.Second, 10}, // Many threads competing for same keys
		{"Wide Key Space", 100, 5 * time.Second, 1000}, // Few collisions on keys
	}

	for _, scenario := range testScenarios {
		fmt.Printf("\n=== Running %s Test ===\n", scenario.name)
		result := runConcurrencyTest(cacheService, scenario.concurrency, scenario.duration, scenario.keySpace)
		printTestResults(scenario.name, result)
	}

	fmt.Println("\nAll concurrency tests completed!")
}

func runConcurrencyTest(
	cacheService *Service[UserData],
	concurrency int,
	duration time.Duration,
	keySpace int,
) *TestResult {
	// Create test result structure
	result := &TestResult{
		keyAccesses:    make(map[string]int),
		keyConsistency: make(map[string]bool),
	}

	// Create a wait group to track all goroutines
	var wg sync.WaitGroup

	// Signal channel to tell goroutines to stop
	done := make(chan struct{})

	// Counter for global consistency test
	counterMap := make(map[string]int)
	counterMutex := sync.Mutex{}

	// Launch worker goroutines
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Performance stat variables
			localOperations := 0

			for {
				select {
				case <-done:
					// Add local stats to global counters
					atomic.AddInt64(&result.totalOperations, int64(localOperations))
					return
				default:
					// Continue with work
					key := fmt.Sprintf("user:%d", rand.Intn(keySpace))

					// Decide operation: get or save (60% get, 40% save)
					if rand.Float32() < 0.6 {
						performConcurrentGet(cacheService, key, result, workerID)
					} else {
						// Get the current counter value for this key (for consistency checking)
						counterMutex.Lock()
						currentCounter := counterMap[key]
						// Increment the counter
						counterMap[key] = currentCounter + 1
						counterValue := currentCounter + 1
						counterMutex.Unlock()

						performConcurrentSave(cacheService, key, counterValue, result, workerID)
					}

					localOperations++

					// Small random delay to vary timing
					time.Sleep(time.Duration(rand.Intn(2)) * time.Millisecond)
				}
			}
		}(i)
	}

	// Run for specified duration then signal all goroutines to stop
	time.Sleep(duration)
	close(done)

	// Wait for all goroutines to complete
	wg.Wait()

	return result
}

func performConcurrentGet(
	cacheService *Service[UserData],
	key string,
	result *TestResult,
	workerID int,
) {
	// Track access for this key
	result.keyMutex.Lock()
	result.keyAccesses[key]++
	result.keyMutex.Unlock()

	// Create response channel
	respChan := make(chan Response[UserData])

	// Measure time
	start := time.Now()

	// Send get request
	cacheService.GetChan() <- Request[UserData]{
		Key:          key,
		ResponseChan: respChan,
	}

	// Wait for response
	resp := <-respChan

	// Measure latency
	elapsed := time.Since(start)

	// Track statistics
	atomic.AddInt64(&result.getCount, 1)

	// Track latency
	result.latencyMutex.Lock()
	result.operationLatencies = append(result.operationLatencies, elapsed)
	result.latencyMutex.Unlock()

	if resp.Error != nil {
		atomic.AddInt64(&result.errors, 1)
	}
}

func performConcurrentSave(
	cacheService *Service[UserData],
	key string,
	counterValue int,
	result *TestResult,
	workerID int,
) {
	// Track access for this key
	result.keyMutex.Lock()
	result.keyAccesses[key]++
	result.keyMutex.Unlock()

	// Extract user ID from key for the test data
	var userID int
	fmt.Sscanf(key, "user:%d", &userID)

	// Measure time
	start := time.Now()

	// Send save request with counter value for consistency checking
	cacheService.SetChan() <- SetRequest[UserData]{
		Key: key,
		Value: UserData{
			ID:      userID,
			Name:    fmt.Sprintf("User %d", userID),
			Profile: fmt.Sprintf("Profile from worker %d", workerID),
			Counter: counterValue, // Use the counter for consistency checking
		},
		// Random expiration between 200ms and 2s
		Expiration: time.Duration(200+rand.Intn(1800)) * time.Millisecond,
	}

	// Measure latency
	elapsed := time.Since(start)

	// Track statistics
	atomic.AddInt64(&result.saveCount, 1)

	// Track latency
	result.latencyMutex.Lock()
	result.operationLatencies = append(result.operationLatencies, elapsed)
	result.latencyMutex.Unlock()
}

func printTestResults(scenarioName string, result *TestResult) {
	fmt.Printf("\nResults for %s:\n", scenarioName)
	fmt.Printf("- Total operations: %d\n", result.totalOperations)
	fmt.Printf("- Get operations: %d (%.1f%%)\n",
		result.getCount,
		100*float64(result.getCount)/float64(result.totalOperations))
	fmt.Printf("- Set operations: %d (%.1f%%)\n",
		result.saveCount,
		100*float64(result.saveCount)/float64(result.totalOperations))
	fmt.Printf("- Errors: %d (%.1f%%)\n",
		result.errors,
		100*float64(result.errors)/float64(result.totalOperations))

	// Calculate average operations per second
	opsPerSecond := float64(result.totalOperations) / 5.0 // assuming 5s duration
	fmt.Printf("- Operations per second: %.2f\n", opsPerSecond)

	// Calculate key access statistics
	result.keyMutex.RLock()
	var totalKeys = len(result.keyAccesses)
	var maxAccesses = 0
	var minAccesses = int(^uint(0) >> 1) // Max int value
	var totalAccesses = 0

	for _, accesses := range result.keyAccesses {
		totalAccesses += accesses
		if accesses > maxAccesses {
			maxAccesses = accesses
		}
		if accesses < minAccesses {
			minAccesses = accesses
		}
	}
	result.keyMutex.RUnlock()

	if totalKeys > 0 {
		avgAccesses := float64(totalAccesses) / float64(totalKeys)
		fmt.Printf("- Key statistics:\n")
		fmt.Printf("  - Unique keys accessed: %d\n", totalKeys)
		fmt.Printf("  - Average accesses per key: %.2f\n", avgAccesses)
		fmt.Printf("  - Min accesses to a key: %d\n", minAccesses)
		fmt.Printf("  - Max accesses to a key: %d\n", maxAccesses)
	}

	// Calculate latency statistics
	result.latencyMutex.Lock()
	if len(result.operationLatencies) > 0 {
		var totalLatency time.Duration
		var minLatency = result.operationLatencies[0]
		var maxLatency = result.operationLatencies[0]

		for _, t := range result.operationLatencies {
			totalLatency += t
			if t < minLatency {
				minLatency = t
			}
			if t > maxLatency {
				maxLatency = t
			}
		}

		avgLatency := totalLatency / time.Duration(len(result.operationLatencies))
		fmt.Printf("- Latency:\n")
		fmt.Printf("  - Average: %v\n", avgLatency)
		fmt.Printf("  - Min: %v\n", minLatency)
		fmt.Printf("  - Max: %v\n", maxLatency)
	}
	result.latencyMutex.Unlock()
}
