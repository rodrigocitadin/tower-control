package main

import (
	"context"
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
		currentAirplane, opType, score := getNextPlane(ctx, rdb, &consecutiveLandings, maxLandingsBeforeTakeoff)

		if currentAirplane == "" {
			continue
		}

		fmt.Printf("\n[Worker] Runway CLEARED -> Plane: %s | Operation: %s | Landings Streak: %d\n", currentAirplane, opType, consecutiveLandings)

		rdb.RPush(ctx, "response:"+currentAirplane, "CLEARED")

		action, err := waitForAction(ctx, rdb, currentAirplane, 5*time.Second)

		if err != nil {
			fmt.Printf("[Worker] TIMEOUT: Plane %s didn't respond. RE-QUEUEING!\n", currentAirplane)
			requeuePlane(ctx, rdb, currentAirplane, opType, score)
			continue
		}

		if action == "CANCEL" {
			fmt.Printf("[Worker] EMERGENCY: Plane %s CANCELED the %s! Releasing runway instantly.\n", currentAirplane, opType)
			continue
		}

		if action == "START" {
			fmt.Printf("[Worker] Plane %s IN USE of runway.\n", currentAirplane)

			completeAction, completeErr := waitForAction(ctx, rdb, currentAirplane, 30*time.Second)
			if completeErr != nil || completeAction != "COMPLETE" {
				fmt.Printf("[Worker] TIMEOUT: Plane %s didn't conclude in time! RE-QUEUEING!\n", currentAirplane)
				requeuePlane(ctx, rdb, currentAirplane, opType, score)
				continue
			}

			fmt.Printf("[Worker] Plane %s left the runway. Returning to FREE state.\n", currentAirplane)
		}
	}
}

func requeuePlane(ctx context.Context, rdb *redis.Client, planeID string, opType string, originalScore float64) {
	if opType == "LANDING" {
		rdb.ZAdd(ctx, "landing_queue", redis.Z{Score: originalScore, Member: planeID})
	} else {
		rdb.RPush(ctx, "takeoff_queue", planeID)
	}
}

func getNextPlane(ctx context.Context, rdb *redis.Client, consecutive *int, maxLandings int) (string, string, float64) {
	if *consecutive >= maxLandings {
		res, err := rdb.BLPop(ctx, 100*time.Millisecond, "takeoff_queue").Result()
		if err == nil && len(res) == 2 {
			*consecutive = 0
			return res[1], "TAKEOFF", 0
		}
	}

	zRes, err := rdb.BZPopMin(ctx, 100*time.Millisecond, "landing_queue").Result()
	if err == nil && zRes != nil {
		*consecutive++
		return zRes.Member.(string), "LANDING", zRes.Score
	}

	res, err := rdb.BLPop(ctx, 100*time.Millisecond, "takeoff_queue").Result()
	if err == nil && len(res) == 2 {
		*consecutive = 0
		return res[1], "TAKEOFF", 0
	}

	return "", "", 0
}

func waitForAction(ctx context.Context, rdb *redis.Client, planeID string, timeout time.Duration) (string, error) {
	res, err := rdb.BLPop(ctx, timeout, "events:"+planeID).Result()
	if err != nil {
		return "", err
	}
	rdb.Del(ctx, "events:"+planeID)
	return res[1], nil
}
