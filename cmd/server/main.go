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
	pubsub := s.rdb.Subscribe(ctx, "tower_notifications:"+req.AirplaneId)
	defer pubsub.Close()
	ch := pubsub.Channel()

	err := s.rdb.RPush(ctx, "takeoff_queue", req.AirplaneId).Err()
	if err != nil {
		return nil, fmt.Errorf("failed to enqueue takeoff: %w", err)
	}

	log.Printf("[gRPC] Plane %s enqueued for TAKEOFF. Waiting for clearance...", req.AirplaneId)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ch:
		return &pb.TakeoffResponse{Status: "CLEARED"}, nil
	}
}

func (s *atcServer) RequestLanding(ctx context.Context, req *pb.LandingRequest) (*pb.LandingResponse, error) {
	crashTime := float64(time.Now().UnixNano())/1e9 + req.TimeRemaining

	pubsub := s.rdb.Subscribe(ctx, "tower_notifications:"+req.AirplaneId)
	defer pubsub.Close()
	ch := pubsub.Channel()

	err := s.rdb.ZAdd(ctx, "landing_queue", redis.Z{
		Score:  crashTime,
		Member: req.AirplaneId,
	}).Err()

	if err != nil {
		return nil, fmt.Errorf("failed to enqueue to Redis: %w", err)
	}

	log.Printf("[gRPC] Plane %s enqueued for LANDING (Crash at: %.2f). Waiting for clearance...", req.AirplaneId, crashTime)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ch:
		return &pb.LandingResponse{Status: "CLEARED"}, nil
	}
}

func (s *atcServer) StartOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.rdb.Publish(ctx, "atc_events:"+req.AirplaneId, "START")
	return &pb.OperationResponse{Success: true, Message: "Start signal sent"}, nil
}

func (s *atcServer) CompleteOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.rdb.Publish(ctx, "atc_events:"+req.AirplaneId, "COMPLETE")
	return &pb.OperationResponse{Success: true, Message: "Completion signal sent"}, nil
}

func (s *atcServer) CancelOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.rdb.Publish(ctx, "atc_events:"+req.AirplaneId, "CANCEL")
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

	log.Println("gRPC server (Redis Engine) listening on :3000...")
	grpcServer.Serve(lis)
}
