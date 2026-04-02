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
	msgs, _ := ch.Consume("landing_queue", "", false, false, false, false, nil)

	fmt.Println("[Worker] FSM started. Runway FREE.")

	for d := range msgs {
		currentAirplane := string(d.Body)
		fmt.Printf("\n[Worker] Runway CLEARED -> Plane: %s | Priority MSG: %d\n", currentAirplane, d.Priority)

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
			fmt.Printf("[Worker] EMERGENCY: Plane %s CANCELED the landing! Releasing runway instantly.\n", currentAirplane)
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
