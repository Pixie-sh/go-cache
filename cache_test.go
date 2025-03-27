package pcache

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSample(t *testing.T) {
	// Create an update function for refreshing expired cache entries
	updateFunc := func(key string) (UserData, time.Duration, error) {
		// In a real application, you'd fetch fresh data here
		fmt.Printf("Updating expired key: %s\n", key)
		return UserData{
			ID:      123,
			Name:    "Updated User",
			Profile: "Updated Profile",
		}, 30 * time.Minute, nil
	}

	// Create the memory cache provider
	memCache := NewMemoryCache[UserData]()

	// Create the cache service with 10 second cleanup interval
	cacheService := NewService[UserData](memCache, updateFunc, ServiceConfiguration{
		CleanupInterval:   10 * time.Second,
		UpdateOnCacheMiss: true,
		UpdateOnGetError:  false,
	})
	defer cacheService.Close()
	var ctx = context.Background()
	cacheService.StartAsync(ctx)

	// Set some data
	setRespChan := make(chan SetResponse[UserData])
	cacheService.SetChan() <- SetRequest[UserData]{
		Ctx:          ctx,
		Key:          "user:123",
		Value:        UserData{ID: 123, Name: "John Doe", Profile: "Developer"},
		Expiration:   5 * time.Second,
		ResponseChan: setRespChan,
	}
	setResp := <-setRespChan
	fmt.Println("Set response:", setResp)

	// Get the data
	respChan := make(chan Response[UserData])
	cacheService.GetChan() <- Request[UserData]{
		Ctx:          ctx,
		Key:          "user:123",
		ResponseChan: respChan,
	}

	// Wait for response
	resp := <-respChan
	if resp.Error != nil {
		fmt.Printf("Error: %v\n", resp.Error)
	} else if resp.CacheMiss {
		fmt.Printf("CacheMiss user: %+v\n", resp.Value)
	} else {
		fmt.Println("User found in cache", resp.Value)
	}

	// Wait to see the cleanup routine work
	fmt.Println("Waiting for expiration...")
	time.Sleep(6 * time.Second)

	// Try to get the data again, it should be updated by the background process
	respChan = make(chan Response[UserData])
	cacheService.GetChan() <- Request[UserData]{
		Key:          "user:123",
		ResponseChan: respChan,
	}

	// Wait for response
	resp = <-respChan
	if resp.Error != nil {
		fmt.Printf("Error: %v\n", resp.Error)
	} else if resp.CacheMiss {
		fmt.Printf("CacheMiss updated user: %+v\n", resp.Value)
	} else {
		fmt.Println("User found in cache", resp.Value)
	}
}
