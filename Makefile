.PHONY: help gen infra-up infra-down run-server run-worker build clean

help:
	@echo "Available commands:"
	@echo "  make gen           - Generate Protobuf files"
	@echo "  make infra-up      - Start infrastructure (RabbitMQ) in the background"
	@echo "  make infra-down    - Stop and remove infrastructure containers"
	@echo "  make run-server    - Start the gRPC API server"
	@echo "  make run-worker    - Start the FSM Worker"
	@echo "  make run-simulator - Start the Plane Simulator"
	@echo "  make build         - Compile binaries for server and worker"
	@echo "  make clean         - Remove generated Protobuf files and compiled binaries"

gen:
	@echo "==> Generating Protobuf files..."
	@mkdir -p pb
	@protoc --proto_path=proto \
	       --go_out=pb --go_opt=paths=source_relative \
	       --go-grpc_out=pb --go-grpc_opt=paths=source_relative \
	       atc.proto
	@echo "==> Files successfully generated in pb/ folder"

infra-up:
	@echo "==> Starting infrastructure (RabbitMQ)..."
	@docker compose up -d
	@echo "==> Infrastructure is running. Web panel at http://localhost:15672"

infra-down:
	@echo "==> Stopping infrastructure..."
	@docker compose down -v

run-server:
	@echo "==> Starting gRPC API server..."
	@go run cmd/server/main.go

run-worker:
	@echo "==> Starting FSM Worker..."
	@go run cmd/worker/main.go

run-simulator:
	@echo "==> Starting Air Traffic Simulator..."
	@go run cmd/simulator/main.go

build: gen
	@echo "==> Compiling binaries..."
	@mkdir -p bin
	@go build -o bin/server cmd/server/main.go
	@go build -o bin/worker cmd/worker/main.go
	@echo "==> Binaries saved to bin/"

clean:
	@echo "==> Cleaning generated files..."
	@rm -rf pb/*.go
	@rm -rf bin/
