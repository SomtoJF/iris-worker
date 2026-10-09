package types

import "time"

const QUEUE_APPLICATION_SIGNAL_NAME = "queue_application"
const CANCEL_APPLICATION_SIGNAL_NAME = "cancel_application"
const BROWSER_POOL_APPLICATION_SETTLED_SIGNAL_NAME = "application_settled"
const MAX_CONCURRENT_APPLICATIONS = 4
const BROWSER_POOL_ROLLOVER_TIMEOUT = 7 * 24 * time.Hour

type ApplicationQueueItem struct {
	IdJobApplication      uint   `json:"id_job_application"`
	Url                   string `json:"url"`
	IdUser                uint   `json:"id_user"`
	IdResume              uint   `json:"id_resume"`
	ApplicationWorkflowId string `json:"application_workflow_id"`
	NewReplayGeneration   bool   `json:"new_replay_generation,omitempty"`
	ResumeUserActionID    uint   `json:"resume_user_action_id,omitempty"`
}

type BrowserPoolApplicationSettledPayload struct {
	IdJobApplication uint `json:"id_job_application"`
}

type CancelApplicationPayload struct {
	IdJobApplication uint   `json:"id_job_application"`
	Reason           string `json:"reason"`
}
