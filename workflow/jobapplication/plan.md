# CAPTCHA Detection & Solving

## Context

Job applications get stuck on CAPTCHA screens, blocking automated submission. Observed in
production: **Cloudflare Turnstile** (Indeed, heavy) and **reCAPTCHA** (Lever, Workable,
Wellfound, Ashby — almost all v2 checkbox). We will integrate **CapSolver** to solve these
automatically.

Design goals (per requirements):
- **All CAPTCHA logic lives inside a single self-contained workflow tool.** The planner never
  reasons about CAPTCHAs — it only invokes the tool; when the tool returns, the CAPTCHA is solved.
- **Deterministic detection** drives everything (a `DetectCaptcha` activity that inspects the live
  DOM), not the vision LLM. Detection runs once per agent-loop iteration.
- After token injection, an **LLM vision check** decides whether a button still needs clicking, so
  the tool fully finishes the solve without the planner.

## How detection works (v2 vs v3 vs Turnstile)

A `DetectCaptcha` activity runs `page.Eval` over the live page and its iframes to find known
markers. This is deterministic — no LLM.

- **reCAPTCHA v2 (checkbox)**: visible widget. Marker = a `.g-recaptcha[data-sitekey]` div OR an
  iframe whose `src` contains `recaptcha/api2/anchor`. Extract `sitekey` from `data-sitekey` or the
  iframe `src` `k=` query param. → CapSolver task `ReCaptchaV2TaskProxyLess`.
- **reCAPTCHA v3 (invisible/score)**: NO anchor widget. Marker = a script tag
  `recaptcha/api.js?render=SITEKEY` and no `api2/anchor` iframe. Requires an `action` string
  (best-effort: read from `grecaptcha.execute` calls / `data-action` attr; default `"submit"`).
  → CapSolver task `ReCaptchaV3TaskProxyLess`. **Heuristic: anchor iframe present ⇒ v2; only
  `render=` script, no anchor ⇒ v3.**
- **Cloudflare Turnstile**: Marker = `.cf-turnstile[data-sitekey]` div OR iframe `src` containing
  `challenges.cloudflare.com`. Extract `sitekey` from `data-sitekey` (or the widget config).
  Optionally `action` / `cData`. → CapSolver task `AntiTurnstileTaskProxyLess`.
- **None**: no markers ⇒ `Type = "none"`, loop proceeds normally.

For the sites in scope, expect mostly reCAPTCHA v2 and Turnstile. v3 support is included but
lower-priority.

## Architecture

A new **child workflow** `SolveCaptchaWorkflow` (mirrors `SubmitApplicationWorkflow` /
`HandleUserActionWorkflow`) registered as the `solve_captcha` tool. It orchestrates new activities:

```
DetectCaptcha        -> {type, sitekey, page_url, action, invisible, extra{}}  (also used per-loop)
SolveWithCapSolver   -> creates CapSolver task, polls until token ready -> {token}
InjectCaptchaToken   -> page.Eval: write token to hidden field + fire widget callback
TakeScreenshot       -> (existing) fresh screenshot after injection
CallLLM (vision)     -> (existing) "is captcha solved, or must a button be clicked? which one?"
ClickCaptchaButton   -> click by CSS selector (NEW capability; index-click can't reach iframe/widget)
```

### Control flow of the tool
1. `DetectCaptcha` (re-confirm type/args on the live page; the loop already detected one).
2. If `type == none` → return `{solved: true, skipped: true}`.
3. `SolveWithCapSolver` → token. On failure/timeout → **fall back to `handle_user_action`**
   (reuse `HandleUserActionWorkflow`) so the application doesn't dead-end; return its result.
4. `InjectCaptchaToken` (inject + fire callback).
5. `TakeScreenshot` → `CallLLM` vision check with a small schema
   `{is_solved: bool, needs_button_click: bool, button_description: string|null}`.
6. If `needs_button_click` → `ClickCaptchaButton`, then re-verify (one more detect/vision pass,
   bounded to ~2 attempts).
7. Return `{solved: bool}`. Planner continues the normal loop (which will click the *application's*
   submit button as it already does).

## Per-loop deterministic detection (wiring)

In `executeJobApplication` (workflow/jobapplication/workflow.go, agent loop ~L264), right after
`TakeScreenshot` succeeds, call `DetectCaptcha`. If `type != none`, execute the `solve_captcha`
child workflow **before** calling `planNextAction`, so the planner always sees a captcha-free page.
Keep it in a small helper (e.g. `maybeSolveCaptcha(sessionCtx, workflowID, ...)`) per the
"collection of smaller helper functions" convention. The planner does not get a `solve_captcha`
tool exposed — it's driven entirely by deterministic detection.

## Files to create / modify

**New — activity package `activity/captcha/`:**
- `activity/captcha/types.go` — `SolveWithCapSolverInput/Output` only (CapSolver API). The
  page-touching structs (`DetectCaptcha*`, `InjectCaptchaToken*`, `ClickCaptchaButton*`) go in
  `activity/browser/types.go`, mirroring `GetFormActionInput/Output`.
- **Decided:** `DetectCaptcha`, `InjectCaptchaToken`, and `ClickCaptchaButton` are new methods on
  the existing **`activity/browser.Activity`** — it already owns `activeSessions map[string]*rod.Page`
  and `browserFactory`, so they get the live page for free and no session map is exposed. Their
  input/output structs go in `activity/browser/types.go` (mirror `GetFormActionInput/Output`). Only
  the CapSolver HTTP client (`SolveWithCapSolver`) lives in the new `activity/captcha` package.
- CapSolver client: read `CAPSOLVER_API_KEY` via `os.Getenv` (mirror `scrapeWithSerper` in
  `activity/web/activity.go`). POST `createTask` then poll `getTaskResult` until `ready`. Use
  `http.NewRequestWithContext`; rely on Temporal `RetryPolicy` for retries. Give the poll a
  generous `StartToCloseTimeout` (solves take 10–60s; set ~2–3 min).

**New — `ClickCaptchaButton` (selector-based click):** the repo has **no click-by-CSS-selector**
today (only index clicks via tagged nodes — `activity/browser/activity.go` `Click`). Add a method
that does `page.Element(selector)` / frame traversal + `.Click(proto.InputMouseButtonLeft, 1)`.
Needed because the post-solve button (reCAPTCHA challenge / Turnstile widget) often sits in an
iframe and isn't a cleanly tagged node.

**New — child workflow `workflow/solvecaptcha/workflow.go`:**
- `SolveCaptchaWorkflow(ctx, SolveCaptchaWorkflowInput) (map[string]interface{}, error)`.
- Input mirrors other tools: `{WorkflowID, IdUser, IdJobApplication}`.
- Structure as helper functions (detect / solve / inject / verify / fallback) per CLAUDE.md.
- Reuse existing `CallLLM` + `getBase64Screenshot` pattern from `handleuseraction/workflow.go` for
  the vision check; add a small response schema.
- On CapSolver failure, execute `HandleUserActionWorkflow` as a child (a new
  `shared.UserActionCaptcha` action type may be added to `shared/shared.go` for nicer UX, or reuse
  `UserActionAdditionalInfo`).

**Modify — `main.go`:**
- Register new browser methods automatically (already registered via `browser.NewActivities`).
- `w.RegisterActivity(captcha.NewActivities(...))` for the CapSolver client.
- `w.RegisterWorkflow(solvecaptcha.SolveCaptchaWorkflow)` in `registerJobApplicationWorkflows`.
- If templates are used, add `solvecaptcha.SetTemplates()` in `loadTemplates`.

**Modify — `workflow/jobapplication/workflow.go`:**
- Add `maybeSolveCaptcha` helper + call it in the agent loop after `TakeScreenshot`.

**Modify — `common/dependencies.go` (only if CapSolver key is injected as a dependency):**
- Simpler to just `os.Getenv("CAPSOLVER_API_KEY")` inside the activity (matches `SERPER_API_KEY`
  usage in `activity/web`). No DI change needed.

**Env:** add `CAPSOLVER_API_KEY` to `.env` / deployment config.

## Notes / risks

- **Proxy / egress IP**: worker egress IP is provided via `EGRESS_IP_ADDRESS` env var and passed to
  CapSolver so the solve originates from the same IP as form submission (avoids token-IP-binding
  rejection on Turnstile / reCAPTCHA Enterprise). CapSolver `createTask` includes the proxy fields
  when `EGRESS_IP_ADDRESS` is set; if unset, falls back to `*ProxyLess` task variants.
- **Iframe traversal**: go-rod supports `page.Frames()` / frame `.Eval` / `.Element`; the repo
  doesn't use it yet. Detection and the post-solve click must recurse into frames.
- **Callback discovery** varies per site; the LLM vision "needs button click?" step is the safety
  net when injecting + firing the callback isn't enough.
- Keep a bounded retry (~2) inside the tool to avoid infinite detect→solve→click loops; on
  exhaustion, fall back to `handle_user_action`.

## Verification (end-to-end)

1. Set `CAPSOLVER_API_KEY`. `make start-temporal-server` + `make start-worker`.
2. **Turnstile**: run a job-application workflow against an Indeed posting known to show Turnstile.
   Confirm logs show `DetectCaptcha type=turnstile`, a CapSolver task id, token injected, vision
   check, and the loop proceeds past the captcha to the form.
3. **reCAPTCHA v2**: run against a Lever/Workable/Ashby/Wellfound posting. Confirm
   `type=recaptcha_v2`, sitekey extracted, solved, planner never sees a captcha tool call.
4. **Fallback**: temporarily set an invalid `CAPSOLVER_API_KEY`; confirm the tool falls back to
   `HandleUserActionWorkflow` (user gets a "solve captcha" action) rather than failing the app.
5. **No-captcha regression**: run a posting with no captcha; confirm `DetectCaptcha type=none`, tool
   is skipped, zero added latency beyond one `DetectCaptcha` call per loop.
6. `go build ./...` and, after adding deps, `go mod vendor` (per repo Vendoring Rule) before deploy.

## Unresolved questions

1. New `shared.UserActionCaptcha` type for the fallback, or reuse `UserActionAdditionalInfo`?
2. v3 `action` string source — is default `"submit"` acceptable for now?
