package browser

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

func skipIfNoBrowser(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, has := launcher.LookPath(); !has {
		t.Skip("no chromium binary found")
	}
}

func launchBrowser(t *testing.T) (*rod.Browser, *launcher.Launcher) {
	t.Helper()
	path, _ := launcher.LookPath()
	l := launcher.New().Bin(path).Set("disable-dev-shm-usage").Set("disable-gpu").NoSandbox(true)
	u, err := l.Launch()
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = browser.Close()
		l.Kill()
	})
	return browser.NoDefaultDevice(), l
}

func killLauncher(t *testing.T, l *launcher.Launcher) {
	t.Helper()
	pid := l.PID()
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	_ = p.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.Signal(syscall.Signal(0)) != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestClosePageBestEffort(t *testing.T) {
	skipIfNoBrowser(t)
	a := NewActivities(nil)

	browser, l := launchBrowser(t)
	incognito := browser.MustIncognito()
	page := incognito.MustPage("about:blank")

	wfID := "wf-close"
	a.mu.Lock()
	a.activeSessions[wfID] = page
	a.incognitoContexts[wfID] = incognito
	a.lastTaggedNodes[wfID] = nil
	a.lastTaggedFileInputs[wfID] = nil
	a.mu.Unlock()

	killLauncher(t, l)

	if err := a.ClosePage(context.Background(), ClosePageInput{WorkflowID: wfID}); err != nil {
		t.Fatalf("ClosePage returned error: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.activeSessions[wfID]; ok {
		t.Error("activeSessions not cleared")
	}
	if _, ok := a.incognitoContexts[wfID]; ok {
		t.Error("incognitoContexts not cleared")
	}
	if _, ok := a.lastTaggedNodes[wfID]; ok {
		t.Error("lastTaggedNodes not cleared")
	}
	if _, ok := a.lastTaggedFileInputs[wfID]; ok {
		t.Error("lastTaggedFileInputs not cleared")
	}
}

func TestClosePageNoActivePage(t *testing.T) {
	a := NewActivities(nil)
	if err := a.ClosePage(context.Background(), ClosePageInput{WorkflowID: "unknown"}); err == nil {
		t.Fatal("expected error for unknown workflow")
	}
}
