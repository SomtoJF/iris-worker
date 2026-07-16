# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Development Commands

```bash
# Start Temporal server (Docker)
make start-temporal-server

# Start worker with hot-reload
make start-worker

# Start worker with fixed CDP port for Chrome DevTools MCP observation
make start-worker-observe

# Build binary
go build -o iris-worker main.go

# Run worker
go run main.go

# Clean up Docker
make clean
```

## Architecture

### Core Pattern: Temporal Workflow System
iris-worker is a Temporal worker that executes job application workflows. The architecture follows Temporal's separation of workflows (orchestration) and activities (execution).

### Key Components

**main.go**: Entry point that:
- Initializes SQLite DB connection (global `init()`)
- Creates `Dependencies` container (AIPI client, browser factory, temp filesystem)
- Registers workflow and activities to task queue `job-application`
- Starts Temporal worker

**common/dependencies.go**: Dependency injection container providing:
- `AIPIClient` - LLM interface via OpenRouter
- `BrowserClient` - Browser automation via go-rod
- `TemporaryFileSystem` - Temp file management with cleanup
- All dependencies initialized via `MakeDependencies()` and cleaned up via `Cleanup()`

**Workflows** (workflow/):
- Live in `workflow/` directory, organized by domain (e.g., `jobapplication/`)
- Pure orchestration - no direct I/O, only activity calls
- Helper functions in `helper.go`, main workflow logic in `workflow.go`
- `jobapplication/JobApplicationWorkflow`: Agentic loop (max 20 iterations) that plans actions via LLM and executes tool calls until application complete

**Activities** (activity/):
- Grouped by responsibility: `llm/`, `sqldb/`
- Each activity package exports a struct with methods (e.g., `sqldb.Activity`, `llm.Activity`)
- Registered to worker via `NewActivities(deps)` constructor pattern
- `sqldb.Activity`: DB operations (UpdateJobApplication)
- `llm.Activity`: LLM completions via AIPI

**AIPI Client** (aipi/):
- Abstraction over LLM providers (currently OpenRouter)
- `aipi/types/types.go`: Common request/response types (AIPIRequest, AIPIResponse)
- `aipi/openrouter/provider.go`: OpenRouter implementation with:
  - Message building (system/user/image messages)
  - JSON schema response formatting
  - Token usage and cost tracking with model pricing table
  - Cost calculation per input/output tokens
- `aipi/client.go`: Main client interface

**Browser Factory** (browserfactory/):
- `BrowserFactory`: Wraps go-rod browser instance
- `ScreenshotForLLM()`: Captures page screenshots with:
  - Transparent grid overlay
  - Accessibility tree extraction
  - Interactive element tagging (buttons, links, inputs) with numeric labels
  - Returns screenshot path + tagged nodes with descriptions
- Used for visual LLM context in workflows

**Initializers** (initializers/):
- `sqldb/`: Global DB connection to SQLite at `~/iris/db/gorm.db`
- `fs/`: Temporary filesystem with auto-cleanup (`os.MkdirTemp` wrapper)

### Data Models

**JobApplication** (activity/sqldb/activity.go):
- Status: `processing`, `applied`, `failed`
- Tracked by `id_job_application` (uint primary key)
- External ID via UUID (`id_external`)

### Workflow Execution Pattern

JobApplicationWorkflow demonstrates the standard pattern:
1. Set activity options (timeout, retry policy) via `workflow.WithActivityOptions`
2. Execute activities via `workflow.ExecuteActivity(ctx, "ActivityName", input).Get(ctx, &result)`
3. Use helper functions (e.g., `updateJobApplicationStatus`) to wrap activity calls
4. Structure complex workflows with helper functions in separate file

### Environment Variables

Required:
- `OPENROUTER_API_KEY`: OpenRouter API key for LLM access

## Complex Tasks
Split complex tasks into smaller subtasks. Delegate subtasks to subagents running on Claude Opus 4.8 (`model: opus`). Main agent (Fable 5) acts as orchestrator only — coordinates, reviews results, doesn't do subtask work itself.

## Vendoring Rule

Deploy builds (docker/Dockerfile) compile with `-mod=vendor` and never hit the network — the `replace github.com/SomtoJF/iris-api => ../iris-api` in go.mod only resolves locally. After changing worker deps OR anything in `iris-api/model`, run `go mod vendor` here and commit the vendor/ changes, otherwise the deploy builds a stale copy or fails.

## Observing the local job-application browser (debugging)

The worker drives Chromium via go-rod. For local testing/debugging you can attach Chrome DevTools MCP to the same browser the agent uses and watch pages, screenshots, DOM, console, and network while a `JobApplicationWorkflow` runs.

### 1. Start the worker with a fixed CDP port

```bash
make start-worker-observe
```

This is the same as `make start-worker` (headed + trace + slow-mo) but pins Chrome remote debugging on **port 9222**.

Confirm CDP is up:

```bash
curl -s http://127.0.0.1:9222/json/version
```

You should see a JSON payload with `webSocketDebuggerUrl`. An empty `http://127.0.0.1:9222/json/list` is normal until a workflow opens a tab.

### 2. Point Chrome DevTools MCP at that browser

In `~/.cursor/mcp.json` (or your Cursor MCP config), connect instead of launching a separate Chrome:

```json
{
  "mcpServers": {
    "chrome-devtools": {
      "command": "npx",
      "args": [
        "chrome-devtools-mcp@latest",
        "--browserUrl",
        "http://127.0.0.1:9222"
      ]
    }
  }
}
```

Restart the MCP server in Cursor after changing this. MCP tools will fail if the observe worker is not running.

### 3. Watch a run

1. Keep `make start-worker-observe` running (Temporal on `localhost:7233`, Redis, etc. as usual).
2. Trigger a job application (API `POST /jobs/apply`, client UI, or Temporal `JobApplicationWorkflow` on task queue `job-application`).
3. From the agent/IDE, use Chrome DevTools MCP:
   - `list_pages` — tabs the worker opened
   - `select_page` — focus the application tab
   - `take_screenshot` / `take_snapshot` — see what the agent sees
   - `list_console_messages` / `list_network_requests` — debug page failures

Workflow tabs appear when `OpenWebpage` runs and disappear when the session closes the page. Poll `list_pages` during the agent loop; short-lived navigations can close before a single screenshot lands.

### Notes

- Use `make start-worker` for normal local headed debugging without a fixed CDP port.
- Do not use `start-worker-observe` in production/Docker; that path stays headless without an exposed debug port.
- Multiple CDP clients can attach to the same Chromium; go-rod keeps control while MCP observes.

## Iterative debugging with Temporal CLI + browser

When fixing job-application failures, run a tight observe → diagnose → fix → re-run loop until the workflow completes. Prefer the project skill `.cursor/skills/debug-job-application/SKILL.md` for the full checklist.

### Tools

1. **Temporal CLI** (`temporal`) — workflow status, pending activities, event history, activity inputs/outputs/failures.
2. **Chrome DevTools MCP** (via `make start-worker-observe`) — live page state while the agent runs.

### Typical loop

```bash
# 1. Observe worker (CDP :9222)
make start-worker-observe

# 2. Start a uniquely-id'd run
WF_ID="job-app-debug-$(date +%s)"
temporal workflow start \
  --address localhost:7233 \
  --namespace default \
  --task-queue job-application \
  --type JobApplicationWorkflow \
  --workflow-id "$WF_ID" \
  --input '{"url":"...","id_user":1,"id_resume":1,"id_job_application":19,"application_external_id":"..."}'

# NEVER rerun successful (applied) applications — only rerun failed ones.
# Known-good sample input (Greenhouse / Stratolaunch):
# {"url":"https://job-boards.greenhouse.io/stratolaunch/jobs/5345941008","id_user":1,"id_resume":1,"id_job_application":20,"application_external_id":"4d11381f-8e6b-4d90-91be-523697045be4"}

# 3. Watch status / failures
temporal workflow describe --address localhost:7233 --workflow-id "$WF_ID"
temporal workflow show --address localhost:7233 --workflow-id "$WF_ID" --follow
temporal workflow show --address localhost:7233 --workflow-id "$WF_ID" --output json > /tmp/wf-history.json

# 4. On failure: inspect ActivityTaskScheduled input + Failed message, fix code, cancel if needed, start a new WF_ID
temporal workflow cancel --address localhost:7233 --workflow-id "$WF_ID"
```

Use history JSON to correlate planner `element_index` values with `TakeScreenshot` tagged nodes and any `Type`/`Click`/`TypeMultiple` errors. Do not assume tagged-node slice position equals the painted index.
