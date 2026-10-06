# Browser Client Design

## Purpose and scope

Replace the go-rod-specific `browserfactory` boundary with a provider-neutral Go `browser` package. Keep go-rod as the selected provider and Kernel available behind the same interface.

## Implementation status

The provider-neutral interface, isolated Rod and Kernel clients, SQL-backed browser/profile/Vault/session records, encrypted Live View persistence, authenticated Live View API, and read-only client view are implemented. Durable user-action submission stores encrypted answers and requeues through the browser pool; the pool also prevents concurrent applications for the same user. Q&A deduplication uses validated JEV decisions with raw-Q&A fallback.

Kernel is not selected by worker dependency wiring. Its browser startup attaches a leased profile and Vault reference, but Managed Auth lifecycle, credential collection/fill, and `NEEDS_AUTH` handoff are not wired. Browser replay catch-up is implemented for safe, semantically identified operations in both providers. It fails closed on missing/ambiguous targets, non-applied or non-replay-safe rows, and uncertain execution; it does not guarantee arbitrary sites can be replayed. Durable user-action resumes still reload the application URL and submit answers as planner context rather than restoring arbitrary paused page state.

The required lifecycle is one isolated browser per job application. The application owns that browser from `OpenWebpage` until application completion, failure, cancellation, timeout, or a user-action pause. Closing the application browser closes the whole browser, not only its current page.

## Current constraints

- `browserfactory` remains as legacy code, but the registered `activity/browser` activities use the provider-neutral `BrowserClient`.
- Rod keeps private browser/page handles in a per-application client; it is process-local and is not recoverable on another worker.
- Browser activity payloads retain `WorkflowID`, which now carries the persisted logical application browser UUID.
- Temporal activity names and payloads are persisted in workflow histories; version gates preserve compatibility for changed workflow behavior.
- `BrowserPoolWorkflow` schedules applications and caps active jobs. It is not a pool of browser instances and serializes active applications per user.

## Package and file structure

```text
browser/
  client.go                   # BrowserClient, ClientType, Config, NewBrowserClient
  types/
    browser.go                # ApplicationBrowserID, options, serializable browser DTOs
    session.go                # SessionRef and SessionStore contracts
  rod/
    client.go                 # RodBrowserClient and NewRodBrowserClient
    session.go                # per-application launch/reconnect/close
    operations.go             # navigation, actions, screenshot, scraping
  kernel/
    client.go                 # KernelBrowserClient and NewKernelBrowserClient
    session.go                # per-application create/reconnect/delete
    operations.go             # Playwright Execution and Computer Controls
    proxy.go                  # Free-plan behavior and future paid geo-proxy setup
activity/
  browser/
    activity.go             # Temporal activity adapters; no backend objects
    types.go                # stable activity inputs/outputs
  sqldb/
    browser_profile.go      # generic provider-tagged user profile references
    browser_vault.go        # generic provider-tagged vault/item references
    browser_auth_connection.go # Managed Auth connection references by domain
    browser_session.go      # logical application browser/session registry
    browser_mutation_changelog.go # per-action mutation replay log
common/dependencies.go        # construct Rod explicitly for now
workflow/jobapplication/      # deterministic identity; pause/requeue recovery
frontend/                     # authenticated Live View URL and iframe events
docs/BROWSERCLIENT.md         # this design
go.mod, go.sum, vendor/       # Kernel SDK when provider is implemented
```

The root `browser` package owns the interface and imports both implementations to construct the selected provider. `browser/rod` and `browser/kernel` import shared contracts from `browser/types`, but neither imports the root package. This avoids an import cycle while allowing the root package to check that each concrete implementation satisfies the interface.

## Public constructors

`NewBrowserClient` is the application-facing construction point. Provider constructors initialize their own SDK/client/config and return their concrete client; per-application browser creation happens later through `StartBrowser`.

```go
// package browser
type ClientType string

const (
    ClientTypeRod    ClientType = "rod"
    ClientTypeKernel ClientType = "kernel"
)

type Config struct {
    SessionStore types.SessionStore
    TempFS       *fs.TemporaryFileSystem
    KernelAPIKey string
    WorkerID     string
}

func NewBrowserClient(clientType ClientType, cfg Config) (BrowserClient, error)

// package browser/rod
type Config struct {
    SessionStore types.SessionStore
    TempFS       *fs.TemporaryFileSystem
    WorkerID     string
}

func NewRodBrowserClient(cfg Config) (*RodBrowserClient, error)

// package browser/kernel
type Config struct {
    SessionStore types.SessionStore
    TempFS       *fs.TemporaryFileSystem
    APIKey       string
}

func NewKernelBrowserClient(cfg Config) (*KernelBrowserClient, error)
```

`common.MakeDependencies()` stays zero-argument and constructs the configured browser client. Worker registration passes `GetBrowserProvider()` to the SQL activities, so application browser IDs are looked up and created per application/provider pair. Switching providers starts a separate browser session and does not migrate the previous provider's browser state. Only when `GetBrowserProvider()` reports Kernel should startup/session code create or load Kernel profiles, Managed Auth connections, and Vaults. Kernel credentials must come from the configured secret source and must never enter workflow input or logs.

## Shared types and browser interface

Shared types are provider-neutral and serializable when they cross an activity/workflow boundary. They must not contain driver handles.

```go
// package browser/types
type ApplicationBrowserID string

type BrowserOptions struct {
    StartingURL string
}

type TaggedNode struct {
    Index       int
    Description string
    X, Y        float64
    Width       float64
    Height      float64
    Role        string
    Value       *string
    Required    *bool
    Checked     *string
}

type TaggedFileInput struct {
    Index int
    HTML  string
    Label *string
    Value *string
}

type Screenshot struct {
    Path                 string
    TaggedNodes          []TaggedNode
    TaggedFileInputNodes []TaggedFileInput
}

type FieldInput struct {
    ElementIndex int
    Text         string
    Replace      bool
}

type Captcha struct {
    Type      string
    SiteKey   string
    PageURL   string
    Action    string
    Invisible bool
    Extra     map[string]string
}

type CaptchaResult struct {
    CallbackFired bool
}

type CapturedRequest struct {
    URL          string
    Method       string
    ResourceType string
    StatusCode   int
    ResponseBody string
}

type SubmissionAttempt struct {
    BeforeURL    string
    NewTabOpened bool
    Requests     []CapturedRequest
}

type SubmissionState struct {
    CurrentURL       string
    URLChanged       bool
    FormPresent      bool
    SuccessText      string
    ValidationErrors []string
    PageText         string
}

const (
    BrowserProviderRod    = "rod"
    BrowserProviderKernel = "kernel"
)
```

`BrowserProviderRod` and `BrowserProviderKernel` are string constants; the root `browser` package re-exports them for client selection. Provider identity is just a name, not a provider object.

The interface operates on an application ID and element indices. The backend retains any Rod element handles or Kernel session details privately.

```go
// package browser
type BrowserClient interface {
    GetBrowserProvider() string
    StartBrowser(ctx context.Context, id types.ApplicationBrowserID, opts types.BrowserOptions) error
    Navigate(ctx context.Context, id types.ApplicationBrowserID, url string) error
    ScreenshotForLLM(ctx context.Context, id types.ApplicationBrowserID, fileName string) (types.Screenshot, error)
    Click(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) error
    Type(ctx context.Context, id types.ApplicationBrowserID, field types.FieldInput) error
    Scroll(ctx context.Context, id types.ApplicationBrowserID, direction string, ratio float64) error
    UploadFile(ctx context.Context, id types.ApplicationBrowserID, fileInputIndex int, filePath string) error
    ScrapeRenderedPage(ctx context.Context, id types.ApplicationBrowserID) (string, error)
    DetectCaptcha(ctx context.Context, id types.ApplicationBrowserID) (types.Captcha, error)
    InjectCaptchaToken(ctx context.Context, id types.ApplicationBrowserID, captchaType, token string) (types.CaptchaResult, error)
    ClickCaptchaButton(ctx context.Context, id types.ApplicationBrowserID, selector string) (bool, error)
    ClickSubmit(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) (types.SubmissionAttempt, error)
    VerifySubmission(ctx context.Context, id types.ApplicationBrowserID, beforeURL string) (types.SubmissionState, error)
    CloseBrowser(ctx context.Context, id types.ApplicationBrowserID) error
}
```

`GetBrowserProvider` reports the selected implementation and gates provider-specific behavior such as Kernel resource setup. `StartBrowser` creates or reconnects the logical application's browser and opens the initial tab at `StartingURL`. `Navigate` handles later navigation. `ClickSubmit` owns the existing new-tab/request-capture behavior. CAPTCHA methods use neutral request/result DTOs, not protocol-specific values. `CloseBrowser` deletes/closes the whole provider browser and is idempotent.

## How an activity selects its browser

Use the stable UUID stored as `ApplicationBrowserID`. Existing activity DTOs carry it in `WorkflowID`; each activity converts it to the named type and calls the shared `BrowserClient` with that ID. The activity does not receive a `*rod.Page`, Kernel session ID, or browser connection URL.

Resolve the logical application browser UUID by job-application ID before creating a browser. If none exists, generate it with Temporal `workflow.SideEffect`, so deterministic replay returns the original UUID rather than calling randomness again; persist it via an idempotent activity and use the ID returned by that activity:

```go
var applicationBrowserID string
var stored ApplicationBrowserIDResult // Found=false only for an absent session; DB errors return as activity errors.
if err := workflow.ExecuteActivity(ctx, "GetApplicationBrowserID", idJobApplication).
    Get(ctx, &stored); err != nil {
    return err
}
if stored.Found {
    applicationBrowserID = stored.ID
} else {
    var generated string
    if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
        return uuid.NewString()
    }).Get(&generated); err != nil {
        return err
    }
    if err := workflow.ExecuteActivity(ctx, "CreateApplicationBrowserID",
        CreateApplicationBrowserIDInput{IdJobApplication: idJobApplication, ID: generated}).
        Get(ctx, &applicationBrowserID); err != nil {
        return err
    }
}
```

`GetApplicationBrowserID` should return a `{Found, ID}` result for the selected provider; database failures remain explicit activity errors. `CreateApplicationBrowserID` must insert-if-absent and return the persisted winner to handle duplicate starts. When a user-action pause ends one workflow and the application is requeued under a new execution, `GetApplicationBrowserID` finds and reuses the saved ID for that provider. Switching providers resolves a distinct provider-scoped ID and starts fresh; it does not migrate browser state. The actual Kernel browser `SessionID` is a separate value and changes whenever the remote browser is recreated.

### Proposed database records

All table names remain provider-neutral. Each provider-backed record has a `provider` column restricted in application validation to `rod` or `kernel`; do not name tables `kernel_*` or `rod_*`. Follow existing project conventions: numeric auto-increment `id_*` primary key, UUID `id_external` (`type:uuid`, default `gen_random_uuid()`, unique), explicit FK columns, timestamps, and soft-delete where appropriate.

| Table | Important fields | Constraints / purpose |
|---|---|---|
| `browser_profile` | `id_browser_profile`, `id_external`, `id_user`, `provider`, `provider_profile_id`, `provider_profile_name`, optional `id_browser_vault`, `created_at`, `updated_at`, `deleted_at` | Unique `(id_user, provider)`: one local profile record per user/provider. Kernel creates/loads provider profile; Rod has no equivalent profile for now. |
| `browser_vault` | `id_browser_vault`, `id_external`, `id_user`, `provider`, `provider_vault_id`, `provider_vault_name`, timestamps, `deleted_at` | Unique `(id_user, provider)`. Stores provider resource references only, never credentials. |
| `browser_auth_connection` | `id_browser_auth_connection`, `id_external`, `id_browser_profile`, `provider`, `provider_connection_id`, `domain`, `status`, `can_reauth`, `can_reauth_reason`, timestamps | Unique `(id_browser_profile, provider, domain)`. A profile may hold multiple managed-auth connections, usually one per domain. |
| `browser_session` | `id_browser_session`, `id_external`, `id_job_application`, `application_browser_id` UUID, `provider`, optional profile/vault FKs, current provider session ID, `status`, active `replay_generation`, encrypted `browser_live_view_url_ciphertext`, committed cursor (`checkpoint_created_at`, `checkpoint_mutation_id`), pending cursor (`pending_checkpoint_created_at`, `pending_checkpoint_mutation_id`), owner/lease fields, started/closed/expiry timestamps, standard timestamps | Unique `(id_job_application, provider)` and unique `application_browser_id`. One logical row survives requeues; replace provider session ID per browser incarnation. A new explicit retry increments `replay_generation`. Clear provider ID and Live View ciphertext after close; retain identity, generation, and committed checkpoint. |
| `browser_mutation_changelog` | `id_browser_mutation_changelog` numeric PK, `id_external` UUID, `id_browser_session` FK, `provider`, `replay_generation`, `operation`, encrypted/redacted JSONB `arguments`, typed JSONB `context` and `result`, per-generation `idempotency_key`, `status` (`pending`, `applied`, `failed`, `reconcile_required`), `created_at`, `updated_at`, optional `completed_at` | One row per action. Unique `(id_browser_session, replay_generation, idempotency_key)`. Fetch applied rows for the active generation ordered by `created_at, id_browser_mutation_changelog`; the PK breaks timestamp ties. |

The JSONB schema is defined by `model.BrowserMutationContext` and `model.BrowserMutationResult` in the API. Context fields: optional `url`, optional `target` (`role`, `name`, `label`, `selector`), optional `element_index` and `file_input_index`, and `replay_safe`. The URL is the destination for `navigate`; when supplied for another operation, it is the current-page precondition. Result fields: `outcome` (`applied`, `failed`, or `uncertain`), `replay_safe`, and optional `replay_session_id`. The session ID makes successful replay activities idempotent if Temporal retries after the changelog update; interrupted replay claims from a replaced provider session can be safely retried on the new browser. The worker serializes these structs directly; add schema changes to the API model rather than writing ad-hoc JSON maps.

Implement these as generic GORM models (e.g. `BrowserProfile`, `BrowserSession`) with `TableName()` matching the names above. Add the new models to the worker's schema initialization/migration path and honor repository migration rules. The profile unique index is intentionally composite so the same user can have a Rod and a Kernel row while never having two profiles for either provider.

### Browser session store and routing

`BrowserClient` resolves the current provider session by `ApplicationBrowserID` through a DB-backed store. All browser activities already receive the application ID in `WorkflowID`; adapt it to the named type at the boundary. They do not receive a Rod pointer, Kernel session ID, or CDP URL.

```go
// package browser/types
type SessionRef struct {
    Provider             string
    ProviderSessionID    string // e.g. Kernel SessionID
    LiveViewURL          string // active Kernel session only; keep secret
    Status               string
    ReplayGeneration     uint64
    CheckpointCreatedAt  time.Time
    CheckpointMutationID uint64
    ExpiresAt            time.Time
}

type SessionStore interface {
    Get(ctx context.Context, id ApplicationBrowserID) (SessionRef, bool, error)
    Save(ctx context.Context, id ApplicationBrowserID, ref SessionRef) error
    ClearActive(ctx context.Context, id ApplicationBrowserID) error
}
```

The store persists session metadata only; the provider closes its browser before active session metadata is cleared. Starting a browser is retry-safe: look up the logical session before creating a provider browser and persist the provider session ID before returning success. Closing deletes the provider resource and clears active session fields without deleting the logical row; cleanup is idempotent. Kernel sessions can reconnect on another worker by provider session ID.

**Rod placement is single-owner by design.** Rod's live `*rod.Browser`/`*rod.Page` objects remain in the process that launched them; the database stores identity but cannot transfer those Go objects. Run all Rod browser activities on one dedicated/single-owner worker process. Multi-replica Rod routing is explicitly out of scope and not needed. Never put provider IDs, Live View URLs, CDP URLs, API keys, or browser objects in Temporal history. Use a disconnected context for cleanup and provider TTL as a crash backstop.

## Mutation changelog and recovery

`browser_mutation_changelog` is an ordered replay journal, not a log of every session event. Each row represents one action and contains the exact operation name and arguments needed to invoke it again, plus page/target context needed to validate the replay. Do not combine multiple tool calls into one row. Replay reads only `applied` rows for the session's active `replay_generation`, ordered by `(created_at, id_browser_mutation_changelog)`.

Record browser mutations emitted by each agent-loop iteration: `click`, `input_text`, `input_multiple`, `scroll`, `navigate`, and `upload_file`. Do not record read-only operations such as screenshots, page scrape, or CAPTCHA detection. Exclude CAPTCHA injection and CAPTCHA-specific button operations because they are unique to the challenge. Exclude explicit `submit_application` operations and generic clicks identified as targeting a submit control; the agent decides when to submit. If target semantics are ambiguous, do not treat the action as safely replayable.

For each replayable mutation:

1. Derive an idempotency key from the application browser ID, replay generation, and stable agent/tool-call identity.
2. Insert or retrieve a `pending` changelog row containing the operation, encrypted arguments, and replay context before executing it.
3. Execute the operation through `BrowserClient`.
4. Persist a sanitized result and finalize the row as `applied`; record failures or uncertain outcomes with an appropriate status.

Serialize mutation execution and changelog writes per browser session so `created_at` and the monotonic PK preserve action order. Do not blindly repeat a `pending` non-idempotent operation after a crash; reconcile page/application state first, then mark it applied, failed, or requiring reconciliation. Prefer semantic role/name/label/selector information over screenshot indices and include the current URL or other replay preconditions. Store durable file references, not transient worker paths. Never persist Vault values, passwords, or tokens; encrypt replayable user-provided values at rest.

During catch-up, fetch all rows for the active generation in timestamp/PK order after the Kernel profile checkpoint (or from the generation start for Rod). Stop at any row that is not `applied` and replay-safe; do not skip it or continue past it. Before each mutation, capture a fresh screenshot and tagged accessibility state. JEV may verify target presence/index drift, but the worker must then resolve exactly one current element using the stored role/name/label/selector and submit metadata. Never use the prior numeric index as a fallback. Execute one mutation, detect/solve CAPTCHA before the next row, then wait a Temporal-recorded random 1–5 seconds. JEV final-state checks use sanitized node/file-input metadata, expected form-field labels and a query/fragment-stripped URL; never send field values or raw file HTML. Replay execution errors are diagnosed with JEV but always stop catch-up.

The session's `replay_pending` marker is set when a provider browser incarnation is created and cleared only after catch-up and final verification complete. This makes open-activity retries resume pending catch-up instead of mistaking a just-created browser for an already-restored one. An explicit retry starts a new generation and clears the prior profile checkpoint cursor.

Replayable operations are navigation, scroll, and click/text input with a stable semantic target. File uploads currently retain worker-local paths and are not replay-safe; catch-up stops at such rows until a durable file-reference design is added. Password fields and submit-target clicks are not replayed.

Treat the profile snapshot and action log as complementary recovery layers:

- Create/load the user's Kernel profile on `StartBrowser` with `save_changes: true` for the designated writer. Kernel snapshots browser user data (including cookies, local storage, preferences and tabs) on browser deletion. It does not merge concurrent writers; serialize writes with a lease on the user's profile row.
- Before `CloseBrowser` deletes the Kernel browser, persist the intended changelog cursor to the pending checkpoint fields. Browser deletion is what commits the `save_changes` profile snapshot; only after delete succeeds promote the pending `(created_at, mutation_id)` cursor to the committed checkpoint and clear the pending fields. If a crash occurs between remote deletion and DB promotion, reconcile the remote session/profile state before deciding whether to promote or replay. After a pause/requeue, create a new browser from that profile and replay only active-generation operations after the committed cursor.
- A browser profile cannot guarantee restoration of ephemeral JavaScript memory or every site's server-side workflow state. The mutation changelog is the recovery/audit trail for replayable actions, not a guarantee that arbitrary websites are perfectly replayable.
- Encrypt/redact changelog payloads and apply retention. Never log passwords, tokens, or values sourced from a Vault. Vault fill is not a replay mutation; if its use needs auditing, record a separate value-free audit event, not an executable changelog row. User-provided non-secret form values may be needed to resume; protect them as sensitive application data.

### Application retries and replay generations

Do not clear changelog rows when Temporal retries an activity, a worker restarts, or the application pauses for user action and resumes. Those are continuations of the same attempt and need the same replay history.

When the application reaches a terminal failed state and an authorized explicit retry starts, increment `replay_generation` and scope all new mutations, idempotency keys, checkpoints, and catch-up queries to that generation. Never replay mutations from the failed generation. Preserve those rows for diagnosis/audit; apply physical deletion only through the normal retention policy. On the new attempt, use the user's profile for reusable auth/storage, close or ignore restored tabs from the failed application, navigate to the original job URL, and start with no mutations replayed from the failed generation.

### Kernel Profile, Managed Auth, and Vault roles

These are distinct Kernel resources; use them together without conflating their responsibilities:

- **Browser Profile:** one profile record per user/provider, identified locally by `(id_user, provider)`. Kernel profile persists reusable browser state. The provider maps the local row to Kernel profile ID/name. The Kernel profile name must be unique in its project. Attach it at browser creation; use `save_changes: true` only while holding the per-profile writer lease.
- **Managed Auth:** zero or more auth connections per profile, usually one per domain. They bootstrap login state into the profile, health-check auth, and may automatically reauthenticate eligible connections. Persist connection IDs and safe status only. If status becomes `NEEDS_AUTH` and a person is required, pause the application and resume through user action; Managed Auth does not promise every login can be recovered automatically.
- **Vault:** attach a user's Kernel Vault when creating the browser. Use supported credential items and Kernel Fill operations so sensitive values are injected without passing through the worker/LLM. Browser attachment is immutable for that browser lifetime. Vault item events concern Vault lifecycle; they do not record clicks/types/scrolls and therefore cannot substitute for `browser_mutation_changelog`.

The Kernel provider creates/loads these resources only when `GetBrowserProvider() == BrowserProviderKernel`. Use deterministic safe resource names derived from the user's external UUID (not email or other PII), and persist returned provider IDs. Current Free/Developer docs list up to three Vaults and three Managed Auth connections; this is a small-scale integration limit, not one resource per arbitrary number of users. Enforce account limits and surface resource-creation errors rather than silently skipping setup; reassess resource topology/plan before broad multi-user rollout.

## User-action pause, close, and requeue

The current `HandleUserActionWorkflow` child can wait indefinitely while the parent job-application workflow and browser session remain open. Replace that lifecycle for requests that require a person:

1. Detect the user-action requirement and persist its request/layout plus the changelog cursor. After the user submits the structured form, persist the values as protected user-action input for the resumed workflow.
2. For Kernel, save the profile by deleting the browser and commit the changelog checkpoint only after deletion succeeds. Close the Rod browser as well; Rod resumes from the active replay generation's changelog.
3. Mark the application `blocked`, publish the existing user-action event, and let the current `JobApplicationWorkflow` complete with a durable "awaiting user" outcome. This releases its BrowserPool slot and avoids a live idle Kernel browser.
4. When the user submits the structured action form, persist the result and idempotently requeue/signal the application. Do not hold a workflow open waiting for that UI response.
5. The next application workflow loads the stored browser UUID/session row and same replay generation. Kernel starts from the user's saved profile and replays active-generation mutations after the committed checkpoint cursor; Rod starts a fresh browser and replays the active generation from its beginning.

The current `HandleUserActionWorkflow` and tool-call child workflow arrangement will need to become a dispatch/await-outside-the-running-browser flow. Ensure duplicate user submissions/requeue signals are idempotent and coordinate job status, browser-pool membership, and session checkpoint transactionally/reconcilably. Preserve old Temporal histories with compatible activity/workflow names or `workflow.GetVersion` gates; test replay, cancellation, and requeue races. Rod has no Kernel profile snapshot: close its single-owner browser and reconstruct from the active generation's changelog on requeue, replaying from the beginning unless a separately verified Rod checkpoint exists.

## Q&A deduplication with JEV

`JobApplicationWorkflow` currently calls `deduplicateQA` before saving collected application questions. Replace its `CallLLM`-style free-form JSON cleanup with the structured `CallJev` activity pattern used by `workflow/initiateapplication/jev.go`.

Send the collected Q&A as JEV state and ask for conservative equivalence decisions over candidate pairs/groups. Merge only entries that clearly express the same underlying question; do not merge distinct intents or conflicting answers, and do not invent or rewrite answers. Validate the returned typed decisions before applying merges. If the JEV activity fails or returns malformed/incomplete decisions, keep the raw Q&A (matching the current workflow's fallback behavior) and log the deduplication failure.

## Live View and frontend contract

Kernel returns `BrowserLiveViewURL` at browser creation. Store it on the active `browser_session` row only—not `job_application`, whose lifetime exceeds the browser resource. It is an ephemeral bearer URL and becomes invalid when the browser is deleted or times out. Protect it at rest, never log it or place it in Temporal history, return it only from an authenticated endpoint that verifies the requesting user owns the job application, and clear it after browser close.

The frontend can expose a read-only Live View iframe while an application browser is active. Validate every `message` event's origin against the origin of the stored Live View URL. Use `KERNEL_PLAYING` as evidence that frames are rendering; handle `KERNEL_PAUSED` and connection diagnostics. Parent messages do not report actual browser clicks, text entry, or scrolls. **Live View is read-only during automation**; collect human input through the structured user-action UI so it can be persisted and replayed. When the workflow has closed the browser for user action, show the action form/status and remove the now-invalid iframe. Add required iframe permissions and CSP directives from Kernel's Live View docs; do not accept untrusted parent messages.

Suggested worker/API boundary:

```go
// Authenticated application API, not a Temporal activity:
func (h *Handler) GetApplicationLiveViewURL(ctx context.Context, idExternal uuid.UUID) (string, error)
```

The API reads/decrypts the active session's URL only after checking `JobApplication.UserId` against the authenticated principal. The frontend listens for `KERNEL_PLAYING` to confirm a useful live connection rather than treating `KERNEL_CONNECTED` as proof that frames are visible.

The worker and API must share `BROWSER_DATA_ENCRYPTION_KEY`, a base64-encoded 32-byte AES key, to encrypt/decrypt the bearer URL. Generate it once with `openssl rand -base64 32` and provision it through each service's secret manager; never commit the generated value. The browser-session store binds ciphertext to the logical application browser UUID as authenticated data, so ciphertext cannot be moved to another session.

## Temporal activity contract

Keep the existing activity names and input/output shapes during this migration. Keep `WorkflowID` as the serialized field and convert it only inside the activity adapter. The `OpenWebpage` adapter calls `StartBrowser` using the workflow ID and URL. `ClosePage` remains registered for Temporal history compatibility but calls `CloseBrowser` to close the entire browser. When paid geo-proxy support is enabled, add optional country/ZIP fields from the existing profile result and version any changed workflow activity arguments; Free mode ignores these location fields. Existing methods continue to serve workflows:

```go
func NewActivities(client browser.BrowserClient) *Activity

func (a *Activity) OpenWebpage(ctx context.Context, input OpenWebpageInput) error
func (a *Activity) TakeScreenshot(ctx context.Context, input TakeScreenshotInput) (TakeScreenshotOutput, error)
func (a *Activity) GetBase64Screenshot(ctx context.Context, input GetBase64ScreenshotInput) (string, error)
func (a *Activity) Click(ctx context.Context, input ClickInput) error
func (a *Activity) Type(ctx context.Context, input TypeInput) error
func (a *Activity) TypeMultiple(ctx context.Context, input TypeMultipleInput) error
func (a *Activity) Scroll(ctx context.Context, input ScrollInput) error
func (a *Activity) Navigate(ctx context.Context, input NavigateInput) error
func (a *Activity) UploadFile(ctx context.Context, input UploadFileInput) error
func (a *Activity) DetectCaptcha(ctx context.Context, input DetectCaptchaInput) (DetectCaptchaOutput, error)
func (a *Activity) InjectCaptchaToken(ctx context.Context, input InjectCaptchaTokenInput) (InjectCaptchaTokenOutput, error)
func (a *Activity) ClickCaptchaButton(ctx context.Context, input ClickCaptchaButtonInput) (ClickCaptchaButtonOutput, error)
func (a *Activity) ScrapeRenderedPage(ctx context.Context, input ScrapeRenderedPageInput) (ScrapeRenderedPageOutput, error)
func (a *Activity) ClickSubmitAndCapture(ctx context.Context, input ClickSubmitInput) (ClickSubmitOutput, error)
func (a *Activity) VerifySubmissionState(ctx context.Context, input VerifySubmissionStateInput) (VerifySubmissionStateOutput, error)
func (a *Activity) ClosePage(ctx context.Context, input ClosePageInput) error
```

`ClosePage` stays registered under its existing Temporal name for histories already in flight, but its implementation calls `BrowserClient.CloseBrowser`, closing the entire per-application browser. If a later cleanup changes workflow activity names, input payloads, or command order, use Temporal versioning and replay tests. `GetBase64Screenshot` remains a filesystem/base64 adapter and does not need a browser handle.

`activity/browser.Activity` no longer owns `map[string]*rod.Page`, `map[string]*rod.Browser`, or cached live `*rod.Element` values. It may retain only neutral tagged DTOs needed by existing activity output behavior; actual element-index resolution stays inside the provider.

## Provider behavior

### go-rod

- Move the existing launcher, health/reconnect logic, screenshot/grid/accessibility tagging, and Rod-focused tests into `browser/rod`.
- Launch one fresh Chrome browser per application. Remove the shared browser/incognito-context architecture and do not attach to the shared fallback CDP port if doing so would break isolation.
- Keep the browser and launcher private to `RodBrowserClient`; close the full browser and terminate its launcher from `CloseBrowser`.
- Preserve compatible current operations, but implement each through the neutral interface and DTOs.
- **Worker placement:** run all Rod browser activities on one dedicated/single-owner process. The registry does not move local Go browser/page objects across processes. Multi-replica Rod support is explicitly out of scope and not needed.

### Kernel (future provider)

- Add `github.com/kernel/kernel-go-sdk` and use its Go SDK APIs.
- `NewKernelBrowserClient` creates/configures the SDK client. `StartBrowser` retrieves the application's session/profile/vault records, calls `client.Browsers.New` once per active browser incarnation, stores the returned `SessionID`, and connects future operations by that ID.
- Create/reuse one Kernel Profile per user/provider and one Vault mapping per user/provider. Attach both when creating a browser. Set `save_changes: true` only while holding the profile writer lease. For each domain requiring login, reuse/create a Managed Auth connection referencing that profile; handle `NEEDS_AUTH` via pause/requeue. Never send Vault credential values through worker operations or LLM prompts.
- Create with `Headless: false`, `Stealth: true`, and `TimeoutSeconds` longer than the 30-minute Temporal session plus cleanup margin (Kernel's documented maximum is 72 hours). Explicitly call `client.Browsers.DeleteByID` on application completion. Closing a Playwright/CDP connection alone does not delete the remote browser.
- Use Playwright Execution for DOM reads/actions and Patchright-managed automation; use Computer Controls selectively when native mouse/keyboard interaction or screen capture is a better fit. Avoid a self-hosted CDP connection as the default control path.
- Persist the returned Live View URL only while the remote browser is active. Delete the browser before a long human wait so the profile is saved and the browser concurrency slot is released; its old Live View URL is invalid after deletion.

## Free-plan bot avoidance and location trade-off

Use the Kernel features available on the current Developer/Free plan:

- Kernel anti-detection browser defaults.
- Headful mode; do not set headless.
- `Stealth: true`, which adds Kernel's default static ISP proxy and automatic CAPTCHA solver.
- Playwright Execution/Patchright for DOM work. Use Computer Controls selectively for OS-level interaction with human-like input.
- Keep the current CAPTCHA activity as fallback for challenges not resolved by Kernel's managed solver; neither solver guarantees every challenge.

Do not plan on Web Bot Auth (WBA), GPU, or configurable proxies on Free. Kernel's current pricing matrix marks configurable proxies unavailable on Developer/Free. Therefore, while staying Free, use the stealth default proxy and **do not claim it matches the user's country or ZIP**. This defers the requested location-matched proxy behavior. The existing profile's `CountryOfResidence` and `Zip` remain the intended future source: after enabling a plan with configurable proxies, normalize the country to ISO 3166, use ZIP only for US, and fall back to country-only targeting if ZIP targeting is absent/unavailable.

Free-plan operational constraints from current docs:

- Five concurrent browsers account-wide. The existing Temporal pool limit of four active applications is below this only if other Kernel use does not consume the remaining capacity; keep handling provider concurrency/rate-limit errors.
- Developer includes $5 monthly usage credits. Headful is currently priced at $0.0001333336/second (eight times headless), or about $0.24 for 30 minutes of active runtime. Set spending/usage alerts before rollout.
- Managed stealth and Computer Controls are available on the Free tier; WBA is not.

The Temporal `BrowserPoolWorkflow` remains the concurrency control for application jobs; do not introduce Kernel Browser Pools as part of this per-application design.

## File-by-file implementation sequence

1. **`browser/types` and `browser/client.go`:** Define provider-name string constants, application browser UUID, neutral DTOs, operation-store contract, `BrowserClient`, renamed methods, and provider factory.
2. **`activity/sqldb/browser_*.go`:** Add provider-generic profile, vault, auth-connection, session, and mutation-changelog models/stores, unique indexes, generation-scoped idempotency constraints, and migration registration.
3. **`browser/rod`:** Move Rod behavior/tests; create one browser per application; keep all Rod browser activities on the dedicated owner process; implement full-browser cleanup.
4. **`browser/kernel`:** Implement profile/Vault attachment, Managed Auth references, headful stealth creation, operation execution/changelog writes, profile checkpoint/delete, Live View URL persistence, and SDK tests.
5. **`activity/browser`:** Replace direct go-rod/proto types with BrowserClient calls and mutation-changelog writes. Keep activity names/serialization; `ClosePage` calls `CloseBrowser`.
6. **`common/dependencies.go`:** Keep `MakeDependencies()` argument-free and explicitly call `NewBrowserClient(ClientTypeRod, cfg)` for now.
7. **`workflow/jobapplication` and `workflow/handleuseraction`:** Generate UUID in `workflow.SideEffect`, persist/retrieve it across requeue, save/close on user-action pause, finish workflow/release slot, and resume/replay after user response. Add replay-generation boundaries for explicit retries after terminal failure; use `CallJev` for conservative Q&A deduplication with raw-Q&A fallback. Add `workflow.GetVersion` gates/replay tests.
8. **Frontend/API:** Add authenticated endpoint for active Live View URL and frontend read-only iframe handling, origin validation, playback signals, and paused-session UX.
9. **Validation/dependencies:** Add provider/session/log/activity/requeue tests, keep this design current, and vendor Kernel SDK when its dependency is added.

## Validation gates

- A fake `BrowserClient` can drive all browser activities without go-rod types.
- Both concrete providers satisfy the root interface at compile time.
- Multiple applications receive distinct browser resources; one application's close does not affect another.
- Every activity selects the correct browser using the same application ID, and open/close retries do not leak duplicate sessions.
- Temporal activity names/payloads remain compatible with existing histories or are explicitly versioned.
- Kernel creation requests headful stealth mode, respects configured timeout, and deletes the session on all terminal paths.
- Free-plan configuration never tries to create a configurable geo proxy; logs and docs do not imply the default stealth proxy matches profile location.
- Kernel concurrency/rate-limit failures surface as activity errors and do not leave registry rows or remote browsers orphaned.
- Mutation rows are one-action-per-record, ordered by `(created_at, id_browser_mutation_changelog)`, and idempotent within a replay generation. Pending operations are reconciled rather than blindly retried.
- Catch-up screenshots/tags before every mutation, resolves unique semantic targets without stale-index fallback, checks CAPTCHA between rows, paces each row randomly 1–5 seconds, and fails closed on uncertain rows or actions.
- Explicit retry after terminal failure starts a new replay generation and never replays failed-generation mutations; Temporal retries and user-action resumes retain the current generation.
- A user-action pause closes the browser, releases the scheduler slot, and requeues idempotently after input; profile leases prevent concurrent Kernel profile writers.
- JEV-based Q&A dedup merges only validated duplicate decisions and preserves raw Q&A on JEV failure.
- Live View access is owner-authorized, ephemeral, cleared on close, and the frontend validates message origins and treats automation view as read-only.

## Sources and constraints

Reviewed `workflow/jobapplication/workflow.go`, `workflow/jobapplication/helper.go`, `workflow/jobapplication/profile/profile.go`, `workflow/initiateapplication/jev.go`, `workflow/handleuseraction/workflow.go`, `activity/browser/`, `activity/sqldb/useraction.go`, `activity/sqldb/jobapplicationprofile.go`, `common/dependencies.go`, and `iris-api/model/job_application_profile.go`.

Kernel Go docs: [Create](https://kernel.sh/docs/introduction/create), [Control](https://kernel.sh/docs/introduction/control), [Stealth](https://kernel.sh/docs/browsers/bot-detection/stealth), [Headless](https://kernel.sh/docs/browsers/headless), [Residential Proxies](https://kernel.sh/docs/proxies/residential), [Termination & Timeouts](https://kernel.sh/docs/browsers/termination), and [Pricing & Limits](https://kernel.sh/docs/info/pricing).

The worker deploy uses vendoring: after introducing the Kernel SDK, update `go.mod`/`go.sum` and run `go mod vendor`.

## Decisions and open issues

- Use a persisted application-browser UUID as the logical browser key; create it through `workflow.SideEffect` and reuse it after requeue.
- Use a DB-backed provider-session registry and per-attempt mutation changelog; keep provider IDs and connection data out of workflow history.
- Keep `MakeDependencies()` argument-free and construct Rod explicitly with `ClientTypeRod` for now; no environment-based provider selection yet.
- Stay on the Free Kernel plan for now; use managed stealth defaults and defer user-country/ZIP proxying.
- Preserve `ClosePage` activity compatibility while changing its implementation to close the whole browser.
- The `BrowserClient.GetBrowserProvider()` method returns only the provider name as a string, not a provider object.
- Rod runs on one dedicated/single-owner worker; multi-replica Rod routing is out of scope.
- Kernel profiles, Managed Auth, and Vaults are Kernel-only. Vaults do not replace the mutation changelog. Kernel Free-plan resource limits must be addressed before broad multi-user rollout.
- Store the encrypted Live View URL only in the active browser session, never on the job application.
