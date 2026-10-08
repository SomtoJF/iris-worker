package kernel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SomtoJF/iris-worker/browser/types"
	kernelsdk "github.com/kernel/kernel-go-sdk"
)

const pagePrelude = `if (!page) throw new Error("no active page");`

func (c *KernelBrowserClient) Navigate(ctx context.Context, id types.ApplicationBrowserID, url string) error {
	if strings.TrimSpace(url) == "" {
		return types.MutationNotExecuted(errors.New("navigation URL is required"))
	}
	code := pagePrelude + ` await page.goto(` + jsString(url) + `, { waitUntil: "domcontentloaded" }); return true;`
	return c.execute(ctx, id, code, nil)
}

func (c *KernelBrowserClient) ScreenshotForLLM(ctx context.Context, id types.ApplicationBrowserID, fileName string) (types.Screenshot, error) {
	if fileName == "" {
		return types.Screenshot{}, errors.New("screenshot filename is required")
	}
	const code = `
` + pagePrelude + `
const snapshot = await page.evaluate(() => {
  document.querySelectorAll("[data-iris-browser-index]").forEach(el => el.removeAttribute("data-iris-browser-index"));
  document.querySelectorAll("[data-iris-file-index]").forEach(el => el.removeAttribute("data-iris-file-index"));
  const visible = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none" && !el.disabled && el.getAttribute("aria-disabled") !== "true"; };
  const candidates = [...document.querySelectorAll('button,input,textarea,select,a,[role="button"],[role="textbox"],[role="checkbox"],[role="radio"],[role="combobox"],[role="switch"],[contenteditable="true"]')].filter(visible);
  const roleOf = (el) => {
    const role = el.getAttribute("role");
    if (role) return role;
    if (el.matches('input[type="password"]')) return "password";
    if (el.matches('input[type="submit"],input[type="image"],button[type="submit"]')) return "button";
    if (el.matches('input[type="checkbox"]')) return "checkbox";
    if (el.matches('input[type="radio"]')) return "radio";
    if (el.matches("textarea,input:not([type]),input[type=text],input[type=email],input[type=search],input[type=tel],input[type=url],input[type=number]") || el.isContentEditable) return "textbox";
    if (el.matches("select")) return "combobox";
    if (el.matches("a")) return "link";
    if (el.matches("button,input[type=button]")) return "button";
    return el.tagName.toLowerCase();
  };
  const labelOf = (el) => [...(el.labels || [])].map(label => label.innerText.trim()).filter(Boolean).join(" ");
  const selectorOf = (el) => el.id && document.querySelectorAll("#" + CSS.escape(el.id)).length === 1 ? "#" + CSS.escape(el.id) : "";
  const isFinalSubmit = (el, name, label) => {
    const text = (name + " " + label).trim().toLowerCase();
    if (text === "submit" || text.includes("submit application") || text === "apply" || text === "apply now" || text.includes("send application")) return true;
    return !text && el.matches('input[type="submit"],input[type="image"],button[type="submit"]');
  };
  const nodes = candidates.map((el, index) => {
    el.setAttribute("data-iris-browser-index", String(index));
    const r = el.getBoundingClientRect();
    const label = labelOf(el) || el.getAttribute("aria-label") || "";
    const name = el.getAttribute("aria-label") || label || el.getAttribute("placeholder") || el.getAttribute("title") || el.innerText || el.textContent || el.getAttribute("name") || "";

    const role = roleOf(el);
    const value = role === "password" ? "<redacted>" : (el.value || null);
    return {index, description:name.trim().slice(0,500), name:name.trim().slice(0,500), label:label.trim().slice(0,500), selector:selectorOf(el), submit:isFinalSubmit(el, name, label), x:r.x, y:r.y, width:r.width, height:r.height, role, value, required:typeof el.required === "boolean" ? el.required : null, checked:typeof el.checked === "boolean" ? String(el.checked) : null};
  });
  const files = [...document.querySelectorAll('input[type="file"]')].filter(visible).map((el, index) => {
    el.setAttribute("data-iris-file-index", String(index));
    const label = labelOf(el) || el.getAttribute("aria-label") || null;
    return {index, html:el.outerHTML, name:el.getAttribute("name") || "", label, value:el.value || null};
  });
  const alerts = [...document.querySelectorAll('[role="alert"],[aria-live="assertive"]')].some(visible);
  return {nodes, files, current_url:location.href, has_visible_alerts:alerts};
});
const image = (await page.screenshot({type:"png"})).toString("base64");
return {image, nodes:snapshot.nodes, files:snapshot.files, current_url:snapshot.current_url, has_visible_alerts:snapshot.has_visible_alerts};`
	var result struct {
		Image            string                  `json:"image"`
		Nodes            []types.TaggedNode      `json:"nodes"`
		Files            []types.TaggedFileInput `json:"files"`
		CurrentURL       string                  `json:"current_url"`
		HasVisibleAlerts bool                    `json:"has_visible_alerts"`
	}
	if err := c.execute(ctx, id, code, &result); err != nil {
		return types.Screenshot{}, err
	}
	if result.Image == "" {
		return types.Screenshot{}, errors.New("Kernel returned an empty screenshot")
	}
	image, err := base64.StdEncoding.DecodeString(result.Image)
	if err != nil {
		return types.Screenshot{}, fmt.Errorf("decode Kernel screenshot: %w", err)
	}
	name := filepath.Base(fileName)
	ext := filepath.Ext(name)
	prefix := strings.TrimSuffix(name, ext) + "-*" + ext
	file, err := os.CreateTemp(c.tempFS.GetBasePath(), prefix)
	if err != nil {
		return types.Screenshot{}, fmt.Errorf("create screenshot file: %w", err)
	}
	path := file.Name()
	if _, err := file.Write(image); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return types.Screenshot{}, fmt.Errorf("write screenshot file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return types.Screenshot{}, fmt.Errorf("close screenshot file: %w", err)
	}
	return types.Screenshot{Path: path, CurrentURL: result.CurrentURL, HasVisibleAlerts: result.HasVisibleAlerts, TaggedNodes: result.Nodes, TaggedFileInputNodes: result.Files}, nil
}

func (c *KernelBrowserClient) Click(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) error {
	if elementIndex < 0 {
		return types.MutationNotExecuted(errors.New("element index cannot be negative"))
	}
	selector := fmt.Sprintf(`[data-iris-browser-index="%d"]`, elementIndex)
	code := pagePrelude + ` const el = page.locator(` + jsString(selector) + `); if (await el.count() === 0) throw new Error("element index not found"); await el.first().click(); return true;`
	return c.execute(ctx, id, code, nil)
}

func (c *KernelBrowserClient) Type(ctx context.Context, id types.ApplicationBrowserID, field types.FieldInput) error {
	if field.ElementIndex < 0 {
		return types.MutationNotExecuted(errors.New("element index cannot be negative"))
	}
	selector := fmt.Sprintf(`[data-iris-browser-index="%d"]`, field.ElementIndex)
	method := "type"
	if field.Replace {
		method = "fill"
	}
	code := pagePrelude + ` const el = page.locator(` + jsString(selector) + `); if (await el.count() === 0) throw new Error("element index not found"); await el.first().` + method + `(` + jsString(field.Text) + `); return true;`
	return c.execute(ctx, id, code, nil)
}

func (c *KernelBrowserClient) Scroll(ctx context.Context, id types.ApplicationBrowserID, direction string, ratio float64) error {
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 || ratio > 1 {
		return types.MutationNotExecuted(errors.New("scroll ratio must be greater than zero and at most one"))
	}
	if direction != "up" && direction != "down" && direction != "left" && direction != "right" {
		return types.MutationNotExecuted(fmt.Errorf("unsupported scroll direction %q", direction))
	}
	delta := ratio
	if direction == "up" || direction == "left" {
		delta = -delta
	}
	x, y := 0.0, 0.0
	if direction == "left" || direction == "right" {
		x = delta
	} else {
		y = delta
	}
	code := pagePrelude + ` await page.evaluate(([xRatio, yRatio]) => window.scrollBy({left: xRatio * innerWidth, top: yRatio * innerHeight, behavior: "smooth"}), [` + strconv.FormatFloat(x, 'f', -1, 64) + `,` + strconv.FormatFloat(y, 'f', -1, 64) + `]); return true;`
	return c.execute(ctx, id, code, nil)
}

func (c *KernelBrowserClient) UploadFile(ctx context.Context, id types.ApplicationBrowserID, fileInputIndex int, filePath string) error {
	if fileInputIndex < 0 {
		return types.MutationNotExecuted(errors.New("file input index cannot be negative"))
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return types.MutationNotExecuted(fmt.Errorf("read upload file: %w", err))
	}
	selector := fmt.Sprintf(`[data-iris-file-index="%d"]`, fileInputIndex)
	filename := filepath.Base(filePath)
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	payload, _ := json.Marshal(base64.StdEncoding.EncodeToString(data))
	nameJSON, _ := json.Marshal(filename)
	mimeJSON, _ := json.Marshal(contentType)
	selectorJSON, _ := json.Marshal(selector)
	code := pagePrelude + ` const el = page.locator(` + string(selectorJSON) + `); if (await el.count() === 0) throw new Error("file input index not found"); await el.first().setInputFiles({name:` + string(nameJSON) + `,mimeType:` + string(mimeJSON) + `,buffer:Buffer.from(` + string(payload) + `,"base64")}); return true;`
	return c.execute(ctx, id, code, nil)
}

func (c *KernelBrowserClient) ScrapeRenderedPage(ctx context.Context, id types.ApplicationBrowserID) (string, error) {
	var text string
	if err := c.execute(ctx, id, pagePrelude+` return await page.locator("body").innerText();`, &text); err != nil {
		return "", err
	}
	return text, nil
}

func (c *KernelBrowserClient) DetectCaptcha(ctx context.Context, id types.ApplicationBrowserID) (types.Captcha, error) {
	code := pagePrelude + `
const result = await page.evaluate(() => {
  const html = document.documentElement.outerHTML;
  const text = (document.body?.innerText || "").toLowerCase();
  let type = "";
  if (document.querySelector('iframe[src*="recaptcha"],.g-recaptcha,[data-sitekey][data-callback]') || html.includes("google.com/recaptcha")) type = "recaptcha_v2";
  if (type === "recaptcha_v2" && (document.querySelector('[data-action]') || [...document.scripts].some(script => script.src.includes("recaptcha") && script.src.includes("render=")))) type = "recaptcha_v3";
  else if (document.querySelector('iframe[src*="hcaptcha"],.h-captcha') || html.includes("hcaptcha.com")) type = "hcaptcha";
  else if (document.querySelector('iframe[src*="challenges.cloudflare.com"],[name="cf-turnstile-response"],.cf-turnstile')) type = "turnstile";
  else if (/verify you are human|captcha challenge|checking your browser/.test(text)) type = "unknown";
  const challenge = document.querySelector('[data-sitekey],.g-recaptcha,.h-captcha,.cf-turnstile');
  const extra = {};
  if (challenge?.getAttribute("data-cdata")) extra.cData = challenge.getAttribute("data-cdata");
  if (challenge?.getAttribute("data-chlpage")) extra.chlPageData = challenge.getAttribute("data-chlpage");
  return {type, site_key:challenge?.getAttribute("data-sitekey") || "", page_url:location.href, action:challenge?.getAttribute("data-action") || "", invisible:challenge?.classList.contains("invisible") || false, extra};
});
return result;`
	var captcha types.Captcha
	if err := c.execute(ctx, id, code, &captcha); err != nil {
		return types.Captcha{}, err
	}
	return captcha, nil
}

func (c *KernelBrowserClient) InjectCaptchaToken(ctx context.Context, id types.ApplicationBrowserID, captchaType, token string) (types.CaptchaResult, error) {
	if token == "" {
		return types.CaptchaResult{}, errors.New("captcha token is required")
	}
	captchaType = strings.ToLower(captchaType)
	if captchaType == "recaptcha_v2" || captchaType == "recaptcha_v3" {
		captchaType = "recaptcha"
	}
	if captchaType != "recaptcha" && captchaType != "hcaptcha" && captchaType != "turnstile" {
		return types.CaptchaResult{}, fmt.Errorf("unsupported captcha type %q", captchaType)
	}
	code := pagePrelude + `
const result = await page.evaluate(({token, type}) => {
  let fired = false;
  if (type === "recaptcha") {
    for (const el of document.querySelectorAll('textarea[name="g-recaptcha-response"],textarea[name="g-recaptcha-response-100000"]')) { el.value = token; el.innerHTML = token; }
    for (const name of Object.keys(window)) { if (name.startsWith("___grecaptcha_cfg")) { const clients = window[name]?.clients || {}; for (const client of Object.values(clients)) for (const widget of Object.values(client)) if (widget?.callback) { widget.callback(token); fired = true; } } }
  }
  if (type === "hcaptcha") { const field = document.querySelector('textarea[name="h-captcha-response"]'); if (field) { field.value = token; field.innerHTML = token; } }
  if (type === "turnstile") { const field = document.querySelector('input[name="cf-turnstile-response"]'); if (field) field.value = token; }
  return {callback_fired:fired};
}, {token:` + jsString(token) + `, type:` + jsString(captchaType) + `});
return result;`
	var result types.CaptchaResult
	if err := c.execute(ctx, id, code, &result); err != nil {
		return types.CaptchaResult{}, err
	}
	return result, nil
}

func (c *KernelBrowserClient) ClickCaptchaButton(ctx context.Context, id types.ApplicationBrowserID, selector string) (bool, error) {
	if strings.TrimSpace(selector) == "" {
		return false, errors.New("captcha button selector is required")
	}
	code := pagePrelude + ` const button = page.locator(` + jsString(selector) + `); if (await button.count() === 0) return false; await button.first().click(); return true;`
	var clicked bool
	if err := c.execute(ctx, id, code, &clicked); err != nil {
		return false, err
	}
	return clicked, nil
}

func (c *KernelBrowserClient) ClickSubmit(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) (types.SubmissionAttempt, error) {
	if elementIndex < 0 {
		return types.SubmissionAttempt{}, errors.New("element index cannot be negative")
	}
	selector := fmt.Sprintf(`[data-iris-browser-index="%d"]`, elementIndex)
	code := `
` + pagePrelude + `
const beforeURL = page.url();
const pagesBefore = await context.pages();
const requests = new Map();
const pendingResponses = [];
const onRequest = req => requests.set(req, {url:req.url(), method:req.method(), resourceType:req.resourceType(), statusCode:0, responseBody:""});
const onResponse = response => {
  const item = requests.get(response.request()); if (!item) return;
  item.statusCode = response.status();
  pendingResponses.push((async () => { try { item.responseBody = (await response.text()).slice(0,65536); } catch (_) {} })());
};
context.on("request", onRequest);
context.on("response", onResponse);
const target = page.locator(` + jsString(selector) + `);
if (await target.count() === 0) { context.off("request", onRequest); context.off("response", onResponse); throw new Error("submit element index not found"); }
try {
  await target.first().click();
  await page.waitForTimeout(1500);
  await Promise.race([Promise.allSettled(pendingResponses), page.waitForTimeout(1000)]);
} finally {
  context.off("request", onRequest);
  context.off("response", onResponse);
}
const pagesAfter = await context.pages();
const newPage = pagesAfter.find(candidate => !pagesBefore.includes(candidate));
if (newPage) await newPage.bringToFront();
return {before_url:beforeURL, new_tab_opened:!!newPage, requests:[...requests.values()].map(r => ({url:r.url, method:r.method, resource_type:r.resourceType, status_code:r.statusCode, response_body:r.responseBody}))};`
	var attempt types.SubmissionAttempt
	if err := c.execute(ctx, id, code, &attempt); err != nil {
		return types.SubmissionAttempt{}, err
	}
	return attempt, nil
}

func (c *KernelBrowserClient) VerifySubmission(ctx context.Context, id types.ApplicationBrowserID, beforeURL string) (types.SubmissionState, error) {
	code := pagePrelude + `
const text = await page.locator("body").innerText();
const invalid = await page.locator(':invalid').evaluateAll(elements => elements.map(el => el.validationMessage).filter(Boolean));
return {current_url:page.url(), url_changed:page.url() !== ` + jsString(beforeURL) + `, form_present:await page.locator("form").count() > 0, success_text:(text.match(/thank you|application (?:was )?submitted|successfully submitted|submission complete/i) || [""])[0], validation_errors:invalid, page_text:text};`
	var state types.SubmissionState
	if err := c.execute(ctx, id, code, &state); err != nil {
		return types.SubmissionState{}, err
	}
	return state, nil
}

func (c *KernelBrowserClient) execute(ctx context.Context, id types.ApplicationBrowserID, code string, result any) error {
	sessionID, err := c.sessionID(ctx, id)
	if err != nil {
		return types.MutationNotExecuted(err)
	}
	response, err := c.api.execute(ctx, sessionID, code)
	if err != nil {
		var apiErr *kernelsdk.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 &&
			apiErr.StatusCode != http.StatusRequestTimeout && apiErr.StatusCode != http.StatusTooManyRequests {
			return types.MutationNotExecuted(safeSDKError("execute Kernel browser operation", err))
		}
		return safeSDKError("execute Kernel browser operation", err)
	}
	if response == nil {
		return errors.New("Kernel returned an empty execution response")
	}
	if !response.Success {
		err := errors.New("Kernel browser operation failed")
		if response.Error != "" {
			err = fmt.Errorf("Kernel browser operation failed: %s", response.Error)
		}
		if strings.Contains(response.Error, "element index not found") || strings.Contains(response.Error, "file input index not found") {
			return types.MutationNotExecuted(err)
		}
		return err
	}
	if result == nil || response.Result == nil {
		return nil
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		return fmt.Errorf("decode Kernel browser operation result: %w", err)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode Kernel browser operation result: %w", err)
	}
	return nil
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
