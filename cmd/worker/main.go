package main

import (
	"log"

	"github.com/thekrauss/kubemanager/internal/infrastructure/workflows/ping"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	c, err := client.Dial(client.Options{
		HostPort: "localhost:7233",
	})
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()
	w := worker.New(c, "kubemanager-task-queue", worker.Options{})

	w.RegisterWorkflow(ping.PingWorkflow)
	w.RegisterActivity(ping.PingActivity)

	log.Println("Worker démarré  En attente de tâches...")
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("Unable to start worker", err)
	}
}
