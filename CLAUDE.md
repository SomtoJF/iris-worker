# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Development Commands

```bash
# Start Temporal server (Docker)
make start-temporal-server

# Start worker with hot-reload
make start-worker

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
