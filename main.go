package main

import (
	"log"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/captcha"
	"github.com/SomtoJF/iris-worker/activity/llm"
	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	s3Activities "github.com/SomtoJF/iris-worker/activity/s3"
	sqldbActivities "github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/common"
	"github.com/SomtoJF/iris-worker/initializers/env"
	"github.com/SomtoJF/iris-worker/workflow/coverletter"
	"github.com/SomtoJF/iris-worker/workflow/handleuseraction"
	"github.com/SomtoJF/iris-worker/workflow/jobapplication"
	"github.com/SomtoJF/iris-worker/workflow/jobdiscovery"
	"github.com/SomtoJF/iris-worker/workflow/processresume"
	"github.com/SomtoJF/iris-worker/workflow/submitapplication"
	"github.com/SomtoJF/iris-worker/workflow/summarizeissue"
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

	temporalClient := dependencies.GetTemporalClient()

	loadTemplates()

	w := worker.New(temporalClient, string(JobApplicationTaskQueueName), worker.Options{
		EnableSessionWorker: true,
	})

	registerJobApplicationWorkflows(w)
	registerJobApplicationActivities(w, dependencies)

	// Start listening to the Task Queue.
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("worker failed: ", err)
	}
}

func registerJobApplicationWorkflows(w worker.Worker) {
	w.RegisterWorkflow(jobapplication.JobApplicationWorkflow)
	w.RegisterWorkflow(jobapplication.AutofillApplicationWorkflow)
	w.RegisterWorkflow(jobapplication.InitiateApplicationWorkflow)
	w.RegisterWorkflow(processresume.ProcessResumeWorkflow)
	w.RegisterWorkflow(coverletter.CoverLetterWorkflow)
	w.RegisterWorkflow(submitapplication.SubmitApplicationWorkflow)
	w.RegisterWorkflow(handleuseraction.HandleUserActionWorkflow)
	w.RegisterWorkflow(jobdiscovery.JobDiscoveryWorkflow)
	w.RegisterWorkflow(summarizeissue.SummarizeIssueWorkflow)
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

	webActivities := web.NewActivity(db)
	w.RegisterActivity(webActivities)

	s3Activity := s3Activities.NewActivity(s3Manager)
	w.RegisterActivity(s3Activity)

	realtimeEventActivities := realtimeevent.NewActivities()
	w.RegisterActivity(realtimeEventActivities)

	captchaActivities := captcha.NewActivities()
	w.RegisterActivity(captchaActivities)
}

func loadTemplates() {
	jobapplication.SetTemplates()
	coverletter.SetTemplates()
	handleuseraction.SetTemplates()
	if err := jobdiscovery.SetTemplates(); err != nil {
		log.Fatal(err)
	}
}
