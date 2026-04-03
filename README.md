# Air Traffic Control Simulator

An event-driven, high-concurrency Air Traffic Control (ATC) simulator built with **Go**, **gRPC**, and **Redis**.

This project serves as a proof-of-concept for advanced distributed system patterns, specifically tackling complex queueing problems like strict priority routing, starvation prevention, and perfect Earliest Deadline First (EDF) scheduling.

## The Challenge

Managing an airport runway is a classic critical-resource problem. The system must handle a massive influx of concurrent requests from airplanes wanting to land or take off. It must guarantee that:

1. Planes closest to crashing (running out of fuel) land first, with millisecond precision.
2. Planes that crash or lose communication do not lock the runway.
3. Planes waiting to take off are not starved indefinitely by a constant stream of emergency landings.

## Architecture & Tech Stack

* **Go (Golang):** Chosen for its first-class concurrency capabilities (goroutines/channels) to simulate dozens of planes communicating simultaneously.
* **gRPC & Protobuf:** Used for the strict, high-performance synchronous communication between the airplanes (clients) and the Control Tower (server).
* **Redis:** Acts as the high-speed, mathematically precise in-memory data store, utilizing **Sorted Sets (ZSET)** for priority queues, **Lists** for FIFO queues, and **Pub/Sub** for event notification.
* **Docker & Make:** For reproducible infrastructure and streamlined execution.

## Architectural Trade-offs: Redis vs. RabbitMQ

During the system design phase, this architecture migrated from **RabbitMQ** to **Redis** to solve a specific, complex issue: **Priority Aging (Starvation)**.

### Why Redis Won (The Aging Problem)

In traditional message brokers like RabbitMQ, priority is a static "bucket" (e.g., Priority 1 to 10) assigned at the moment a message enters the queue. If a plane enters with 40s of fuel (Priority 6), it stays at Priority 6 forever. If it waits for 35s, it now only has 5s of fuel, but new planes arriving with 5s of fuel will receive Priority 10 and jump ahead of it, causing the older plane to crash.

**Redis** solves this elegantly by using a continuous timeline instead of static buckets. 

* We use a **Redis Sorted Set (ZSET)**.
* The "Score" of the plane is its **Absolute Crash Timestamp** (`CurrentTime + FuelRemaining`).
* As time passes, the absolute timestamp never changes, but the system simply pulls the plane with the lowest numerical score using `BZPOPMIN`. The Earliest Deadline First is always mathematically perfect.

### What We Sacrificed (Acknowledgments)

By moving to Redis, we sacrificed the native `Ack` (Acknowledgment) mechanism of RabbitMQ. In Redis, `BZPOPMIN` physically removes the item from the queue. If our Worker crashes immediately after pulling a plane, that plane's data is lost. In a production scenario, this would be mitigated by atomic Lua scripts that move the plane to an "in-flight" tracking list until confirmed.

## Core Mechanics & System Design

### 1. Earliest Deadline First (EDF)
When an airplane requests a landing, the gRPC server calculates the exact UNIX timestamp of when it will run out of fuel. It pushes this to the Redis `landing_queue` (ZSET). The Worker always pops the plane with the smallest timestamp, guaranteeing absolute priority without CPU-heavy resorting.

### 2. Starvation Prevention (Weighted Fair Queuing)
Because landing emergencies could theoretically block the runway forever, the FSM (Finite State Machine) Worker implements a weighted scheduler. It guarantees that for every **3 landings**, the system forcefully reads from the Redis `takeoff_queue` (FIFO List) for **1 takeoff** (if any are queued), preventing takeoff starvation while prioritizing safety.

### 3. Synchronous API over Asynchronous Messaging
Airplanes use synchronous gRPC calls that *block* until the ATC grants clearance. Behind the scenes, the gRPC server routes the request through Redis and subscribes to a specific **Redis Pub/Sub** channel for that airplane. This perfectly hides the async complexity from the client.

### 4. Fail-Fast & Cascading Failure Mitigation
If an airplane runs out of fuel while waiting in the queue, it instantly triggers a `CANCEL` RPC call (Fail-Fast pattern). The Worker catches this event via Pub/Sub and releases the runway in milliseconds. If an airplane simply stops responding, the Worker relies on strict `Timeouts` to abort the operation and call the next plane, preventing the entire airport from halting (Cascading Failure).

## Getting Started

### Prerequisites
* Go 1.26+
* Docker & Docker Compose
* Make
* Protoc (Protocol Buffers Compiler)

### Running the Project

The project is easily orchestrated using the provided `Makefile`. Open three separate terminal instances:

**1. Start the Infrastructure**

Spins up the Redis instance via Docker.

```bash
make infra-up
```

**2. Start the Control Tower (Server & Worker)**

In Terminal 1, start the gRPC Server:

```bash
make run-server
```

In Terminal 2, start the Finite State Machine Worker:

```bash
make run-worker
```

**3. Run the Traffic Simulator**

In Terminal 3, fire up the simulator. This will spawn 50 concurrent planes (goroutines) with randomized fuel levels and operations (landing/takeoff) arriving over a 20-second window.

```bash
make run-simulator
```

Watch the terminal outputs to see the system dynamically prioritize landings, schedule takeoffs, and handle inevitable crashes gracefully!

### Cleaning Up

```bash
make infra-down
make clean
```
