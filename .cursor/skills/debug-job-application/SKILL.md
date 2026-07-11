---
name: debug-job-application
description: >-
  Iteratively debug iris-worker JobApplicationWorkflow runs using Temporal CLI
  (activity failures, inputs/outputs) and Chrome DevTools MCP browser observation.
  Use when debugging job applications, watching workflows fail, fixing browser
  activities, captcha, TypeMultiple/Click, or when the user asks to run a job
  application in a fix loop until it succeeds.
---

# Debug Job Application Workflow

Iteratively run a job application, observe failures via Temporal CLI + headed browser, fix one issue at a time, and repeat until the workflow completes successfully.

## Prerequisites

- Temporal server reachable at `localhost:7233` (`make start-temporal-server` if needed)
- Worker with fixed CDP port: `make start-worker-observe` (from `iris-worker`)
- Chrome DevTools MCP pointed at `http://127.0.0.1:9222` (see `CLAUDE.md`)
- `temporal` CLI installed

Confirm:

```bash
curl -s http://127.0.0.1:9222/json/version
temporal operator cluster health --address localhost:7233
```

## Loop

```
Task Progress:
- [ ] Start (or confirm) observe worker
- [ ] Start JobApplicationWorkflow with a unique workflow id
- [ ] Monitor with temporal CLI until failure or completion
- [ ] Inspect failing activity inputs/outputs/errors
- [ ] Optionally confirm page state via Chrome DevTools MCP
- [ ] Fix the root cause (one issue)
- [ ] Wait for CompileDaemon rebuild / restart worker if needed
- [ ] Cancel leftover run if still open; start a fresh workflow
- [ ] Repeat until Status=COMPLETED and application submitted
```

Do **not** stop after the first fix. Keep looping until the application goes through (or the user stops you).

## Start a run

Use a unique workflow id every attempt:

```bash
WF_ID="job-app-debug-$(date +%s)"
temporal workflow start \
  --address localhost:7233 \
  --namespace default \
  --task-queue job-application \
  --type JobApplicationWorkflow \
  --workflow-id "$WF_ID" \
  --input '{
    "url":"https://jobs.ashbyhq.com/qdrant.tech/99a18cbe-0337-43f4-b087-c6e24d556bdb",
    "id_user":1,
    "id_resume":1,
    "id_job_application":19,
    "application_external_id":"6a499b13-6546-459c-8396-309011a692aa"
  }'
echo "$WF_ID"
```

Adjust the JSON input when the user supplies different values.

## Monitor with Temporal CLI

**Status / pending activities:**

```bash
temporal workflow describe --address localhost:7233 --workflow-id "$WF_ID"
```

**Live history (follow):**

```bash
temporal workflow show --address localhost:7233 --workflow-id "$WF_ID" --follow
```

**Full history as JSON (best for debugging activity I/O):**

```bash
temporal workflow show --address localhost:7233 --workflow-id "$WF_ID" --output json > /tmp/wf-history.json
```

When an activity fails, extract from history:

1. `ActivityTaskScheduled` — activity name + **input** payload
2. `ActivityTaskFailed` / `TimedOut` — **failure message**, stack, timeout type
3. Nearby `ActivityTaskCompleted` — successful **outputs** for context (e.g. prior `TakeScreenshot` tagged node count vs `TypeMultiple` indices)

Useful filters while running:

```bash
temporal workflow describe --address localhost:7233 --workflow-id "$WF_ID" \
  | grep -E 'Status|Pending Activities|Type |Attempt|LastFailure|CloseTime'
```

**Cancel a stuck run before the next attempt:**

```bash
temporal workflow cancel --address localhost:7233 --workflow-id "$WF_ID"
```

## Observe the browser

With `make start-worker-observe` and Chrome DevTools MCP:

- `list_pages` / `select_page` — find the application tab
- `take_screenshot` / `take_snapshot` — compare what the agent sees vs Temporal activity I/O
- Use this to validate captcha state, form fill, wrong clicks, etc.

Browser observation complements Temporal; prefer Temporal for *why* an activity failed, MCP for *what the page looked like*.

## Fix discipline

1. Identify **one** concrete failure from Temporal (activity name + error + relevant input).
2. Fix the root cause in code — do not paper over with unrelated retries.
3. Element indices: resolve by painted `Index` on tagged nodes from the **last `TakeScreenshot`**, never assume slice position `0..n-1` equals planner indices, and do not re-tag before Click/Type (that remaps indices).
4. After CompileDaemon rebuilds, start a **new** workflow id (do not resume a failed run unless intentionally testing replay).
5. After CompileDaemon rebuilds the worker, **cancel** the in-flight run and start a new workflow id — browser sessions live in-process and die on worker restart.
6. Prefer `make start-worker` (no fixed CDP) for submission attempts; use `start-worker-observe` when you need Chrome DevTools MCP. Attached CDP can increase bot-detection risk on some ATS sites (e.g. Ashby spam flags).
7. Log each loop iteration briefly: failure → fix → next run id.

## Known failure patterns

| Symptom | Likely cause | Fix direction |
|---|---|---|
| `index out of range` in Type/Click | Re-tag remapped indices vs planner screenshot | Resolve by painted `Index` from last `TakeScreenshot` cache; do not re-tag before interact |
| `this.select is not a function` / not editable | Planner typed into radio/button | Reject non-editable targets; radios/checkboxes need `click` |
| Click StartToClose timeout | rod WaitInteractable/stable hang | Bound timeout + JS `element.click()` fallback |
| CapSolver `check ... invisible` | v2 invisible not passed to CapSolver | Pass `isInvisible: true` from DetectCaptcha |
| Scrape empty / bad JSON job details | Serper down + Colly can't render SPA | Prefer `GetPageText` from open browser page |
| Ashby "flagged as possible spam" | ATS bot filter / repeated test submits | Fill all required fields first; retry once; if persistent, try without CDP observe and/or a fresh applicant identity |

## Success criteria

- `temporal workflow describe` shows `COMPLETED`
- No unrecovered activity panics
- Application reached a submitted / applied terminal state for the job (or user-confirmed success)
