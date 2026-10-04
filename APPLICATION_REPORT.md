# Monthly Application Reports

## Goal

Let a user generate one AI-analyzed spreadsheet report for each eligible past calendar month, then download that saved report whenever needed without calling Decide again.

The workbook is generated programmatically from Iris data first. Decide adds analysis to the workbook asynchronously. Generation and download are separate actions.

## User experience

- Show eligible months from the user's signup month through the last completed calendar month. Never offer the current month or a future month.
- A user selects a month and starts report generation. If a report already exists for that month, show its state and offer download when ready; do not start another report.
- Notify the user when generation finishes, for example: “Your report for October 2026 is ready for download.” Notify clearly if generation fails.
- Download is a separate action. The client requests a short-lived S3 download URL from Iris and downloads the saved file directly.
- Keep the report available for repeat downloads. Do not regenerate a completed report or call Decide again for that user/month.

## Report contents

Include applications applied to during the selected month, using `AppliedAt` as the month-selection date. Applications without an `AppliedAt` date are not included.

The initial workbook should include:

- An application detail sheet: company, role, job URL, applied date, application status, response status, and relevant application data.
- Summary sheets with counts and trends by response status, application status, role, company, and month.
- Time-to-outcome metrics derived from response-status history: elapsed time from `AppliedAt` to the first `interviewing` transition and to the first `rejected` transition, where those transitions exist.

Treat analysis and any recommendations returned by Decide as AI-generated insights, not authoritative facts. Preserve the original programmatically generated workbook separately from Decide's analyzed output.

## Data model

### Response status changelog

Add an append-only response-status changelog table associated with a job application and user. Each row records the prior status, new status, change timestamp, and change source. Insert a row in the same database transaction as every response-status change so the current application state and its history cannot diverge.

Use this history to power an application timeline and report outcome durations. Do not invent historical transitions for records that predate changelog tracking; their timeline begins with the first recorded change.

### Application report

Add an application-report table with:

- Report ID and user ID.
- Calendar year and month, with month stored as an integer from **1 to 12**.
- Generation state (`pending`, `processing`, `ready`, or `failed`) and any useful failure detail.
- A JSON/JSONB snapshot of the raw report data used to build the workbook.
- S3 object keys for the original workbook and the analyzed downloadable workbook.
- Creation and update timestamps, plus completion time if useful.

Enforce a unique constraint on `(user_id, year, month)`. The report row is the durable source of truth for generation state and report availability. A failed attempt may be retried for the same report row and snapshot; a ready report is immutable and must never trigger another Decide run.

## Asynchronous generation and download flow

1. The client loads eligible months and existing report states from Iris.
2. On Generate, the API authenticates the user, validates that the requested month is eligible and complete, and creates or finds the unique report row. It snapshots that user's applications and response history for the selected month.
3. The API starts a Temporal report workflow using the report ID as its idempotency key and returns the report ID and current state.
4. The workflow builds the original workbook from the stored snapshot, uploads it to S3, sends it to Decide for analysis, and stores the resulting downloadable workbook in S3.
5. The workflow updates the report row to `ready` (or `failed`) and publishes a user-scoped Redis event through the existing SSE path. The event identifies the report and month; it does not contain a download URL.
6. The client listens for report-ready/failed events and refreshes that report from the API. On reconnect or page load, the client fetches report state again because Redis Pub/Sub events are transient.
7. On Download, the client calls a separate authenticated API endpoint. The API verifies report ownership and ready state, then returns a short-lived presigned S3 URL. The client downloads the stored file directly.

Never expose the Decide API key to the client or extension. Never rely on a Redis event as the only record that a report completed.

## Implementation plan

### 1. Record response-status history

- Add the changelog model and migration in `iris-api`, following the existing GORM migration conventions.
- Update every API path that changes a job's response status to append a changelog row transactionally with the status update.
- Review non-API writers and ensure they use the same persistence rule if they can change response status.
- Add a user-authorized endpoint for fetching a job's ordered timeline and include that timeline in the application detail experience.
- Do not fabricate backfilled transition timestamps for existing records.

### 2. Add report persistence and API contracts

- Add the report model, uniqueness constraint, and migration.
- Add authenticated endpoints to list eligible months/report states, create-or-return a monthly report, get report state, and request a download URL.
- Enforce ownership on every operation. Validate month range `1..12`, signup-month eligibility, and that the requested month is before the current month.
- Snapshot report inputs when the report is created so retries use the same data.
- Keep report creation idempotent under concurrent requests; an existing ready report is returned rather than regenerated.

### 3. Build the report workflow

- Add a Temporal workflow and activities in `iris-worker` for loading the stored snapshot, building the workbook, uploading the original workbook, invoking Decide, storing the analyzed output, updating report state, and publishing completion/failure events.
- Use bounded retries, explicit timeouts, and a deterministic workflow ID based on the report ID.
- Keep the original workbook if Decide fails, and expose failure state rather than presenting a partial result as ready.
- Store artifacts under user-scoped S3 keys and persist only object keys, never long-lived download URLs.

### 4. Add client experience

- Add an application-reports view where users select an eligible past month and see report generation states.
- Listen for report-ready/failed events through the existing SSE realtime provider, then refresh state from the API.
- Make Download available only for ready reports; request a presigned URL on click and download the existing artifact.
- Show an existing report instead of offering a second generation action for the same month.

### 5. Validate end to end

- Test month eligibility, signup boundary, month `1..12` validation, and user isolation.
- Test concurrent generation requests create one report/workflow and that ready reports never call Decide again.
- Test changelog writes are atomic with response-status updates, and duration metrics use the first matching transition.
- Test workflow success/failure, retries, transient/missed SSE events, and repeat downloads.
- Confirm report and workbook data are not exposed across users and the Decide key remains server-side.

## Repository integration notes

- `iris-api` already has S3 upload and presigned-download helpers, Redis Pub/Sub, an authenticated SSE endpoint, Temporal workflow startup patterns, and a GORM migration registry.
- `iris-worker` already publishes user-scoped Redis events from Temporal workflows and has S3 activities/utilities.
- `iris-client` already has an SSE realtime provider and listens to realtime actions in the UI.
- If any shared types in `iris-api/model` change, regenerate `iris-worker/vendor` with `go mod vendor` as required by the worker build setup.
- Before running a migration, leave only the newly affected tables enabled in `migrate/migrate.go`.

## Decisions and assumptions

- Months are stored and sent by the API as integers `1..12`.
- Eligibility is based on complete calendar months, from the signup month through the previous month; month boundaries use UTC unless Iris adds a user timezone.
- Report membership is based on `AppliedAt`, not application creation date or the date a response arrived.
- A completed report is a point-in-time snapshot and is immutable. Failed reports can retry against the same stored snapshot; completed reports cannot be regenerated.
- Decide's analyzed workbook is the downloadable artifact; the original workbook is retained separately for traceability and recovery.
