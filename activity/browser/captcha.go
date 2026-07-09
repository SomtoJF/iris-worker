package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// detectCaptchaJS inspects the page and its iframes for known captcha markers and
// returns a JSON-serializable result. It is deterministic — no LLM involved.
//
// Detection rules:
//   - reCAPTCHA v2: a .g-recaptcha[data-sitekey] element OR an iframe whose src
//     contains "recaptcha/api2/anchor". Sitekey from data-sitekey or the iframe "k" param.
//   - reCAPTCHA v3: a script src containing "recaptcha/api.js?render=SITEKEY" and NO
//     api2/anchor iframe. Sitekey from the render param.
//   - Turnstile: a .cf-turnstile[data-sitekey] element OR an iframe src containing
//     "challenges.cloudflare.com". Sitekey from data-sitekey.
const detectCaptchaJS = `() => {
	const result = { type: "none", site_key: "", action: "", invisible: false, extra: {} };
	const iframes = Array.from(document.querySelectorAll('iframe'));

	// --- Turnstile ---
	const tsEl = document.querySelector('.cf-turnstile[data-sitekey], [data-sitekey][class*="turnstile"]');
	const tsFrame = iframes.find(f => (f.src || '').includes('challenges.cloudflare.com'));
	if (tsEl || tsFrame) {
		result.type = "turnstile";
		result.site_key = (tsEl && tsEl.getAttribute('data-sitekey')) || '';
		if (tsEl) {
			const a = tsEl.getAttribute('data-action');
			if (a) result.action = a;
			const c = tsEl.getAttribute('data-cdata');
			if (c) result.extra.cdata = c;
		}
		return result;
	}

	// --- reCAPTCHA v2 (anchor widget present) ---
	const anchorFrame = iframes.find(f => (f.src || '').includes('recaptcha/api2/anchor'));
	const grEl = document.querySelector('.g-recaptcha[data-sitekey], [data-sitekey].g-recaptcha');
	if (anchorFrame || grEl) {
		result.type = "recaptcha_v2";
		if (grEl) {
			result.site_key = grEl.getAttribute('data-sitekey') || '';
			result.invisible = (grEl.getAttribute('data-size') === 'invisible');
		}
		if (!result.site_key && anchorFrame) {
			try {
				const u = new URL(anchorFrame.src);
				result.site_key = u.searchParams.get('k') || '';
				if (u.searchParams.get('size') === 'invisible') result.invisible = true;
			} catch (e) {}
		}
		return result;
	}

	// --- reCAPTCHA v3 (render script, no anchor) ---
	const scripts = Array.from(document.querySelectorAll('script[src*="recaptcha/api.js"]'));
	const renderScript = scripts.find(s => /[?&]render=/.test(s.src) && !/render=explicit/.test(s.src));
	if (renderScript) {
		try {
			const u = new URL(renderScript.src);
			const render = u.searchParams.get('render');
			if (render && render !== 'explicit') {
				result.type = "recaptcha_v3";
				result.site_key = render;
				result.invisible = true;
				const actionEl = document.querySelector('[data-action]');
				result.action = (actionEl && actionEl.getAttribute('data-action')) || 'submit';
			}
		} catch (e) {}
	}

	return result;
}`

// DetectCaptcha inspects the live page for a captcha and returns its type + args.
func (a *Activity) DetectCaptcha(ctx context.Context, input DetectCaptchaInput) (DetectCaptchaOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return DetectCaptchaOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	var detected struct {
		Type      string            `json:"type"`
		SiteKey   string            `json:"site_key"`
		Action    string            `json:"action"`
		Invisible bool              `json:"invisible"`
		Extra     map[string]string `json:"extra"`
	}

	obj, err := page.Eval(detectCaptchaJS)
	if err != nil {
		return DetectCaptchaOutput{}, fmt.Errorf("failed to eval captcha detection: %w", err)
	}
	if err := obj.Value.Unmarshal(&detected); err != nil {
		return DetectCaptchaOutput{}, fmt.Errorf("failed to parse captcha detection: %w", err)
	}

	if detected.Type == "" {
		detected.Type = CaptchaTypeNone
	}

	out := DetectCaptchaOutput{
		Type:      detected.Type,
		SiteKey:   detected.SiteKey,
		Action:    detected.Action,
		Invisible: detected.Invisible,
		Extra:     detected.Extra,
	}
	if out.Type != CaptchaTypeNone {
		out.PageURL = page.MustInfo().URL
	}
	return out, nil
}

// injectRecaptchaJS writes the token into every g-recaptcha-response field and fires
// any registered grecaptcha client callbacks. Returns whether a callback was fired.
const injectRecaptchaJS = `(token) => {
	let fired = false;
	document.querySelectorAll('textarea[name="g-recaptcha-response"], textarea#g-recaptcha-response').forEach(el => {
		el.style.display = '';
		el.value = token;
	});
	// Some pages read the token from a hidden input of the same name.
	document.querySelectorAll('input[name="g-recaptcha-response"]').forEach(el => { el.value = token; });
	try {
		if (window.___grecaptcha_cfg && window.___grecaptcha_cfg.clients) {
			const clients = window.___grecaptcha_cfg.clients;
			for (const k in clients) {
				const client = clients[k];
				const stack = [client];
				while (stack.length) {
					const node = stack.pop();
					if (!node || typeof node !== 'object') continue;
					for (const p in node) {
						const v = node[p];
						if (typeof v === 'function' && p === 'callback') {
							try { v(token); fired = true; } catch (e) {}
						} else if (v && typeof v === 'object') {
							stack.push(v);
						}
					}
				}
			}
		}
	} catch (e) {}
	return fired;
}`

// injectTurnstileJS writes the token into cf-turnstile-response inputs. Turnstile's
// success callback normally sets these; setting them directly satisfies most forms.
const injectTurnstileJS = `(token) => {
	let found = false;
	document.querySelectorAll('input[name="cf-turnstile-response"], input[name="cf_turnstile_response"]').forEach(el => {
		el.value = token;
		found = true;
	});
	// g-recaptcha-response is sometimes reused by Turnstile-backed forms.
	document.querySelectorAll('input[name="g-recaptcha-response"], textarea[name="g-recaptcha-response"]').forEach(el => {
		el.value = token;
	});
	return found;
}`

// InjectCaptchaToken writes the solved token into the page's hidden response field and
// fires the widget callback where possible.
func (a *Activity) InjectCaptchaToken(ctx context.Context, input InjectCaptchaTokenInput) (InjectCaptchaTokenOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return InjectCaptchaTokenOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	js := injectRecaptchaJS
	if input.Type == CaptchaTypeTurnstile {
		js = injectTurnstileJS
	}

	obj, err := page.Eval(js, input.Token)
	if err != nil {
		return InjectCaptchaTokenOutput{}, fmt.Errorf("failed to inject captcha token: %w", err)
	}

	page.MustWaitIdle()
	return InjectCaptchaTokenOutput{CallbackFired: obj.Value.Bool()}, nil
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

	if clicked := clickInSearchable(page, input.Selector); clicked {
		page.MustWaitIdle()
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
		if clicked := clickInSearchable(framePage, input.Selector); clicked {
			page.MustWaitIdle()
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
