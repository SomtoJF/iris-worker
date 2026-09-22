package browserpool

type ApplicationQueueItem struct {
	IdJobApplication uint   `json:"id_job_application"`
	Url              string `json:"url"`
	IdUser           uint   `json:"id_user"`
	IdResume         uint   `json:"id_resume"`
}
