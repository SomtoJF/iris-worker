package browser

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// captchaResponseSelectors are the hidden fields CapSolver tokens are written into.
var captchaResponseSelectors = []string{
	`textarea[name="g-recaptcha-response"]`,
	`textarea#g-recaptcha-response`,
	`input[name="g-recaptcha-response"]`,
	`input[name="cf-turnstile-response"]`,
	`input[name="cf_turnstile_response"]`,
}

// DetectCaptcha inspects the live page for a captcha and returns its type + args.
// Detection is deterministic (DOM inspection via go-rod, no LLM):
//   - Turnstile: a .cf-turnstile[data-sitekey] element OR an iframe src containing
//     "challenges.cloudflare.com". Sitekey from data-sitekey.
//   - reCAPTCHA v2: a .g-recaptcha[data-sitekey] element OR an iframe src containing
//     "recaptcha/api2/anchor". Sitekey from data-sitekey or the iframe "k" param.
//   - reCAPTCHA v3: a script src "recaptcha/api.js?render=SITEKEY" and no anchor iframe.
//
// If a non-empty token is already present in a captcha response field (e.g. after a
// successful InjectCaptchaToken), returns type=none so the agent loop does not re-solve.
// This matters especially for v3: the render= script stays on the page forever.
func (a *Activity) DetectCaptcha(ctx context.Context, input DetectCaptchaInput) (DetectCaptchaOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return DetectCaptchaOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	if hasSolvedCaptchaToken(page) {
		return DetectCaptchaOutput{Type: CaptchaTypeNone}, nil
	}

	iframeSrcs, err := elementSrcs(page, "iframe")
	if err != nil {
		return DetectCaptchaOutput{}, fmt.Errorf("failed to read iframes: %w", err)
	}

	var out DetectCaptchaOutput
	switch {
	case detectTurnstile(page, iframeSrcs, &out):
	case detectRecaptchaV2(page, iframeSrcs, &out):
	case detectRecaptchaV3(page, &out):
	default:
		out.Type = CaptchaTypeNone
	}

	if out.Type != CaptchaTypeNone {
		out.PageURL = page.MustInfo().URL
	}
	return out, nil
}

// hasSolvedCaptchaToken reports whether any captcha response field already holds a token.
func hasSolvedCaptchaToken(page *rod.Page) bool {
	for _, selector := range captchaResponseSelectors {
		els, err := page.Elements(selector)
		if err != nil {
			continue
		}
		for _, el := range els {
			obj, err := el.Eval(`() => (this.value || this.innerHTML || "").trim()`)
			if err != nil || obj == nil {
				continue
			}
			if obj.Value.Str() != "" {
				return true
			}
		}
	}
	return false
}

func detectTurnstile(page *rod.Page, iframeSrcs []string, out *DetectCaptchaOutput) bool {
	el := firstElement(page, ".cf-turnstile[data-sitekey]", `[data-sitekey][class*="turnstile"]`)
	hasFrame := anyContains(iframeSrcs, "challenges.cloudflare.com")
	if el == nil && !hasFrame {
		return false
	}

	out.Type = CaptchaTypeTurnstile
	out.Extra = map[string]string{}
	if el != nil {
		out.SiteKey = attr(el, "data-sitekey")
		if action := attr(el, "data-action"); action != "" {
			out.Action = action
		}
		if cdata := attr(el, "data-cdata"); cdata != "" {
			out.Extra["cdata"] = cdata
		}
	}
	return true
}

func detectRecaptchaV2(page *rod.Page, iframeSrcs []string, out *DetectCaptchaOutput) bool {
	anchorSrc, hasAnchor := firstContaining(iframeSrcs, "recaptcha/api2/anchor")
	el := firstElement(page, ".g-recaptcha[data-sitekey]", "[data-sitekey].g-recaptcha")
	if el == nil && !hasAnchor {
		return false
	}

	out.Type = CaptchaTypeRecaptchaV2
	if el != nil {
		out.SiteKey = attr(el, "data-sitekey")
		out.Invisible = attr(el, "data-size") == "invisible"
	}
	if out.SiteKey == "" && hasAnchor {
		if u, err := url.Parse(anchorSrc); err == nil {
			out.SiteKey = u.Query().Get("k")
			if u.Query().Get("size") == "invisible" {
				out.Invisible = true
			}
		}
	}
	return true
}

func detectRecaptchaV3(page *rod.Page, out *DetectCaptchaOutput) bool {
	scriptSrcs, err := elementSrcs(page, `script[src*="recaptcha/api.js"]`)
	if err != nil {
		return false
	}

	for _, src := range scriptSrcs {
		u, err := url.Parse(src)
		if err != nil {
			continue
		}
		render := u.Query().Get("render")
		if render == "" || render == "explicit" {
			continue
		}

		out.Type = CaptchaTypeRecaptchaV3
		out.SiteKey = render
		out.Invisible = true
		out.Action = "submit"
		if actionEl := firstElement(page, "[data-action]"); actionEl != nil {
			if action := attr(actionEl, "data-action"); action != "" {
				out.Action = action
			}
		}
		return true
	}
	return false
}

// injectCaptchaTimeout bounds DOM/JS work so a busy page or circular grecaptcha
// object graph cannot pin the activity until StartToClose.
const injectCaptchaTimeout = 15 * time.Second

// InjectCaptchaToken writes the solved token into the page's hidden response field and,
// for reCAPTCHA, fires the widget callback where possible.
func (a *Activity) InjectCaptchaToken(ctx context.Context, input InjectCaptchaTokenInput) (InjectCaptchaTokenOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return InjectCaptchaTokenOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	// Bound every rod call; do not MustWaitIdle — captcha pages keep network/JS busy
	// (recaptcha iframes, analytics) so requestIdleCallback often never fires.
	page = page.Timeout(injectCaptchaTimeout)

	var callbackFired bool
	var err error
	if input.Type == CaptchaTypeTurnstile {
		err = injectTurnstileToken(page, input.Token)
	} else {
		callbackFired, err = injectRecaptchaToken(page, input.Token)
	}
	if err != nil {
		return InjectCaptchaTokenOutput{}, fmt.Errorf("failed to inject captcha token: %w", err)
	}

	return InjectCaptchaTokenOutput{CallbackFired: callbackFired}, nil
}

// injectRecaptchaToken writes the token into every g-recaptcha-response field, then
// fires any registered grecaptcha client callback. Setting the field value is done via
// go-rod per element; firing the callback requires walking the window.___grecaptcha_cfg
// object graph, which is not DOM and has no go-rod/Go equivalent, so that single step
// stays as a focused page-level script.
func injectRecaptchaToken(page *rod.Page, token string) (bool, error) {
	selectors := []string{
		`textarea[name="g-recaptcha-response"]`,
		`textarea#g-recaptcha-response`,
		`input[name="g-recaptcha-response"]`,
	}
	if err := ensureCaptchaTokenWritten(page, token, selectors...); err != nil {
		return false, err
	}
	return fireRecaptchaCallback(page, token)
}

// injectTurnstileToken writes the token into the cf-turnstile-response inputs (and the
// g-recaptcha-response field, which Turnstile-backed forms sometimes reuse).
func injectTurnstileToken(page *rod.Page, token string) error {
	selectors := []string{
		`input[name="cf-turnstile-response"]`,
		`input[name="cf_turnstile_response"]`,
		`input[name="g-recaptcha-response"]`,
		`textarea[name="g-recaptcha-response"]`,
	}
	return ensureCaptchaTokenWritten(page, token, selectors...)
}

// ensureCaptchaTokenWritten sets the token on matching fields, creating a hidden
// g-recaptcha-response textarea when none exist (common for v3 before first execute).
func ensureCaptchaTokenWritten(page *rod.Page, token string, selectors ...string) error {
	wrote, err := setValueAndShow(page, token, selectors...)
	if err != nil {
		return err
	}
	if wrote > 0 {
		return nil
	}

	// No response field yet — create one so DetectCaptcha can see the solved token
	// and the form can submit it.
	_, err = page.Eval(`(token) => {
		let el = document.querySelector('textarea[name="g-recaptcha-response"], input[name="g-recaptcha-response"]');
		if (!el) {
			el = document.createElement('textarea');
			el.name = 'g-recaptcha-response';
			el.id = 'g-recaptcha-response';
			el.style.display = 'none';
			(document.forms[0] || document.body).appendChild(el);
		}
		el.value = token;
		el.innerHTML = token;
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
		return true;
	}`, token)
	if err != nil {
		return fmt.Errorf("create captcha response field: %w", err)
	}
	if !hasSolvedCaptchaToken(page) {
		return fmt.Errorf("captcha token was not written to any response field")
	}
	return nil
}

// setValueAndShow sets el.value = value on every element matching any selector and
// clears any display:none so hidden response fields are populated. Returns how many
// elements were updated.
func setValueAndShow(page *rod.Page, value string, selectors ...string) (int, error) {
	wrote := 0
	for _, selector := range selectors {
		els, err := page.Elements(selector)
		if err != nil {
			return wrote, fmt.Errorf("query %q: %w", selector, err)
		}
		for _, el := range els {
			if _, err := el.Eval(`(v) => {
				this.style.display = '';
				this.value = v;
				if (this.tagName === 'TEXTAREA') this.innerHTML = v;
				this.dispatchEvent(new Event('input', { bubbles: true }));
				this.dispatchEvent(new Event('change', { bubbles: true }));
			}`, value); err != nil {
				return wrote, fmt.Errorf("set value on %q: %w", selector, err)
			}
			wrote++
		}
	}
	return wrote, nil
}

// fireRecaptchaClientsJS walks the grecaptcha client registry and invokes the first
// callback found for each client. Returns whether any callback fired. This operates on
// the window object graph (not the DOM), so it has no typed go-rod equivalent.
// Visited-set + depth cap are required: ___grecaptcha_cfg is cyclic and an unbounded
// walk hangs the Eval forever (which looked like a MustWaitIdle hang in practice).
const fireRecaptchaClientsJS = `(token) => {
	let fired = false;
	try {
		const cfg = window.___grecaptcha_cfg;
		if (cfg && cfg.clients) {
			const visited = new Set();
			const maxDepth = 20;
			for (const k in cfg.clients) {
				const stack = [{ node: cfg.clients[k], depth: 0 }];
				while (stack.length) {
					const { node, depth } = stack.pop();
					if (!node || typeof node !== 'object' || depth > maxDepth) continue;
					if (visited.has(node)) continue;
					visited.add(node);
					for (const p in node) {
						const v = node[p];
						if (typeof v === 'function' && p === 'callback') {
							try { v(token); fired = true; } catch (e) {}
						} else if (v && typeof v === 'object') {
							stack.push({ node: v, depth: depth + 1 });
						}
					}
				}
			}
		}
	} catch (e) {}
	return fired;
}`

func fireRecaptchaCallback(page *rod.Page, token string) (bool, error) {
	obj, err := page.Eval(fireRecaptchaClientsJS, token)
	if err != nil {
		return false, fmt.Errorf("fire recaptcha callback: %w", err)
	}
	return obj.Value.Bool(), nil
}

// ClickCaptchaButton clicks the first element matching selector on the page or inside
// any iframe. Used to press a post-solve confirm/submit button when injection alone
// isn't enough. The repo otherwise only clicks by tagged-node index; captcha buttons
// often live in iframes, so this is selector + frame based.
func (a *Activity) ClickCaptchaButton(ctx context.Context, input ClickCaptchaButtonInput) (ClickCaptchaButtonOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return ClickCaptchaButtonOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	if clickInSearchable(page, input.Selector) {
		return ClickCaptchaButtonOutput{Clicked: true}, nil
	}

	frames, err := page.Elements("iframe")
	if err != nil {
		return ClickCaptchaButtonOutput{}, fmt.Errorf("failed to enumerate iframes: %w", err)
	}
	for _, frameEl := range frames {
		framePage, err := frameEl.Frame()
		if err != nil || framePage == nil {
			continue
		}
		if clickInSearchable(framePage, input.Selector) {
			return ClickCaptchaButtonOutput{Clicked: true}, nil
		}
	}

	return ClickCaptchaButtonOutput{Clicked: false}, nil
}

// clickInSearchable attempts to find and click a selector within a page or frame.
// rod's *rod.Page returned by Element.Frame() shares the Element/Click API.
func clickInSearchable(p *rod.Page, selector string) bool {
	el, err := p.Timeout(2 * time.Second).Element(selector)
	if err != nil || el == nil {
		return false
	}
	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return false
	}
	return true
}

// ====== SMALL DOM HELPERS ======

// firstElement returns the first element matching any of the selectors, or nil.
func firstElement(page *rod.Page, selectors ...string) *rod.Element {
	for _, selector := range selectors {
		has, el, err := page.Has(selector)
		if err == nil && has {
			return el
		}
	}
	return nil
}

// attr returns an element attribute value, or "" if absent/error.
func attr(el *rod.Element, name string) string {
	v, err := el.Attribute(name)
	if err != nil || v == nil {
		return ""
	}
	return *v
}

// elementSrcs returns the "src" attribute of every element matching selector.
func elementSrcs(page *rod.Page, selector string) ([]string, error) {
	els, err := page.Elements(selector)
	if err != nil {
		return nil, err
	}
	srcs := make([]string, 0, len(els))
	for _, el := range els {
		if src := attr(el, "src"); src != "" {
			srcs = append(srcs, src)
		}
	}
	return srcs, nil
}

func anyContains(values []string, substr string) bool {
	_, ok := firstContaining(values, substr)
	return ok
}

func firstContaining(values []string, substr string) (string, bool) {
	for _, v := range values {
		if strings.Contains(v, substr) {
			return v, true
		}
	}
	return "", false
}
