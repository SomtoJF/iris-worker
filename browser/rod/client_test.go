package rod

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/SomtoJF/iris-worker/initializers/fs"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

type memorySessionStore struct {
	cleared int
	ref     types.SessionRef
	found   bool
}

func (s *memorySessionStore) Get(context.Context, types.ApplicationBrowserID) (types.SessionRef, bool, error) {
	return s.ref, s.found, nil
}
func (s *memorySessionStore) Save(_ context.Context, _ types.ApplicationBrowserID, ref types.SessionRef) error {
	s.ref, s.found = ref, true
	return nil
}
func (s *memorySessionStore) ClearActive(context.Context, types.ApplicationBrowserID) error {
	s.cleared++
	s.ref = types.SessionRef{}
	s.found = false
	return nil
}
func (s *memorySessionStore) CompleteReplay(context.Context, types.ApplicationBrowserID, string) error {
	s.ref.ReplayPending = false
	return nil
}

func TestCloseBrowserWithoutSessionIsIdempotent(t *testing.T) {
	store := &memorySessionStore{}
	client, err := NewRodBrowserClient(Config{SessionStore: store})
	if err != nil {
		t.Fatalf("NewRodBrowserClient: %v", err)
	}
	id := types.ApplicationBrowserID("app-1")
	for range 2 {
		if err := client.CloseBrowser(context.Background(), id); err != nil {
			t.Fatalf("CloseBrowser: %v", err)
		}
	}
	if store.cleared != 2 {
		t.Fatalf("ClearActive called %d times, want 2", store.cleared)
	}
}

func TestFindTaggedNodeUsesPaintedIndex(t *testing.T) {
	first, seventh := &rod.Element{}, &rod.Element{}
	nodes := []taggedNode{
		{dto: types.TaggedNode{Index: 2}, element: first},
		{dto: types.TaggedNode{Index: 7}, element: seventh},
	}
	got, err := findTaggedNode(nodes, 7)
	if err != nil {
		t.Fatalf("findTaggedNode: %v", err)
	}
	if got != seventh {
		t.Fatal("resolved slice offset instead of painted index")
	}
	_, err = findTaggedNode(nodes, 1)
	if err == nil || !strings.Contains(err.Error(), "indices=[2 7]") {
		t.Fatalf("missing index error = %v, want actual painted indices", err)
	}
}

func TestStartBrowserRequiresApplicationID(t *testing.T) {
	client, err := NewRodBrowserClient(Config{})
	if err != nil {
		t.Fatalf("NewRodBrowserClient: %v", err)
	}
	if err := client.StartBrowser(context.Background(), "", types.BrowserOptions{}); err == nil {
		t.Fatal("expected empty ID error")
	}
}

func TestApplicationsUseIsolatedBrowsers(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration test")
	}
	if _, found := launcher.LookPath(); !found {
		t.Skip("no Chromium executable found")
	}
	client, err := NewRodBrowserClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := types.ApplicationBrowserID("application-1"), types.ApplicationBrowserID("application-2")
	if err := client.StartBrowser(context.Background(), firstID, types.BrowserOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := client.StartBrowser(context.Background(), secondID, types.BrowserOptions{}); err != nil {
		_ = client.CloseBrowser(context.Background(), firstID)
		t.Fatal(err)
	}
	first, _ := client.get(context.Background(), firstID)
	second, _ := client.get(context.Background(), secondID)
	if first.browser == second.browser || first.launcher.PID() == second.launcher.PID() {
		t.Fatal("applications share a browser process")
	}
	oldPID := first.launcher.PID()
	process, err := os.FindProcess(oldPID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := client.Navigate(context.Background(), firstID, "about:blank"); err != nil {
		t.Fatalf("relaunch unhealthy application browser: %v", err)
	}
	first, _ = client.get(context.Background(), firstID)
	if first.launcher.PID() == oldPID {
		t.Fatal("unhealthy browser was not relaunched")
	}
	if err := client.CloseBrowser(context.Background(), firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := second.browser.Pages(); err != nil {
		t.Fatalf("closing first browser affected second: %v", err)
	}
	if err := client.CloseBrowser(context.Background(), secondID); err != nil {
		t.Fatal(err)
	}
}

func TestRodBrowserOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration test")
	}
	if _, found := launcher.LookPath(); !found {
		t.Skip("no Chromium executable found")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/recaptcha/api.js", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = fmt.Fprintf(w, "Application submitted for %s", r.FormValue("name"))
			return
		}
		_, _ = io.WriteString(w, `<!doctype html><html><body>
			<form method="post" action="/submit">
				<label for="name">Name</label><input id="name" name="name" required>
				<label for="resume">Resume</label><input id="resume" name="resume" type="file">
				<button type="submit">Submit application</button>
			</form>
			<script src="/recaptcha/api.js?render=test-site-key"></script>
		</body></html>`)
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, _ = fmt.Fprintf(w, "Application submitted for %s", r.FormValue("name"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tempFS := fs.NewTemporaryFilesystem()
	t.Cleanup(tempFS.Cleanup)
	client, err := NewRodBrowserClient(Config{TempFS: tempFS})
	if err != nil {
		t.Fatal(err)
	}
	id := types.ApplicationBrowserID("operations-test")
	if err := client.StartBrowser(context.Background(), id, types.BrowserOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.CloseBrowser(context.Background(), id) })
	if err := client.Navigate(context.Background(), id, server.URL); err != nil {
		t.Fatal(err)
	}

	screenshot, err := client.ScreenshotForLLM(context.Background(), id, "form.png")
	if err != nil {
		t.Fatalf("ScreenshotForLLM: %v", err)
	}
	if _, err := os.Stat(screenshot.Path); err != nil {
		t.Fatalf("screenshot file: %v", err)
	}
	if len(screenshot.TaggedNodes) == 0 || len(screenshot.TaggedFileInputNodes) != 1 {
		t.Fatalf("screenshot tags = %d nodes, %d file inputs", len(screenshot.TaggedNodes), len(screenshot.TaggedFileInputNodes))
	}

	var textIndex, submitIndex = -1, -1
	for _, node := range screenshot.TaggedNodes {
		if strings.EqualFold(node.Role, "textbox") {
			textIndex = node.Index
		}
		if strings.EqualFold(node.Role, "button") {
			submitIndex = node.Index
		}
	}
	if textIndex < 0 || submitIndex < 0 {
		t.Fatalf("expected tagged textbox and button, got %+v", screenshot.TaggedNodes)
	}
	if err := client.Type(context.Background(), id, types.FieldInput{ElementIndex: textIndex, Text: "Ada", Replace: true}); err != nil {
		t.Fatalf("Type: %v", err)
	}
	filePath := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(filePath, []byte("resume"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.UploadFile(context.Background(), id, screenshot.TaggedFileInputNodes[0].Index, filePath); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	captcha, err := client.DetectCaptcha(context.Background(), id)
	if err != nil {
		t.Fatalf("DetectCaptcha: %v", err)
	}
	if captcha.Type != "recaptcha_v3" || captcha.SiteKey != "test-site-key" || captcha.PageURL == "" {
		t.Fatalf("captcha = %+v", captcha)
	}
	if _, err := client.InjectCaptchaToken(context.Background(), id, captcha.Type, "test-token"); err != nil {
		t.Fatalf("InjectCaptchaToken: %v", err)
	}
	captcha, err = client.DetectCaptcha(context.Background(), id)
	if err != nil || captcha.Type != "none" {
		t.Fatalf("DetectCaptcha after token = %+v, %v", captcha, err)
	}

	attempt, err := client.ClickSubmit(context.Background(), id, submitIndex)
	if err != nil {
		t.Fatalf("ClickSubmit: %v", err)
	}
	if !strings.HasPrefix(attempt.BeforeURL, server.URL) || len(attempt.Requests) == 0 || attempt.Requests[0].StatusCode != http.StatusOK {
		t.Fatalf("submission attempt = %+v", attempt)
	}
	state, err := client.VerifySubmission(context.Background(), id, attempt.BeforeURL)
	if err != nil {
		t.Fatalf("VerifySubmission: %v", err)
	}
	if state.SuccessText != "application submitted" || !state.URLChanged {
		t.Fatalf("submission state = %+v", state)
	}
	text, err := client.ScrapeRenderedPage(context.Background(), id)
	if err != nil || !strings.Contains(text, "Application submitted for Ada") {
		t.Fatalf("ScrapeRenderedPage = %q, %v", text, err)
	}
}
