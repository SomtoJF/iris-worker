package captcha

// SolveWithCapSolverInput carries the detected captcha args to the solver.
type SolveWithCapSolverInput struct {
	Type      string `json:"type"`     // recaptcha_v2 | recaptcha_v3 | turnstile
	SiteKey   string `json:"site_key"`
	PageURL   string `json:"page_url"`
	Action    string `json:"action"`    // recaptcha v3 only
	Invisible bool   `json:"invisible"` // recaptcha v2 invisible
}

type SolveWithCapSolverOutput struct {
	Token string `json:"token"`
}
