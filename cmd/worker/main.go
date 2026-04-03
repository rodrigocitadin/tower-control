package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Redis Failed: %v", err)
	}

	fmt.Println("[Worker] FSM started. Runway FREE.")

	consecutiveLandings := 0
	const maxLandingsBeforeTakeoff = 3
	ctx := context.Background()

	for {
		currentAirplane, opType := getNextPlane(ctx, rdb, &consecutiveLandings, maxLandingsBeforeTakeoff)

		if currentAirplane == "" {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		fmt.Printf("\n[Worker] Runway CLEARED -> Plane: %s | Operation: %s | Landings Streak: %d\n", currentAirplane, opType, consecutiveLandings)

		pubsub := rdb.Subscribe(ctx, "atc_events:"+currentAirplane)
		eventMsgs := pubsub.Channel()

		rdb.Publish(ctx, "tower_notifications:"+currentAirplane, "CLEARED")

		action, err := waitForAction(eventMsgs, 5*time.Second)

		if err != nil {
			fmt.Printf("[Worker] TIMEOUT: Plane %s didn't respond. Aborting.\n", currentAirplane)
			pubsub.Close()
			continue
		}

		if action == "CANCEL" {
			fmt.Printf("[Worker] EMERGENCY: Plane %s CANCELED the %s! Releasing runway instantly.\n", currentAirplane, opType)
			pubsub.Close()
			continue
		}

		if action == "START" {
			fmt.Printf("[Worker] Plane %s IN USE of runway.\n", currentAirplane)

			completeAction, completeErr := waitForAction(eventMsgs, 30*time.Second)
			if completeErr != nil || completeAction != "COMPLETE" {
				fmt.Printf("[Worker] EMERGENCY: Plane %s didn't conclude in time!\n", currentAirplane)
				pubsub.Close()
				continue
			}

			fmt.Printf("[Worker] Plane %s left the runway. Returning to FREE state.\n", currentAirplane)
		}
		pubsub.Close()
	}
}

func getNextPlane(ctx context.Context, rdb *redis.Client, consecutive *int, maxLandings int) (string, string) {
	if *consecutive >= maxLandings {
		res, err := rdb.BLPop(ctx, 1*time.Second, "takeoff_queue").Result()
		if err == nil && len(res) == 2 {
			*consecutive = 0
			return res[1], "TAKEOFF"
		}
	}

	zRes, err := rdb.BZPopMin(ctx, 1*time.Second, "landing_queue").Result()
	if err == nil && zRes != nil {
		*consecutive++
		return zRes.Member.(string), "LANDING"
	}

	res, err := rdb.BLPop(ctx, 1*time.Second, "takeoff_queue").Result()
	if err == nil && len(res) == 2 {
		*consecutive = 0
		return res[1], "TAKEOFF"
	}

	return "", ""
}

func waitForAction(eventMsgs <-chan *redis.Message, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case msg := <-eventMsgs:
			return msg.Payload, nil
		case <-timer.C:
			return "", errors.New("timeout")
		}
	}
}
