# Tower Control Simulator

An event-driven, high-concurrency Air Traffic Control (ATC) simulator built with **Go**, **gRPC**, and **RabbitMQ**.

This project serves as a proof-of-concept for advanced distributed system patterns, specifically tackling complex queueing problems like strict priority routing, starvation prevention, and cascading failure mitigation.

## The Challenge

Managing an airport runway is a classic critical-resource problem. The system must handle a massive influx of concurrent requests from airplanes wanting to land or take off. It must guarantee that:

1. Planes running out of fuel land first.
2. Planes that crash or lose communication do not lock the runway.
3. Planes waiting to take off are not starved indefinitely by a constant stream of emergency landings.

## Architecture & Tech Stack

* **Go (Golang):** Chosen for its first-class concurrency capabilities (goroutines/channels) to simulate dozens of planes communicating simultaneously.
* **gRPC & Protobuf:** Used for the strict, high-performance synchronous communication between the airplanes (clients) and the Control Tower (server).
* **RabbitMQ:** Acts as the async message broker and shock absorber, routing requests into distinct queues (`landing_queue` and `takeoff_queue`) and handling priority buffering.
* **Docker & Make:** For reproducible infrastructure and streamlined execution.

## Architectural Trade-offs: RabbitMQ vs. Redis

A common question in this design is why we opted for **RabbitMQ** over **Redis Sorted Sets (ZSET)** for the priority queue. 

### Why RabbitMQ?

The deciding factor was **Reliability and Message Acknowledgments (Acks)**. 

* **Guaranteed Processing:** RabbitMQ’s `Ack` system ensures that if a Worker crashes while a plane is on the runway, the message is not lost. It remains in the queue and is re-delivered to another worker.
* **Native Priority Support:** It provides built-in support for priority levels (1-10), which simplifies the implementation of the Earliest Deadline First (EDF) logic without extra Lua scripting or complex client-side management.

### How we could use Redis (The Alternative Path)

If absolute precision were more critical than "at-least-once" delivery guarantees, Redis would be the superior choice for ordering:

* **The ZSET Strategy:** We would use a **Redis Sorted Set** where the "Score" is the absolute `Crash Timestamp` (`CurrentTime + FuelRemaining`).
* **Millisecond Precision:** Unlike RabbitMQ’s 10-bucket priority system, Redis would provide a perfectly sorted list with millisecond precision, ensuring the most urgent plane is *always* at the top.
* **The Workflow:** 
    1. The Server would insert planes using `ZADD`.
    2. The Worker would pull the most urgent plane using `BZPOPMIN`.
    3. Communication for signals (`START`, `CANCEL`) would be handled via **Redis Pub/Sub** channels named after each `airplane_id`.

In short, **RabbitMQ** was chosen for its **safety and robustness** in a simulation of critical resources, while **Redis** would be the choice for **mathematical precision** in ordering.

## Core Mechanics & System Design

### 1. Earliest Deadline First (EDF)

When an airplane requests a landing, it sends its remaining fuel time. The gRPC server calculates a priority score (1-10) and publishes it to a RabbitMQ priority queue. The system ensures that planes closest to a fatal crash jump to the front of the line, regardless of arrival order.

### 2. Starvation Prevention (Weighted Fair Queuing)

Because landing emergencies could theoretically block the runway forever, the FSM (Finite State Machine) Worker implements a weighted scheduler. It guarantees that for every **3 landings**, the system forcefully yields the runway for **1 takeoff** (if any are queued), preventing takeoff starvation while prioritizing safety.

### 3. Synchronous API over Asynchronous Messaging

Airplanes use synchronous gRPC calls that *block* until the ATC grants clearance. Behind the scenes, the gRPC server routes the request through RabbitMQ and waits for a specific `CLEARED` notification from the Worker using temporary reply queues. This hides the async complexity from the client.

### 4. Fail-Fast & Cascading Failure Mitigation

If an airplane runs out of fuel while waiting in the queue, it instantly triggers a `CANCEL` RPC call (Fail-Fast pattern). The Worker catches this event and releases the runway in milliseconds. If an airplane simply stops responding, the Worker relies on strict `Timeouts` to abort the operation and call the next plane, preventing the entire airport from halting (Cascading Failure).

## Getting Started

### Prerequisites

* Go 1.26+
* Docker & Docker Compose
* Make
* Protoc (Protocol Buffers Compiler)

### Running the Project

The project is easily orchestrated using the provided `Makefile`. Open three separate terminal instances:

**1. Start the Infrastructure**

Spins up the RabbitMQ broker via Docker.
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
