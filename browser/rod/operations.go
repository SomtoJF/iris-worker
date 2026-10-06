package rod

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func (c *RodBrowserClient) Navigate(ctx context.Context, id types.ApplicationBrowserID, targetURL string) error {
	s, err := c.get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return err
	}
	page := s.page.Context(ctx)
	if err := page.Navigate(targetURL); err != nil {
		return fmt.Errorf("navigate to %s: %w", targetURL, err)
	}
	if err := rod.Try(func() { page.MustWaitLoad() }); err != nil {
		return fmt.Errorf("wait for navigation: %w", err)
	}
	s.page = page.Context(context.Background())
	s.taggedNodes = nil
	s.fileInputs = nil
	return nil
}

func (c *RodBrowserClient) ScreenshotForLLM(ctx context.Context, id types.ApplicationBrowserID, fileName string) (types.Screenshot, error) {
	s, err := c.get(ctx, id)
	if err != nil {
		return types.Screenshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return types.Screenshot{}, err
	}
	page := s.page.Context(ctx)
	path := c.screenshotPath(fileName)
	out, nodes, inputs, err := screenshotForLLM(page, path)
	if err != nil {
		return types.Screenshot{}, err
	}
	s.taggedNodes = nodes
	s.fileInputs = inputs
	out.Path = path
	return out, nil
}

func screenshotForLLM(page *rod.Page, path string) (out types.Screenshot, nodes []taggedNode, inputs []taggedFileInput, err error) {
	err = rod.Try(func() {
		page.MustWaitLoad()
		if info, infoErr := page.Info(); infoErr == nil && info != nil {
			out.CurrentURL = info.URL
		}
		out.HasVisibleAlerts = page.MustEval(`() => [...document.querySelectorAll('[role="alert"],[aria-live="assertive"]')].some(el => {
			const r = el.getBoundingClientRect(), s = getComputedStyle(el);
			return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none";
		})`).Bool()
		accessibilityTree, _ := getPageAccessibilityTree(page)
		fileInputElements, getErr := getFileInputElements(page)
		if getErr != nil {
			panic(getErr)
		}
		drawTransparentGrid(page)
		defer func() {
			_, _ = page.Eval(`() => {
				document.getElementById('agent-grid')?.remove();
				document.querySelectorAll('.agent-tag').forEach(el => el.remove());
			}`)
		}()
		nodes = tagAccessibilityNodes(page, accessibilityTree)
		page.MustScreenshot(path)
		for _, node := range nodes {
			out.TaggedNodes = append(out.TaggedNodes, node.dto)
		}
		inputs = tagFileInputNodes(page, fileInputElements)
		for _, input := range inputs {
			out.TaggedFileInputNodes = append(out.TaggedFileInputNodes, input.dto)
		}
	})
	return out, nodes, inputs, err
}

func getPageAccessibilityTree(page *rod.Page) ([]*proto.AccessibilityAXNode, error) {
	res, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return nil, err
	}
	return res.Nodes, nil
}

func getFileInputElements(page *rod.Page) ([]*rod.Element, error) {
	if err := page.WaitStable(200 * time.Millisecond); err != nil {
		return nil, err
	}
	return page.Elements("input[type='file']")
}

func tagFileInputNodes(page *rod.Page, elements []*rod.Element) []taggedFileInput {
	out := make([]taggedFileInput, 0, len(elements))
	for i, element := range elements {
		labelHTML := getLabelHTMLForInput(page, element)
		var label *string
		if labelHTML != "" {
			label = &labelHTML
		}
		html, _ := element.HTML()
		value, _ := element.Attribute("value")
		name, _ := element.Attribute("name")
		nameValue := ""
		if name != nil {
			nameValue = *name
		}
		out = append(out, taggedFileInput{
			dto:     types.TaggedFileInput{Index: i, HTML: html, Name: nameValue, Label: label, Value: value},
			element: element,
		})
	}
	return out
}

func getLabelHTMLForInput(page *rod.Page, input *rod.Element) string {
	id, err := input.Attribute("id")
	if err != nil || id == nil || *id == "" {
		return ""
	}
	labels, err := page.Elements("label")
	if err != nil {
		return ""
	}
	for _, label := range labels {
		forAttr, err := label.Attribute("for")
		if err != nil || forAttr == nil || *forAttr != *id {
			continue
		}
		html, _ := label.HTML()
		return html
	}
	return ""
}

func drawTransparentGrid(page *rod.Page) {
	page.MustEval(`() => {
		const canvas = document.createElement('canvas');
		canvas.id = 'agent-grid';
		canvas.style = 'position:fixed; top:0; left:0; pointer-events:none; z-index:9999;';
		canvas.width = window.innerWidth;
		canvas.height = window.innerHeight;
		const ctx = canvas.getContext('2d');
		ctx.strokeStyle = 'rgba(255, 0, 0, 0.2)';
		for(let i=0; i<canvas.width; i+=100) { ctx.strokeRect(i, 0, 0, canvas.height); }
		for(let i=0; i<canvas.height; i+=100) { ctx.strokeRect(0, i, canvas.width, 0); }
		document.body.appendChild(canvas);
	}`)
}

func tagAccessibilityNodes(page *rod.Page, tree []*proto.AccessibilityAXNode) []taggedNode {
	var focusable []*proto.AccessibilityAXNode
	for _, node := range tree {
		if !node.Ignored && isInteractive(node) && node.BackendDOMNodeID != 0 {
			focusable = append(focusable, node)
		}
	}
	out := make([]taggedNode, 0, len(focusable))
	for _, node := range focusable {
		bounds := getNodeBounds(page, node)
		if bounds == nil {
			continue
		}
		index := len(out)
		page.MustEval(`(x, y, i) => {
			const tag = document.createElement('div');
			tag.className = 'agent-tag';
			tag.innerText = i;
			tag.style = `+"`"+`position:fixed;left:${x}px;top:${y}px;background:#f00;color:#fff;padding:2px 4px;font-size:10px;font-weight:bold;border-radius:3px;z-index:1000000;pointer-events:none;`+"`"+`;
			document.body.appendChild(tag);
		}`, bounds.X, bounds.Y, index)
		element := getElementFromNode(page, node)
		role := nodeRole(node)
		name := nodeName(node)
		label := elementLabel(element)
		submit := isSubmitElement(element, name, label)
		if isPasswordElement(element) {
			role = "password"
		}
		value := nodeValue(node, element)
		var required *bool
		if isFormRole(role) {
			v := isNodeRequired(node, element)
			required = &v
		}
		var checked *string
		if role == "checkbox" || role == "radio" || role == "switch" {
			if v := checkboxCheckedState(node); v != "" {
				checked = &v
			}
		}
		out = append(out, taggedNode{
			dto: types.TaggedNode{
				Index: index, Description: descriptionFromNode(node, index), Name: name, Label: label, Submit: submit,
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
				Role: role, Value: &value, Required: required, Checked: checked,
			},
			element: element,
		})
	}
	return out
}

func nodeRole(node *proto.AccessibilityAXNode) string {
	if node.Role != nil && !node.Role.Value.Nil() {
		return node.Role.Value.String()
	}
	return ""
}

func nodeName(node *proto.AccessibilityAXNode) string {
	if node.Name != nil && !node.Name.Value.Nil() {
		return strings.TrimSpace(node.Name.Value.String())
	}
	return ""
}

func elementLabel(element *rod.Element) string {
	if element == nil {
		return ""
	}
	label, err := element.Eval(`() => [...(this.labels || [])].map(el => el.innerText.trim()).filter(Boolean).join(" ") || this.getAttribute("aria-label") || ""`)
	if err != nil || label == nil || label.Value.Nil() {
		return ""
	}
	return strings.TrimSpace(label.Value.Str())
}

func isSubmitElement(element *rod.Element, name, label string) bool {
	text := strings.ToLower(strings.TrimSpace(name + " " + label))
	if text == "submit" || strings.Contains(text, "submit application") ||
		text == "apply" || text == "apply now" || strings.Contains(text, "send application") {
		return true
	}
	if element != nil {
		value, err := element.Attribute("type")
		return err == nil && value != nil && strings.EqualFold(*value, "submit") && text == ""
	}
	return false
}

func isPasswordElement(element *rod.Element) bool {
	if element == nil {
		return false
	}
	value, err := element.Attribute("type")
	return err == nil && value != nil && strings.EqualFold(*value, "password")
}

func nodeValue(node *proto.AccessibilityAXNode, element *rod.Element) string {
	if node.Value != nil && !node.Value.Value.Nil() {
		return node.Value.Value.String()
	}
	if nodeRole(node) == "combobox" && element != nil {
		value, err := element.Eval(`() => { const c=this.closest('.select__control'); const v=c?.querySelector('[class*="single-value"]'); return v ? v.innerText : ""; }`)
		if err == nil {
			return value.Value.String()
		}
	}
	return ""
}

func isNodeRequired(node *proto.AccessibilityAXNode, element *rod.Element) bool {
	if element != nil {
		if v, err := element.Attribute("required"); err == nil && v != nil {
			return true
		}
		if v, err := element.Attribute("aria-required"); err == nil && v != nil {
			switch strings.ToLower(strings.TrimSpace(*v)) {
			case "true", "1":
				return true
			case "false", "0":
				return false
			}
		}
	}
	for _, prop := range node.Properties {
		if prop.Name == "required" && prop.Value != nil {
			return prop.Value.Value.Bool()
		}
	}
	return false
}

func descriptionFromNode(node *proto.AccessibilityAXNode, index int) string {
	name, role := "", nodeRole(node)
	if node.Name != nil && !node.Name.Value.Nil() {
		name = node.Name.Value.String()
	}
	return fmt.Sprintf("Tag %d: Role: %s - Name: %s", index, strings.ToUpper(role), name)
}

func checkboxCheckedState(node *proto.AccessibilityAXNode) string {
	for _, prop := range node.Properties {
		if prop.Name == "checked" && prop.Value != nil {
			return prop.Value.Value.String()
		}
	}
	return ""
}

func isFormRole(role string) bool {
	switch role {
	case "checkbox", "radio", "switch", "textbox", "searchbox", "textarea", "select", "combobox":
		return true
	default:
		return false
	}
}

func isInteractive(node *proto.AccessibilityAXNode) bool {
	roles := map[string]bool{
		"button": true, "link": true, "textbox": true, "checkbox": true,
		"radio": true, "combobox": true, "menuitem": true, "searchbox": true,
		"switch": true, "slider": true, "tab": true, "option": true,
		"select": true, "label": true, "textarea": true, "input": true,
	}
	role := strings.ToLower(nodeRole(node))
	return role != "rootwebarea" && roles[role]
}

func getNodeBounds(page *rod.Page, node *proto.AccessibilityAXNode) *proto.DOMRect {
	if node.BackendDOMNodeID == 0 {
		return nil
	}
	res, err := proto.DOMGetBoxModel{BackendNodeID: node.BackendDOMNodeID}.Call(page)
	if err != nil || res.Model == nil || len(res.Model.Border) < 6 {
		return nil
	}
	return &proto.DOMRect{
		X: res.Model.Border[0], Y: res.Model.Border[1],
		Width:  res.Model.Border[2] - res.Model.Border[0],
		Height: res.Model.Border[5] - res.Model.Border[1],
	}
}

func getElementFromNode(page *rod.Page, node *proto.AccessibilityAXNode) *rod.Element {
	if node.BackendDOMNodeID == 0 {
		return nil
	}
	el, err := page.ElementFromNode(&proto.DOMNode{BackendNodeID: node.BackendDOMNodeID})
	if err != nil {
		return nil
	}
	return el
}

func findTaggedNode(nodes []taggedNode, index int) (*rod.Element, error) {
	indices := make([]int, 0, len(nodes))
	for _, node := range nodes {
		indices = append(indices, node.dto.Index)
		if node.dto.Index == index {
			if node.element == nil {
				return nil, fmt.Errorf("element at index %d has no DOM element", index)
			}
			return node.element, nil
		}
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no tagged nodes cached for this page; call TakeScreenshot before interacting")
	}
	return nil, fmt.Errorf("element index %d not found among %d tagged nodes (indices=%v)", index, len(nodes), indices)
}

func (c *RodBrowserClient) Click(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) error {
	s, err := c.get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return err
	}
	element, err := findTaggedNode(s.taggedNodes, elementIndex)
	if err != nil {
		return err
	}
	page := s.page.Context(ctx)
	element = element.Context(ctx)
	before, err := s.browser.Pages()
	if err != nil {
		return fmt.Errorf("enumerate application tabs: %w", err)
	}
	if err := clickElement(element); err != nil {
		return fmt.Errorf("click element: %w", err)
	}
	time.Sleep(time.Second)
	after, err := s.browser.Pages()
	if err != nil {
		return fmt.Errorf("enumerate application tabs after click: %w", err)
	}
	if len(after) > len(before) {
		newPage := after[len(after)-1]
		if err := newPage.Timeout(10 * time.Second).WaitLoad(); err == nil {
			s.page = newPage.Context(context.Background())
			s.taggedNodes = nil
			s.fileInputs = nil
			page = s.page
		}
	}
	_ = page.Timeout(3 * time.Second).WaitIdle(time.Second)
	return nil
}

func clickElement(element *rod.Element) error {
	if err := element.Timeout(8*time.Second).Click(proto.InputMouseButtonLeft, 1); err == nil {
		return nil
	}
	_, err := element.Eval(`() => { this.click(); return true; }`)
	return err
}

func (c *RodBrowserClient) Type(ctx context.Context, id types.ApplicationBrowserID, field types.FieldInput) error {
	s, err := c.get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return err
	}
	page := s.page.Context(ctx)
	element, err := findTaggedNode(s.taggedNodes, field.ElementIndex)
	if err != nil {
		return err
	}
	element = element.Context(ctx)
	if err := typeField(page, element, s.taggedNodes, field); err != nil {
		return err
	}
	return nil
}

func typeField(page *rod.Page, element *rod.Element, nodes []taggedNode, field types.FieldInput) error {
	editable, err := elementIsEditable(element)
	if err != nil {
		return fmt.Errorf("failed to inspect element %d: %w", field.ElementIndex, err)
	}
	if !editable {
		role := ""
		for _, node := range nodes {
			if node.dto.Index == field.ElementIndex {
				role = node.dto.Role
				break
			}
		}
		return fmt.Errorf("element index %d is not editable (role=%q tag); use click for buttons/radios/checkboxes", field.ElementIndex, role)
	}
	if field.Replace {
		if err := clearElementText(element); err != nil {
			return fmt.Errorf("failed to clear text: %w", err)
		}
	}
	if err := element.Timeout(8 * time.Second).Input(field.Text); err != nil {
		_, evalErr := element.Eval(`(text) => { const el=this; el.focus(); if ('value' in el) { el.value=text; el.dispatchEvent(new Event('input',{bubbles:true})); el.dispatchEvent(new Event('change',{bubbles:true})); return 'value'; } if (el.isContentEditable) { el.textContent=text; el.dispatchEvent(new Event('input',{bubbles:true})); return 'contenteditable'; } return 'noop'; }`, field.Text)
		if evalErr != nil {
			return fmt.Errorf("failed to type text: %w (js fallback: %v)", err, evalErr)
		}
	}
	_ = page.Timeout(3 * time.Second).WaitIdle(time.Second)
	return nil
}

func elementIsEditable(element *rod.Element) (bool, error) {
	res, err := element.Eval(`() => { const el=this, tag=(el.tagName||'').toLowerCase(); if(el.isContentEditable)return true; if(tag==='textarea')return true; if(tag==='input'){const t=(el.getAttribute('type')||'text').toLowerCase();return !['button','submit','reset','checkbox','radio','file','image','hidden'].includes(t);} return 'value' in el && (tag==='select'||el.getAttribute('role')==='textbox'); }`)
	if err != nil {
		return false, err
	}
	return res.Value.Bool(), nil
}

func clearElementText(element *rod.Element) error {
	_, err := element.Eval(`() => { const el=this; if(typeof el.select==='function'){el.focus();el.select();return 'select';} if(el.isContentEditable){el.focus();const r=document.createRange();r.selectNodeContents(el);const s=window.getSelection();s.removeAllRanges();s.addRange(r);return 'contenteditable';} if('value' in el){el.focus();el.value='';el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'value';} return 'noop'; }`)
	return err
}

func (c *RodBrowserClient) Scroll(ctx context.Context, id types.ApplicationBrowserID, direction string, ratio float64) error {
	if ratio < 0.1 || ratio > 1 {
		return fmt.Errorf("scroll ratio must be between 0.1 and 1.0, got %f", ratio)
	}
	multiplier := 1.0
	if direction == "up" {
		multiplier = -1
	} else if direction != "down" {
		return fmt.Errorf("scroll direction must be 'up' or 'down', got %s", direction)
	}
	s, err := c.get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return err
	}
	page := s.page.Context(ctx)
	_, err = page.Eval(`(ratio,mult)=>{window.scrollBy({top:window.innerHeight*ratio*mult,behavior:'instant'});}`, ratio, multiplier)
	if err != nil {
		return fmt.Errorf("failed to scroll: %w", err)
	}
	_ = page.Timeout(3 * time.Second).WaitIdle(time.Second)
	return nil
}

func (c *RodBrowserClient) UploadFile(ctx context.Context, id types.ApplicationBrowserID, fileInputIndex int, filePath string) error {
	s, err := c.get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return err
	}
	page := s.page.Context(ctx)
	if len(s.fileInputs) == 0 {
		_, _, inputs, getErr := screenshotForLLM(page, c.screenshotPath("temp.png"))
		if getErr != nil {
			return fmt.Errorf("get tagged file inputs: %w", getErr)
		}
		s.fileInputs = inputs
	}
	indices := make([]int, 0, len(s.fileInputs))
	for _, input := range s.fileInputs {
		indices = append(indices, input.dto.Index)
		if input.dto.Index == fileInputIndex {
			if input.element == nil {
				return fmt.Errorf("file input at index %d has no DOM element", fileInputIndex)
			}
			if err := input.element.Context(ctx).SetFiles([]string{filePath}); err != nil {
				return fmt.Errorf("failed to upload file: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("file input index %d not found among %d tagged file inputs (indices=%v)", fileInputIndex, len(s.fileInputs), indices)
}
