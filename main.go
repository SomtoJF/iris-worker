package main

import (
	"log"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/llm"
	s3Activities "github.com/SomtoJF/iris-worker/activity/s3"
	sqldbActivities "github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/common"
	"github.com/SomtoJF/iris-worker/initializers/env"
	"github.com/SomtoJF/iris-worker/workflow/jobapplication"
	"github.com/SomtoJF/iris-worker/workflow/processresume"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

type TaskQueueName string

const (
	JobApplicationTaskQueueName TaskQueueName = "job-application"
)

func init() {
	err := env.LoadEnvVariables()
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

	loadTemplates()

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
	w.RegisterWorkflow(processresume.ProcessResumeWorkflow)
}

func registerJobApplicationActivities(w worker.Worker, dependencies common.Dependencies) {
	db := dependencies.GetDB()
	s3Manager := dependencies.GetS3Manager()
	aipiClient := dependencies.GetAIPIClient()
	browserClient := dependencies.GetBrowserClient()

	sqldbActivities := sqldbActivities.NewActivities(db)
	w.RegisterActivity(sqldbActivities)

	llmActivities := llm.NewActivity(aipiClient)
	w.RegisterActivity(llmActivities)

	browserActivities := browser.NewActivities(browserClient)
	w.RegisterActivity(browserActivities)

	webActivities := web.NewActivity()
	w.RegisterActivity(webActivities)

	s3Activity := s3Activities.NewActivity(s3Manager)
	w.RegisterActivity(s3Activity)
}

func loadTemplates() {
	jobapplication.SetTemplates()
}
