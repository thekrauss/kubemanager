package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/thekrauss/kubemanager/internal/infrastructure/workflows/ping"
	"go.temporal.io/sdk/client"
)

func main() {
	c, err := client.Dial(client.Options{HostPort: "localhost:7233"})
	if err != nil {
		log.Fatalln(err)
	}
	defer c.Close()

	options := client.StartWorkflowOptions{
		ID:        "ping-test-" + uuid.New().String(),
		TaskQueue: "kubemanager-task-queue",
	}

	log.Println(" envoi de la commande...")
	we, err := c.ExecuteWorkflow(context.Background(), options, ping.PingWorkflow, "Beto")
	if err != nil {
		log.Fatalln(err)
	}

	var result string

	err = we.Get(context.Background(), &result)
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Printf("\n RÉSULTAT : %s\n\n", result)
}
