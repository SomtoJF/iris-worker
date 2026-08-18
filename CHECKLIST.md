# TODO list of things that need to be done

## Urgent

- [x] Add chrome extension link to the marketing page and client dash
- [x] Add name field to resumes
- [ ] View resumes by display name in extension
- [ ] Page changes within the same tab shouldnt clear extension scan memory
- [ ] Disable automated apply to every job that is not from greenhouse

## Others

- [ ] Implement email notifications on successful/failed applications, comments on feedback and user action required. (Mailjet)
- [x] Figure out captcha solving
  - [x] Turnstile + reCAPTCHA v2/v3 detect + CapSolver solve (working)
  - [x] hCaptcha detect + inject + buildTask (HCaptchaTaskProxyless — exact lowercase "less") wired end-to-end
  - [ ] **BLOCKED (account, not code): hCaptcha solving not enabled on the CapSolver account.**
    - Symptom: worker fails Lever apps with failure_status=CAPTCHA; SolveWithCapSolver → createTask 400
      `ERROR_INVALID_TASK_DATA` / "We don't support this service. Please contact support..."
    - Diagnosis: getBalance works ($5.99, key valid); reCAPTCHA type validates sitekey; hCaptcha type is
      _recognized_ but refused ("We don't support this service", and a known-good hCaptcha sitekey returns a
      usage-policy block) → hCaptcha is a separately-gated service on the plan. Distinct from a bad type name,
      which returns "This service is not supported: ".
    - Fix: enable hCaptcha on the CapSolver account (or swap to a key/plan that includes it). No code change.
    - Detection/inject/mapping already correct in activity/browser/captcha.go + activity/captcha/activity.go.
- [x] Implement auto-resume suggestion
- [ ] Re-evaluate submission detection logic. Maybe just have the LLM figure it out. --Wait until the page is stable (network request has been made) and immediately take the screenshot. LLM checks if the form inputs are partially filled (usually means failure) or success messages or if we're on a different page than the application page with the form (like the job description page) or if the new url contains success indicators
- [ ] Indeed Integration
- [ ] Make sure automated Apply works well for the following job boards:
  - [x] Greenhouse (job-boards.greenhouse.io)
  - [ ] Lever (jobs.lever.co) — blocked on hCaptcha (CapSolver account doesn't have hCaptcha enabled; see "Figure out captcha solving")
  - [ ] Wellfound (wellfound.com)
  - [ ] Workable (apply.workable.com)
  - [ ] Ashby (jobs.ashbyhq.com)
  - [ ] Remotefront (remotefront.com)
- [ ] Make sure assisted (extension) Apply works well for the following job boards:
  - [x] Greenhouse (job-boards.greenhouse.io)
  - [ ] Lever (jobs.lever.co) — blocked on hCaptcha (CapSolver account doesn't have hCaptcha enabled; see "Figure out captcha solving")
  - [ ] Wellfound (wellfound.com)
  - [ ] Workable (apply.workable.com)
  - [ ] Ashby (jobs.ashbyhq.com)
  - [ ] Remotefront (remotefront.com)
