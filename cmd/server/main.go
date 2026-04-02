package main

import (
	"context"
	"fmt"
	"log"
	"net"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rodrigocitadin/tower-control/pb"
	"google.golang.org/grpc"
)

type tcServer struct {
	pb.UnimplementedControlTowerServer
	amqpChan *amqp.Channel
}

func calculatePriority(timeRemaining float64) uint8 {
	urgency := 10 - int(timeRemaining/10)
	if urgency > 10 {
		return 10
	}
	if urgency < 1 {
		return 1
	}
	return uint8(urgency)
}

func (s *tcServer) RequestLanding(ctx context.Context, req *pb.LandingRequest) (*pb.LandingResponse, error) {
	priority := calculatePriority(req.TimeRemaining)

	q, _ := s.amqpChan.QueueDeclare("", false, true, true, false, nil)
	s.amqpChan.QueueBind(q.Name, req.AirplaneId, "tower_notifications", false, nil)
	msgs, _ := s.amqpChan.Consume(q.Name, "", true, false, false, false, nil)

	err := s.amqpChan.PublishWithContext(ctx,
		"", "landing_queue", false, false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			Priority:     priority,
			ContentType:  "text/plain",
			Body:         []byte(req.AirplaneId),
		})

	if err != nil {
		return nil, fmt.Errorf("failed to enqueue to RabbitMQ: %w", err)
	}

	log.Printf("[gRPC] Plane %s enqueued (Priority: %d). Waiting for clearance...", req.AirplaneId, priority)

	select {
	case <-ctx.Done():
		s.amqpChan.QueueDelete(q.Name, false, false, false)
		return nil, ctx.Err()
	case <-msgs:
		s.amqpChan.QueueDelete(q.Name, false, false, false)
		return &pb.LandingResponse{Status: "CLEARED"}, nil
	}
}

func (s *tcServer) StartOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.amqpChan.PublishWithContext(ctx, "tc_events", req.AirplaneId, false, false, amqp.Publishing{Body: []byte("START")})
	return &pb.OperationResponse{Success: true, Message: "Start signal sent"}, nil
}

func (s *tcServer) CompleteOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.amqpChan.PublishWithContext(ctx, "tc_events", req.AirplaneId, false, false, amqp.Publishing{Body: []byte("COMPLETE")})
	return &pb.OperationResponse{Success: true, Message: "Completion signal sent"}, nil
}

func (s *tcServer) CancelOperation(ctx context.Context, req *pb.OperationRequest) (*pb.OperationResponse, error) {
	s.amqpChan.PublishWithContext(ctx, "tc_events", req.AirplaneId, false, false, amqp.Publishing{Body: []byte("CANCEL")})
	return &pb.OperationResponse{Success: true, Message: "Cancel signal sent"}, nil
}

func main() {
	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		log.Fatalf("RabbitMQ Fail: %v", err)
	}
	defer conn.Close()

	ch, _ := conn.Channel()
	defer ch.Close()

	args := amqp.Table{"x-max-priority": 10}
	ch.QueueDeclare("landing_queue", true, false, false, false, args)

	ch.ExchangeDeclare("tc_events", "direct", true, false, false, false, nil)
	ch.ExchangeDeclare("tower_notifications", "direct", true, false, false, false, nil)

	lis, _ := net.Listen("tcp", ":3000")
	grpcServer := grpc.NewServer()
	pb.RegisterControlTowerServer(grpcServer, &tcServer{amqpChan: ch})

	log.Println("gRPC server listening on :3000...")
	grpcServer.Serve(lis)
}
