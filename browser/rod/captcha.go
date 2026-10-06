package rod

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const injectCaptchaTimeout = 15 * time.Second

var captchaResponseSelectors = []string{
	`textarea[name="g-recaptcha-response"]`, `textarea#g-recaptcha-response`,
	`input[name="g-recaptcha-response"]`, `input[name="cf-turnstile-response"]`,
	`input[name="cf_turnstile_response"]`, `textarea[name="h-captcha-response"]`,
	`input[name="h-captcha-response"]`,
}

func (c *RodBrowserClient) DetectCaptcha(ctx context.Context, id types.ApplicationBrowserID) (types.Captcha, error) {
	s, err := c.get(ctx, id)
	if err != nil {
		return types.Captcha{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return types.Captcha{}, err
	}
	page := s.page.Context(ctx)
	if hasSolvedCaptchaToken(page) {
		return types.Captcha{Type: "none"}, nil
	}
	iframeSrcs, err := elementSrcs(page, "iframe")
	if err != nil {
		return types.Captcha{}, fmt.Errorf("failed to read iframes: %w", err)
	}
	var out types.Captcha
	switch {
	case detectTurnstile(page, iframeSrcs, &out):
	case detectHcaptcha(page, iframeSrcs, &out):
	case detectRecaptchaV2(page, iframeSrcs, &out):
	case detectRecaptchaV3(page, &out):
	default:
		out.Type = "none"
	}
	if out.Type != "none" {
		info, err := page.Info()
		if err != nil {
			return types.Captcha{}, fmt.Errorf("read current page URL: %w", err)
		}
		out.PageURL = info.URL
	}
	return out, nil
}

func hasSolvedCaptchaToken(page *rod.Page) bool {
	for _, selector := range captchaResponseSelectors {
		els, err := page.Elements(selector)
		if err != nil {
			continue
		}
		for _, el := range els {
			obj, err := el.Eval(`() => (this.value || this.innerHTML || "").trim()`)
			if err == nil && obj != nil && obj.Value.Str() != "" {
				return true
			}
		}
	}
	return false
}

func detectTurnstile(page *rod.Page, iframeSrcs []string, out *types.Captcha) bool {
	el := firstElement(page, ".cf-turnstile[data-sitekey]", `[data-sitekey][class*="turnstile"]`)
	if el == nil && !anyContains(iframeSrcs, "challenges.cloudflare.com") {
		return false
	}
	out.Type = "turnstile"
	out.Extra = map[string]string{}
	if el != nil {
		out.SiteKey = attr(el, "data-sitekey")
		out.Action = attr(el, "data-action")
		if value := attr(el, "data-cdata"); value != "" {
			out.Extra["cdata"] = value
		}
	}
	return true
}

func detectHcaptcha(page *rod.Page, iframeSrcs []string, out *types.Captcha) bool {
	frameSrc, hasFrame := firstContaining(iframeSrcs, "hcaptcha.com")
	el := firstElement(page, ".h-captcha[data-sitekey]", "[data-sitekey].h-captcha", `[data-sitekey][class*="h-captcha"]`)
	if el == nil && !hasFrame {
		return false
	}
	out.Type = "hcaptcha"
	if el != nil {
		out.SiteKey = attr(el, "data-sitekey")
		out.Invisible = attr(el, "data-size") == "invisible"
	}
	if out.SiteKey == "" && hasFrame {
		if parsed, err := url.Parse(frameSrc); err == nil {
			out.SiteKey = parsed.Query().Get("sitekey")
			if out.SiteKey == "" {
				if params, err := url.ParseQuery(parsed.Fragment); err == nil {
					out.SiteKey = params.Get("sitekey")
				}
			}
		}
	}
	return true
}

func detectRecaptchaV2(page *rod.Page, iframeSrcs []string, out *types.Captcha) bool {
	anchor, hasAnchor := firstContaining(iframeSrcs, "recaptcha/api2/anchor")
	el := firstElement(page, ".g-recaptcha[data-sitekey]", "[data-sitekey].g-recaptcha")
	if el == nil && !hasAnchor {
		return false
	}
	out.Type = "recaptcha_v2"
	if el != nil {
		out.SiteKey = attr(el, "data-sitekey")
		out.Invisible = attr(el, "data-size") == "invisible"
	}
	if out.SiteKey == "" && hasAnchor {
		if parsed, err := url.Parse(anchor); err == nil {
			out.SiteKey = parsed.Query().Get("k")
			out.Invisible = parsed.Query().Get("size") == "invisible"
		}
	}
	return true
}

func detectRecaptchaV3(page *rod.Page, out *types.Captcha) bool {
	srcs, err := elementSrcs(page, `script[src*="recaptcha/api.js"]`)
	if err != nil {
		return false
	}
	for _, src := range srcs {
		parsed, err := url.Parse(src)
		if err != nil {
			continue
		}
		render := parsed.Query().Get("render")
		if render == "" || render == "explicit" {
			continue
		}
		out.Type, out.SiteKey, out.Invisible, out.Action = "recaptcha_v3", render, true, "submit"
		if actionEl := firstElement(page, "[data-action]"); actionEl != nil {
			if action := attr(actionEl, "data-action"); action != "" {
				out.Action = action
			}
		}
		return true
	}
	return false
}

func (c *RodBrowserClient) InjectCaptchaToken(ctx context.Context, id types.ApplicationBrowserID, captchaType, token string) (types.CaptchaResult, error) {
	s, err := c.get(ctx, id)
	if err != nil {
		return types.CaptchaResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return types.CaptchaResult{}, err
	}
	page := s.page.Context(ctx).Timeout(injectCaptchaTimeout)
	var callbackFired bool
	switch captchaType {
	case "turnstile":
		err = injectTurnstileToken(page, token)
	case "hcaptcha":
		err = injectHcaptchaToken(page, token)
	default:
		callbackFired, err = injectRecaptchaToken(page, token)
	}
	if err != nil {
		return types.CaptchaResult{}, fmt.Errorf("failed to inject captcha token: %w", err)
	}
	return types.CaptchaResult{CallbackFired: callbackFired}, nil
}

func injectRecaptchaToken(page *rod.Page, token string) (bool, error) {
	selectors := []string{`textarea[name="g-recaptcha-response"]`, `textarea#g-recaptcha-response`, `input[name="g-recaptcha-response"]`}
	if err := ensureCaptchaTokenWritten(page, token, selectors...); err != nil {
		return false, err
	}
	return fireRecaptchaCallback(page, token)
}

func injectTurnstileToken(page *rod.Page, token string) error {
	return ensureCaptchaTokenWritten(page, token, `input[name="cf-turnstile-response"]`, `input[name="cf_turnstile_response"]`, `input[name="g-recaptcha-response"]`, `textarea[name="g-recaptcha-response"]`)
}

func injectHcaptchaToken(page *rod.Page, token string) error {
	return ensureCaptchaTokenWritten(page, token, `textarea[name="h-captcha-response"]`, `input[name="h-captcha-response"]`, `textarea[name="g-recaptcha-response"]`, `input[name="g-recaptcha-response"]`)
}

func ensureCaptchaTokenWritten(page *rod.Page, token string, selectors ...string) error {
	wrote, err := setValueAndShow(page, token, selectors...)
	if err != nil {
		return err
	}
	if wrote == 0 {
		_, err = page.Eval(`(token)=>{let el=document.querySelector('textarea[name="g-recaptcha-response"],input[name="g-recaptcha-response"]');if(!el){el=document.createElement('textarea');el.name='g-recaptcha-response';el.id='g-recaptcha-response';el.style.display='none';(document.forms[0]||document.body).appendChild(el);}el.value=token;el.innerHTML=token;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return true;}`, token)
		if err != nil {
			return fmt.Errorf("create captcha response field: %w", err)
		}
		if !hasSolvedCaptchaToken(page) {
			return fmt.Errorf("captcha token was not written to any response field")
		}
	}
	return nil
}

func setValueAndShow(page *rod.Page, value string, selectors ...string) (int, error) {
	wrote := 0
	for _, selector := range selectors {
		els, err := page.Elements(selector)
		if err != nil {
			return wrote, fmt.Errorf("query %q: %w", selector, err)
		}
		for _, el := range els {
			if _, err := el.Eval(`(v)=>{this.style.display='';this.value=v;if(this.tagName==='TEXTAREA')this.innerHTML=v;this.dispatchEvent(new Event('input',{bubbles:true}));this.dispatchEvent(new Event('change',{bubbles:true}));}`, value); err != nil {
				return wrote, fmt.Errorf("set value on %q: %w", selector, err)
			}
			wrote++
		}
	}
	return wrote, nil
}

const fireRecaptchaClientsJS = `(token)=>{let fired=false;try{const cfg=window.___grecaptcha_cfg;if(cfg&&cfg.clients){const visited=new Set(),stack=[];for(const k in cfg.clients)stack.push({node:cfg.clients[k],depth:0});while(stack.length){const {node,depth}=stack.pop();if(!node||typeof node!=='object'||depth>20||visited.has(node))continue;visited.add(node);for(const p in node){const v=node[p];if(typeof v==='function'&&p==='callback'){try{v(token);fired=true}catch(e){}}else if(v&&typeof v==='object')stack.push({node:v,depth:depth+1})}}}}catch(e){}return fired;}`

func fireRecaptchaCallback(page *rod.Page, token string) (bool, error) {
	obj, err := page.Eval(fireRecaptchaClientsJS, token)
	if err != nil {
		return false, fmt.Errorf("fire recaptcha callback: %w", err)
	}
	return obj.Value.Bool(), nil
}

func (c *RodBrowserClient) ClickCaptchaButton(ctx context.Context, id types.ApplicationBrowserID, selector string) (bool, error) {
	s, err := c.get(ctx, id)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return false, err
	}
	page := s.page.Context(ctx)
	if clickInSearchable(page, selector) {
		return true, nil
	}
	frames, err := page.Elements("iframe")
	if err != nil {
		return false, fmt.Errorf("failed to enumerate iframes: %w", err)
	}
	for _, frame := range frames {
		framePage, err := frame.Frame()
		if err == nil && framePage != nil && clickInSearchable(framePage, selector) {
			return true, nil
		}
	}
	return false, nil
}

func clickInSearchable(page *rod.Page, selector string) bool {
	el, err := page.Timeout(2 * time.Second).Element(selector)
	return err == nil && el != nil && el.Click(proto.InputMouseButtonLeft, 1) == nil
}

func firstElement(page *rod.Page, selectors ...string) *rod.Element {
	for _, selector := range selectors {
		if has, el, err := page.Has(selector); err == nil && has {
			return el
		}
	}
	return nil
}

func attr(element *rod.Element, name string) string {
	value, err := element.Attribute(name)
	if err != nil || value == nil {
		return ""
	}
	return *value
}

func elementSrcs(page *rod.Page, selector string) ([]string, error) {
	els, err := page.Elements(selector)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(els))
	for _, el := range els {
		if value := attr(el, "src"); value != "" {
			out = append(out, value)
		}
	}
	return out, nil
}

func anyContains(values []string, substr string) bool {
	_, ok := firstContaining(values, substr)
	return ok
}

func firstContaining(values []string, substr string) (string, bool) {
	for _, value := range values {
		if strings.Contains(value, substr) {
			return value, true
		}
	}
	return "", false
}
