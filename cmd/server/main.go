package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rodrigocitadin/atc/pb"
	"google.golang.org/grpc"
)

type atcServer struct {
	pb.UnimplementedControlTowerServer
	rdb *redis.Client
}

func (s *atcServer) RequestTakeoff(ctx context.Context, req *pb.TakeoffRequest) (*pb.TakeoffResponse, error) {
	err := s.rdb.SetArgs(ctx, "active:"+req.AirplaneId, "1", redis.SetArgs{
		Mode: "NX",
		TTL:  10 * time.Minute,
	}).Err()

	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("plane %s is already in an active operation", req.AirplaneId)
		}
		return nil, fmt.Errorf("failed to set active state: %w", err)
	}

	err = s.rdb.RPush(ctx, "takeoff_queue", req.AirplaneId).Err()
	if err != nil {
		s.rdb.Del(ctx, "active:"+req.AirplaneId)
		return nil, fmt.Errorf("failed to enqueue takeoff: %w", err)
	}

	log.Printf("[gRPC] Plane %s enqueued for TAKEOFF. Waiting for clearance...", req.AirplaneId)

	res, err := s.rdb.BLPop(ctx, 0, "response:"+req.AirplaneId).Result()
	if err != nil {
		s.rdb.Del(ctx, "active:"+req.AirplaneId)
		return nil, err
	}

	return &pb.TakeoffResponse{Status: res[1]}, nil
}

func (s *atcServer) RequestLanding(ctx context.Context, req *pb.LandingRequest) (*pb.LandingResponse, error) {
	err := s.rdb.SetArgs(ctx, "active:"+req.AirplaneId, "1", redis.SetArgs{
		Mode: "NX",
		TTL:  10 * time.Minute,
	}).Err()

	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("plane %s is already in an active operation", req.AirplaneId)
		}
		return nil, fmt.Errorf("failed to set active state: %w", err)
	}

	crashTime := float64(time.Now().UnixNano())/1e9 + req.TimeRemaining

	err = s.rdb.ZAdd(ctx, "landing_queue", redis.Z{
		Score:  crashTime,
		Member: req.AirplaneId,
	}).Err()

	if err != nil {
		s.rdb.Del(ctx, "active:"+req.AirplaneId)
		return nil, fmt.Errorf("failed to enqueue to Redis: %w", err)
	}

	log.Printf("[gRPC] Plane %s enqueued for LANDING (Crash at: %.2f). Waiting for clearance...", req.AirplaneId, crashTime)

	gracePeriod := 1.0
	maxWaitTime := time.Duration((req.TimeRemaining + gracePeriod) * float64(time.Second))

	waitCtx, cancel := context.WithTimeout(ctx, maxWaitTime)
	defer cancel()

	res, err := s.rdb.BLPop(waitCtx, 0, "response:"+req.AirplaneId).Result()
	if err != nil {
		log.Printf("[gRPC] Clearing aircraft %s (Fuel timeout or disconnection)", req.AirplaneId)

		s.rdb.Del(context.Background(), "active:"+req.AirplaneId)
		s.rdb.ZRem(context.Background(), "landing_queue", req.AirplaneId)

		if err == context.DeadlineExceeded {
			return nil, fmt.Errorf("ran out of fuel while waiting for clearance")
		}
		return nil, err
	}

	return &pb.LandingResponse{Status: res[1]}, nil
}

func (s *atcServer) StartOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.rdb.RPush(ctx, "events:"+req.AirplaneId, "START")
	return &pb.OperationResponse{Success: true, Message: "Start signal sent"}, nil
}

func (s *atcServer) CompleteOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.rdb.Del(ctx, "active:"+req.AirplaneId)
	s.rdb.RPush(ctx, "events:"+req.AirplaneId, "COMPLETE")
	return &pb.OperationResponse{Success: true, Message: "Completion signal sent"}, nil
}

func (s *atcServer) CancelOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.rdb.Del(ctx, "active:"+req.AirplaneId)
	s.rdb.ZRem(ctx, "landing_queue", req.AirplaneId)
	s.rdb.LRem(ctx, "takeoff_queue", 0, req.AirplaneId)
	s.rdb.RPush(ctx, "events:"+req.AirplaneId, "CANCEL")
	return &pb.OperationResponse{Success: true, Message: "Cancel signal sent"}, nil
}

func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Redis Fail: %v", err)
	}

	rdb.FlushDB(context.Background())

	lis, _ := net.Listen("tcp", ":3000")
	grpcServer := grpc.NewServer()
	pb.RegisterControlTowerServer(grpcServer, &atcServer{rdb: rdb})

	log.Println("gRPC server (Redis Reliable Engine) listening on :3000...")
	grpcServer.Serve(lis)
}
