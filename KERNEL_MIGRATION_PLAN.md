# Kernel Browser Migration Plan

## Goal

Replace the worker's local Go-rod browser backend with Kernel-managed browsers without changing the application API or the Temporal workflow contract. Keep Go-rod available as a development/fallback provider while Kernel is validated.

Kernel is a browser provider, not the application scheduler. The existing `BrowserPoolWorkflow` is a durable Temporal queue and concurrency limiter; it does not create, reserve, or recycle browser instances.

## Current architecture

1. `POST /jobs/apply` resolves the user's resume, writes a `pending` application row and a stable application workflow ID, then starts `InitiateApplicationWorkflow` with autonomous apply enabled. It returns `202` without waiting for application completion.
2. `InitiateApplicationWorkflow` loads the application, scrapes and validates the posting, extracts and persists its details, signals `queue_application` to the configured browser-pool workflow, then marks the row `queued`.
3. `BrowserPoolWorkflow` deduplicates by application ID, queues requests, and uses a deterministic semaphore capped at four active applications. For each available slot it starts a `JobApplicationWorkflow` child with the application workflow ID and waits for its completion.
4. Each `JobApplicationWorkflow` creates a Temporal session and drives browser activities through the worker's browser dependencies. Its child execution timeout and session/soft timeout are both 30 minutes. It persists terminal application state and sends `application_settled` to the pool when it exits.
5. The API cancellation endpoint first conditionally changes an eligible application row to `cancelled`, then asynchronously signals `cancel_application` to the pool. The pool removes queued work or forwards `CANCEL_APPLICATION` to the active application workflow.

The worker registers `BrowserPoolWorkflow` on the `job-application` task queue and starts it at startup using `BROWSER_POOL_WORKFLOW_ID`, which must match the API configuration. Additional worker replicas tolerate the pool already running.

The browser pool rolls over after seven days or when its history exceeds 10,000 events or 10 MiB, whichever comes first. It drains signals already waiting in its channels, then uses Continue-As-New with:

- `InitialQueue`: the remaining queue snapshot.
- `ActiveApplications`: the active application records, including their application workflow IDs.

The next pool run rebuilds the queue and reacquires one semaphore slot per restored active record. The child application workflows are started with `PARENT_CLOSE_POLICY_ABANDON`, so Continue-As-New does not cancel them. They continue independently; they are not restarted or migrated as part of the rollover. Their Temporal execution histories and browser/session state remain with those child workflows, not in the pool's Continue-As-New input. The pool only carries scheduling metadata.

## Kernel migration design

### 1. Replace the browser implementation behind activities

Keep workflow orchestration browser-provider agnostic. Add Kernel support at the existing browser dependency/activity boundary rather than introducing a second scheduler or placing Kernel API calls in workflows. Preserve the activity behavior required by `JobApplicationWorkflow` (open page, screenshots/tagged nodes, browser actions, and close page).

Create one isolated Kernel browser/session per active application workflow. Bind its identity and cleanup lifecycle to the application workflow ID. Enforce Kernel account concurrency limits consistently with the pool's current maximum of four; configuration must not allow the scheduler to exceed the provider limit.

Keep Go-rod for local development and an explicitly selected fallback. Provider selection and Kernel credentials belong in worker configuration/dependency construction, not in API requests.

### 2. Preserve API and workflow inputs

Keep the existing API contract and workflow inputs stable during the provider migration. The pool queue item continues to carry the application ID, URL, user ID, resume ID, and application workflow ID. Do not move job details, browser handles, cookies, or other non-deterministic browser state into Temporal workflow input/history.

The extension endpoints are a separate flow: extension `InitiateApplication` starts the initiate workflow with autonomous apply disabled, and extension autofill starts its own workflow. They do not enqueue an autonomous application in the browser pool and should not be coupled to the Kernel migration unless they independently need browser access.

### 3. Browser lifecycle and cleanup

Open or attach to the Kernel session in browser activities and close it on normal completion, application failure, cancellation, and timeout. Cleanup must be idempotent and use a disconnected Temporal context when the application context has been cancelled. Configure a provider-side TTL/lease as a backstop: a worker crash cannot run workflow defer/cleanup code, and a local browser/session may outlive the worker process.

Do not treat the pool's concurrency slot as proof that a remote browser exists. A failed create/connect must fail the application through the normal workflow error path and eventually release the pool slot. Retried activities must not create unbounded orphan sessions; use a stable per-application session key or explicitly close any session before retrying creation.

## Failure, restart, rollover, and cancellation behavior

### Worker or workflow-task crash and replay

Temporal workflow state is reconstructed by replaying its event history after a worker/process restart. The pool's in-memory queue, deduplication set, active map, semaphore, and timers are deterministic workflow state; they are not an external in-memory browser pool. A normal worker restart should therefore resume the same workflow execution and continue from history. Activity side effects still need idempotency and retries.

A workflow execution that is failed, terminated, or otherwise closed is different from a worker restart: replay will not revive a closed execution. The current pool code has no recovery loop that discovers/restarts a closed pool or reconciles queued database rows. The API and worker rely on the same configured pool workflow ID. Worker startup starts the pool if it is not already running; a worker restart resumes the same open execution, while a closed execution can be started again. This does not provide reconciliation for queued database rows or failed signals.

If an activity/worker dies while a Kernel browser is open, Temporal can retry the activity/workflow, but the external browser is not part of workflow history. Kernel session lookup, reconnect, expiry, and orphan cleanup need defined behavior. Never assume browser state can be recreated just by replaying workflow code.

### Pool rollover timeout and history limits

The seven-day timer and history thresholds trigger a controlled Continue-As-New; they are not queue or application timeouts. The pool drains signals already buffered for queueing and cancellation, completion signals, and child completions before snapshotting state.

Queued applications transfer in FIFO snapshot order and are deduplicated again in the new run. Active application records transfer in sorted ID order; the new run reacquires their semaphore slots. Active children are left running by `PARENT_CLOSE_POLICY_ABANDON`; after completion they signal the pool by workflow ID with an empty run ID, so the signal targets the current execution in the Continue-As-New chain.

Signals arriving at the rollover boundary are the key race: only signals drained before the old run returns are in its snapshot. Callers must handle signal failures/retry safely, and enqueue must be idempotent. Add an integration test for queue, cancel, and settle signals racing with Continue-As-New. Do not mark rollover complete until those signals are either transferred or durably retried.

### Application timeout and rollover interaction

Pool rollover does not extend or reset an active application's timeout. An application child can outlive multiple pool runs, while its own execution/session timeout continues. Both limits are 30 minutes, so this is the effective upper bound for an application.

### Cancellation cases

- **Queued:** the API changes the database status first; the pool removes the item and releases no active slot because it never acquired one.
- **Active:** the pool forwards `CANCEL_APPLICATION`; the child cancels its session context, performs disconnected cleanup, and publishes the cancellation event. The API already wrote the `cancelled` status/reason.
- **Repeated/late cancel:** make both pool removal and child cancellation idempotent. If the item is already settled or unknown, the pool currently does nothing.
- **Signal failure:** cancellation signaling runs asynchronously in the API and only logs failures. The API can return `202` although the workflow was not signaled. Add retry/reconciliation so a database row cannot remain cancelled while a live workflow continues.
- **Cancel during initiation:** the API can mark an application cancelled before the initiate workflow sends its queue signal. Today the pool may not yet know about the application, so its cancellation signal is a no-op; initiation can subsequently enqueue it. Before dispatch, verify that the application remains eligible, or persist/reconcile cancellation so this race cannot start a cancelled application.
- **Cancellation around rollover:** drain buffered cancel signals before snapshot, and ensure a cancellation sent to the old run during Continue-As-New is retried or delivered to the new run. Keep database status authoritative and do not rely on in-memory pool state as the source of truth.

### State ownership and consistency

- The database owns user-visible application status, reasons, and extracted job details.
- The pool workflow owns only queue membership and active-slot bookkeeping. Continue-As-New transfers these explicitly; they are not stored in an external browser pool.
- Each child workflow owns its execution progress and its current browser/session interaction. Pool rollover does not copy that progress.
- Kernel owns remote browser resources. Use an application-scoped stable key/TTL so a child can reconnect after worker restart and abandoned resources eventually expire.

Queue delivery is currently signal-based rather than transactional with the database. A failed enqueue signal can leave an application row pending/queued without pool work; a crash between enqueue and the database status update can also make the visible state lag execution. Add idempotent queueing and a reconciliation path that compares nonterminal database rows with pool/application workflow state.

## Rollout and validation

1. Confirm the worker and API use the same `BROWSER_POOL_WORKFLOW_ID`; worker startup registers and starts the pool, but a health/recovery check for a missing or closed pool is still useful.
2. Implement and test Kernel browser activities with per-application session identity, cleanup, reconnect, and TTL behavior. Keep Go-rod selectable for local development.
3. Add workflow tests for worker replay, enqueue deduplication, Continue-As-New with both queued and active applications, late completion signals, and each cancellation race above.
4. Run a staged rollout with a small concurrency cap. Compare browser creation/cleanup, application outcomes, leaked sessions, and queue/status reconciliation before increasing concurrency.
5. Remove Go-rod only after Kernel paths and rollback behavior are proven in production.

## Open decisions

- What durable mechanism will retry failed enqueue/cancel signals and reconcile nonterminal database rows?
- What Kernel session lookup, reuse, and TTL guarantees are available for recovery after worker crashes?
