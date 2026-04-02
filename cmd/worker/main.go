package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		log.Fatalf("RabbitMQ Failed: %v", err)
	}
	defer conn.Close()

	ch, _ := conn.Channel()
	defer ch.Close()

	ch.Qos(1, 0, false)
	landingMsgs, _ := ch.Consume("landing_queue", "", false, false, false, false, nil)
	takeoffMsgs, _ := ch.Consume("takeoff_queue", "", false, false, false, false, nil)

	fmt.Println("[Worker] FSM started. Runway FREE.")

	consecutiveLandings := 0
	const maxLandingsBeforeTakeoff = 3

	for {
		d, opType := getNextPlane(landingMsgs, takeoffMsgs, &consecutiveLandings, maxLandingsBeforeTakeoff)

		currentAirplane := string(d.Body)
		fmt.Printf("\n[Worker] Runway CLEARED -> Plane: %s | Operation: %s | Landings Streak: %d\n", currentAirplane, opType, consecutiveLandings)

		q, _ := ch.QueueDeclare("", false, true, true, false, nil)
		ch.QueueBind(q.Name, currentAirplane, "tc_events", false, nil)
		eventMsgs, _ := ch.Consume(q.Name, "", true, false, false, false, nil)

		ch.PublishWithContext(context.Background(), "tower_notifications", currentAirplane, false, false, amqp.Publishing{
			Body: []byte("CLEARED"),
		})

		action, err := waitForAction(eventMsgs, 5*time.Second)

		if err != nil {
			fmt.Printf("[Worker] TIMEOUT: Plane %s didn't respond. Aborting.\n", currentAirplane)
			cleanUp(ch, q.Name, &d)
			continue
		}

		if action == "CANCEL" {
			fmt.Printf("[Worker] EMERGENCY: Plane %s CANCELED the %s! Releasing runway instantly.\n", currentAirplane, opType)
			cleanUp(ch, q.Name, &d)
			continue
		}

		if action == "START" {
			fmt.Printf("[Worker] Plane %s IN USE of runway.\n", currentAirplane)

			completeAction, completeErr := waitForAction(eventMsgs, 30*time.Second)
			if completeErr != nil || completeAction != "COMPLETE" {
				fmt.Printf("[Worker] EMERGENCY: Plane %s didn't conclude in time!\n", currentAirplane)
				cleanUp(ch, q.Name, &d)
				continue
			}

			fmt.Printf("[Worker] Plane %s left the runway. Returning to FREE state.\n", currentAirplane)
			d.Ack(false)
			ch.QueueDelete(q.Name, false, false, false)
		}
	}
}

func getNextPlane(landingMsgs, takeoffMsgs <-chan amqp.Delivery, consecutive *int, maxLandings int) (amqp.Delivery, string) {
	if *consecutive >= maxLandings {
		select {
		case d := <-takeoffMsgs:
			*consecutive = 0
			return d, "TAKEOFF"
		default:
		}
	}

	select {
	case d := <-landingMsgs:
		*consecutive++
		return d, "LANDING"
	default:
	}

	select {
	case d := <-takeoffMsgs:
		*consecutive = 0
		return d, "TAKEOFF"
	default:
	}

	select {
	case d := <-landingMsgs:
		*consecutive++
		return d, "LANDING"
	case d := <-takeoffMsgs:
		*consecutive = 0
		return d, "TAKEOFF"
	}
}

func waitForAction(eventMsgs <-chan amqp.Delivery, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case msg := <-eventMsgs:
			return string(msg.Body), nil
		case <-timer.C:
			return "", errors.New("timeout")
		}
	}
}

func cleanUp(ch *amqp.Channel, queueName string, d *amqp.Delivery) {
	ch.QueueDelete(queueName, false, false, false)
	d.Nack(false, false)
}
