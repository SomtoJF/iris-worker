package main

import (
	"log"

	"github.com/SomtoJF/iris-worker/initializers/sqldb"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

type TaskQueueName string

const (
	JobApplicationTaskQueueName TaskQueueName = "job-application"
)

func init() {
	err := sqldb.ConnectToSQLite()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	c, err := client.Dial(client.Options{})

	if err != nil {
		log.Fatalln("Unable to create Temporal client:", err)
	}

	defer c.Close()

	w := worker.New(c, string(JobApplicationTaskQueueName), worker.Options{})

	registerJobApplicationWorkflows(w)
	registerJobApplicationActivities(w)

	// Start listening to the Task Queue.
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("unable to start Worker", err)
	}
}

func registerJobApplicationWorkflows(w worker.Worker) {
	// w.RegisterWorkflow(JobApplicationWorkflow)
}

func registerJobApplicationActivities(w worker.Worker) {
	// w.RegisterActivity(JobApplicationActivity)
}
