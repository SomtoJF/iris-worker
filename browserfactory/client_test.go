package browserfactory

import (
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SomtoJF/iris-worker/initializers/fs"
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

func newFactory(t *testing.T) *BrowserFactory {
	t.Helper()
	bf, err := NewBrowserFactory(fs.NewTemporaryFilesystem())
	if err != nil {
		t.Fatalf("NewBrowserFactory: %v", err)
	}
	t.Cleanup(func() {
		if bf.browser != nil {
			_ = bf.browser.Close()
		}
		if bf.launcher != nil {
			bf.launcher.Kill()
		}
	})
	return bf
}

func procAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func killChromium(t *testing.T, bf *BrowserFactory) {
	t.Helper()
	if bf.launcher == nil {
		t.Fatal("launcher nil; cannot kill chromium")
	}
	pid := bf.launcher.PID()
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	_ = p.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for procAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if procAlive(pid) {
		t.Fatalf("chromium pid %d still alive", pid)
	}
}

func TestHealthyLiveBrowser(t *testing.T) {
	skipIfNoBrowser(t)
	bf := newFactory(t)

	if !bf.healthy() {
		t.Fatal("expected healthy browser")
	}
	b, err := bf.GetBrowser()
	if err != nil {
		t.Fatalf("GetBrowser: %v", err)
	}
	if b != bf.browser {
		t.Fatal("expected same browser pointer")
	}
}

func TestHealthyAfterKill(t *testing.T) {
	skipIfNoBrowser(t)
	bf := newFactory(t)
	killChromium(t, bf)

	if bf.healthy() {
		t.Fatal("expected unhealthy after kill")
	}
}

func TestGetBrowserRelaunches(t *testing.T) {
	skipIfNoBrowser(t)
	bf := newFactory(t)
	killChromium(t, bf)

	b, err := bf.GetBrowser()
	if err != nil {
		t.Fatalf("GetBrowser after kill: %v", err)
	}
	err = rod.Try(func() {
		page := b.MustIncognito().MustPage("about:blank")
		page.MustClose()
	})
	if err != nil {
		t.Fatalf("relaunched browser unusable: %v", err)
	}
}

func TestGetBrowserConcurrent(t *testing.T) {
	skipIfNoBrowser(t)
	bf := newFactory(t)
	killChromium(t, bf)

	const n = 10
	var wg sync.WaitGroup
	results := make([]*rod.Browser, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = bf.GetBrowser()
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got different browser pointer", i)
		}
	}
}

func TestNoZombieProcess(t *testing.T) {
	skipIfNoBrowser(t)
	bf := newFactory(t)

	var oldPIDs []int
	for c := 0; c < 2; c++ {
		if bf.launcher != nil {
			oldPIDs = append(oldPIDs, bf.launcher.PID())
		}
		killChromium(t, bf)
		if _, err := bf.GetBrowser(); err != nil {
			t.Fatalf("cycle %d GetBrowser: %v", c, err)
		}
	}

	for _, pid := range oldPIDs {
		if procAlive(pid) {
			t.Fatalf("zombie chromium pid %d still alive", pid)
		}
	}
}
