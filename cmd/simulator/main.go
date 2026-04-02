package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/rodrigocitadin/tower-control/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	conn, err := grpc.NewClient("localhost:3000", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	client := pb.NewControlTowerClient(conn)

	totalPlanes := 50
	var wg sync.WaitGroup

	fmt.Printf("Starting simulation with %d planes...\n\n", totalPlanes)

	for i := 1; i <= totalPlanes; i++ {
		wg.Add(1)

		go func(planeNum int) {
			defer wg.Done()

			arrivalDelay := time.Duration(rand.Float64()*20.0) * time.Second
			time.Sleep(arrivalDelay)

			fuelSeconds := 2.0 + rand.Float64()*43.0
			airplaneID := fmt.Sprintf("FLIGHT-%03d", planeNum)

			fmt.Printf("[%s] Arrived at airspace! Requesting landing. Fuel for: %05.2f sec\n", airplaneID, fuelSeconds)

			requestTime := time.Now()

			res, err := client.RequestLanding(context.Background(), &pb.LandingRequest{
				AirplaneId:    airplaneID,
				TimeRemaining: fuelSeconds,
			})

			if err != nil {
				log.Printf("[%s] Communication error: %v\n", airplaneID, err)
				return
			}

			if res.Status == "CLEARED" {
				timeWaited := time.Since(requestTime).Seconds()

				if timeWaited > fuelSeconds {
					fmt.Printf("[%s] FATAL CRASH: Ran out of fuel! (Waited %.2f s, Fuel %.2f s)\n", airplaneID, timeWaited, fuelSeconds)
					client.CancelOperation(context.Background(), &pb.OperationRequest{AirplaneId: airplaneID})
					return
				}

				fmt.Printf("[%s] CLEARED: Landing safely! (Waited %.2f s, Fuel %.2f s)\n", airplaneID, timeWaited, fuelSeconds)
				client.StartOperation(context.Background(), &pb.OperationRequest{AirplaneId: airplaneID})

				time.Sleep(1 * time.Second)

				fmt.Printf("[%s] Landing complete! Releasing runway.\n", airplaneID)
				client.CompleteOperation(context.Background(), &pb.OperationRequest{AirplaneId: airplaneID})
			}
		}(i)
	}

	wg.Wait()
	fmt.Println("\nSimulation ended!")
}
