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

// Statistics structure to track metrics
type Stats struct {
	getCount   int64
	getHits    int64
	getMisses  int64
	getErrors  int64
	saveCount  int64
	saveErrors int64
	getTimes   []time.Duration
	saveTimes  []time.Duration
	timesMutex sync.Mutex
}

func TestLoadTest(t *testing.T) {
	// Set random seed
	rand.Seed(time.Now().UnixNano())

	// Create an update function for refreshing expired cache entries
	updateFunc := func(key string) (UserData, time.Duration, error) {
		// Simulate some delay for the update function
		time.Sleep(5 * time.Millisecond)

		userID := rand.Intn(100)
		return UserData{
			ID:      userID,
			Name:    fmt.Sprintf("Updated User %d", userID),
			Profile: fmt.Sprintf("Updated Profile %d", userID),
		}, 30 * time.Second, nil
	}

	// Create the memory cache provider
	memCache := NewMemoryCache[UserData]()

	// Create the cache service with 1 second cleanup interval
	cacheService := NewService[UserData](memCache, updateFunc, ServiceConfiguration{
		CleanupInterval:   1 * time.Second,
		UpdateOnCacheMiss: true,
		UpdateOnGetError:  false,
	})
	defer cacheService.Close()
	cacheService.StartAsync(context.Background())

	// Initialize statistics
	stats := &Stats{
		getTimes:  make([]time.Duration, 0, 500000),
		saveTimes: make([]time.Duration, 0, 500000),
	}

	// Preload some data to test both cache hits and misses
	fmt.Println("Preloading cache...")
	preloadCache(cacheService, 100)

	// Run the load test
	fmt.Println("\nStarting load test...")

	// Setup real-time monitoring
	stopMonitoring := make(chan struct{})
	go monitorStats(stats, stopMonitoring)

	// Run the actual load test
	loadTest(cacheService, stats, 500000, 10*time.Second)

	// Stop monitoring
	close(stopMonitoring)

	// Print final results
	printFinalStats(stats)
}

// preloadCache adds some initial data to the cache
func preloadCache(cacheService *Service[UserData], count int) {
	for i := 0; i < count; i++ {
		userID := i
		cacheService.SetChan() <- SetRequest[UserData]{
			Key: fmt.Sprintf("user:%d", userID),
			Value: UserData{
				ID:      userID,
				Name:    fmt.Sprintf("User %d", userID),
				Profile: fmt.Sprintf("Profile %d", userID),
			},
			// Random expiration between 5 and 30 seconds
			Expiration: time.Duration(5+rand.Intn(25)) * time.Second,
		}
	}

	// Give it a moment to process the preloaded data
	time.Sleep(100 * time.Millisecond)
}

// monitorStats periodically displays stats during the test
func monitorStats(stats *Stats, stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastGetCount, lastSaveCount int64
	startTime := time.Now()

	for {
		select {
		case <-ticker.C:
			currentGetCount := atomic.LoadInt64(&stats.getCount)
			currentSaveCount := atomic.LoadInt64(&stats.saveCount)
			getRate := currentGetCount - lastGetCount
			saveRate := currentSaveCount - lastSaveCount

			fmt.Printf("\rRunning for %.1fs | Gets: %d (%.1f/s) | Saves: %d (%.1f/s) | Total: %d",
				time.Since(startTime).Seconds(),
				currentGetCount,
				float64(getRate),
				currentSaveCount,
				float64(saveRate),
				currentGetCount+currentSaveCount)

			lastGetCount = currentGetCount
			lastSaveCount = currentSaveCount
		case <-stop:
			return
		}
	}
}

// loadTest sends a large number of requests over the specified duration
func loadTest(cacheService *Service[UserData], stats *Stats, targetRequests int, duration time.Duration) {
	var wg sync.WaitGroup
	startTime := time.Now()
	endTime := startTime.Add(duration)

	// Calculate how many goroutines to use (workers)
	numWorkers := 50
	requestsPerWorker := targetRequests / numWorkers

	// Launch workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Each worker sends its share of requests
			for j := 0; j < requestsPerWorker && time.Now().Before(endTime); j++ {
				// Determine whether to do a get or save (70% gets, 30% saves)
				if rand.Float32() < 0.7 {
					performGet(cacheService, stats)
				} else {
					performSave(cacheService, stats)
				}

				// Small sleep to control rate
				delay := time.Duration(rand.Intn(500)) * time.Microsecond
				time.Sleep(delay)
			}
		}(i)
	}

	// Wait for all workers to complete
	wg.Wait()

	// Print a newline after the monitoring output
	fmt.Println()
}

// performGet executes a Get operation against the cache
func performGet(cacheService *Service[UserData], stats *Stats) {
	// Choose a user ID - some will exist in cache, some won't
	userID := rand.Intn(200) // Note: We preloaded only 0-99

	// Create response channel
	respChan := make(chan Response[UserData])

	// Measure execution time
	start := time.Now()

	// Send get request
	cacheService.GetChan() <- Request[UserData]{
		Key:          fmt.Sprintf("user:%d", userID),
		ResponseChan: respChan,
	}

	// Wait for response
	resp := <-respChan

	// Calculate elapsed time
	elapsed := time.Since(start)

	// Track results
	atomic.AddInt64(&stats.getCount, 1)
	if resp.Error != nil {
		atomic.AddInt64(&stats.getErrors, 1)
	} else if resp.CacheMiss {
		atomic.AddInt64(&stats.getHits, 1)
	} else {
		atomic.AddInt64(&stats.getMisses, 1)
	}

	// Set timing information
	stats.timesMutex.Lock()
	stats.getTimes = append(stats.getTimes, elapsed)
	stats.timesMutex.Unlock()
}

// performSave executes a Set operation against the cache
func performSave(cacheService *Service[UserData], stats *Stats) {
	userID := rand.Intn(200)

	// Measure execution time
	start := time.Now()

	// Send save request
	cacheService.SetChan() <- SetRequest[UserData]{
		Key: fmt.Sprintf("user:%d", userID),
		Value: UserData{
			ID:      userID,
			Name:    fmt.Sprintf("User %d", userID),
			Profile: fmt.Sprintf("Profile %d", userID),
		},
		// Random expiration between 5 and 60 seconds
		Expiration: time.Duration(5+rand.Intn(55)) * time.Second,
	}

	// Calculate elapsed time
	elapsed := time.Since(start)

	// Track results
	atomic.AddInt64(&stats.saveCount, 1)

	// Since save is asynchronous, we can't easily track errors
	// However, we can measure the time it takes to send to the channel

	// Set timing information
	stats.timesMutex.Lock()
	stats.saveTimes = append(stats.saveTimes, elapsed)
	stats.timesMutex.Unlock()
}

// printFinalStats outputs detailed statistics after the test completes
func printFinalStats(stats *Stats) {
	fmt.Printf("\n\n========== Load Test Results ==========\n")

	// Calculate operations per second
	totalOps := stats.getCount + stats.saveCount
	fmt.Printf("Total Operations: %d\n", totalOps)
	fmt.Printf("  - Get Operations: %d (%.1f%%)\n", stats.getCount, 100*float64(stats.getCount)/float64(totalOps))
	fmt.Printf("    - Cache Hits: %d (%.1f%%)\n", stats.getHits, 100*float64(stats.getHits)/float64(stats.getCount))
	fmt.Printf("    - Cache Misses: %d (%.1f%%)\n", stats.getMisses, 100*float64(stats.getMisses)/float64(stats.getCount))
	fmt.Printf("    - Errors: %d (%.1f%%)\n", stats.getErrors, 100*float64(stats.getErrors)/float64(stats.getCount))
	fmt.Printf("  - Set Operations: %d (%.1f%%)\n", stats.saveCount, 100*float64(stats.saveCount)/float64(totalOps))

	// Calculate latency statistics
	stats.timesMutex.Lock()
	defer stats.timesMutex.Unlock()

	if len(stats.getTimes) > 0 {
		var totalGetTime time.Duration
		var minGetTime = stats.getTimes[0]
		var maxGetTime = stats.getTimes[0]

		for _, t := range stats.getTimes {
			totalGetTime += t
			if t < minGetTime {
				minGetTime = t
			}
			if t > maxGetTime {
				maxGetTime = t
			}
		}

		avgGetTime := totalGetTime / time.Duration(len(stats.getTimes))
		fmt.Printf("\nGet Latency:\n")
		fmt.Printf("  - Average: %v\n", avgGetTime)
		fmt.Printf("  - Min: %v\n", minGetTime)
		fmt.Printf("  - Max: %v\n", maxGetTime)
	}

	if len(stats.saveTimes) > 0 {
		var totalSaveTime time.Duration
		var minSaveTime = stats.saveTimes[0]
		var maxSaveTime = stats.saveTimes[0]

		for _, t := range stats.saveTimes {
			totalSaveTime += t
			if t < minSaveTime {
				minSaveTime = t
			}
			if t > maxSaveTime {
				maxSaveTime = t
			}
		}

		avgSaveTime := totalSaveTime / time.Duration(len(stats.saveTimes))
		fmt.Printf("\nSet Latency:\n")
		fmt.Printf("  - Average: %v\n", avgSaveTime)
		fmt.Printf("  - Min: %v\n", minSaveTime)
		fmt.Printf("  - Max: %v\n", maxSaveTime)
	}
}
