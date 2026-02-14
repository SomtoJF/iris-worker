package main

import (
	"log"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/llm"
	sqldbActivities "github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/common"
	"github.com/SomtoJF/iris-worker/initializers/sqldb"
	"github.com/SomtoJF/iris-worker/workflow/jobapplication"
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
	dependencies, err := common.MakeDependencies()
	if err != nil {
		log.Fatal(err)
	}
	defer dependencies.Cleanup()

	c, err := client.Dial(client.Options{})

	if err != nil {
		log.Fatalln("Unable to create Temporal client:", err)
	}

	defer c.Close()

	w := worker.New(c, string(JobApplicationTaskQueueName), worker.Options{
		EnableSessionWorker: true,
	})

	registerJobApplicationWorkflows(w)
	registerJobApplicationActivities(w, dependencies)

	// Start listening to the Task Queue.
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("unable to start Worker", err)
	}
}

func registerJobApplicationWorkflows(w worker.Worker) {
	w.RegisterWorkflow(jobapplication.JobApplicationWorkflow)
}

func registerJobApplicationActivities(w worker.Worker, dependencies common.Dependencies) {
	sqldbActivities := sqldbActivities.NewActivities(sqldb.DB)
	w.RegisterActivity(sqldbActivities)

	llmActivities := llm.NewActivity(dependencies.GetAIPIClient())
	w.RegisterActivity(llmActivities)

	browserActivities := browser.NewActivities(dependencies.GetBrowserClient())
	w.RegisterActivity(browserActivities)
}
