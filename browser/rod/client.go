package rod

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/SomtoJF/iris-worker/initializers/fs"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/google/uuid"
)

type Config struct {
	SessionStore types.SessionStore
	TempFS       *fs.TemporaryFileSystem
	WorkerID     string
}

type RodBrowserClient struct {
	store    types.SessionStore
	tempFS   *fs.TemporaryFileSystem
	mu       sync.Mutex
	sessions map[types.ApplicationBrowserID]*applicationSession
}

type applicationSession struct {
	mu                sync.Mutex
	closed            bool
	browser           *rod.Browser
	launcher          *launcher.Launcher
	page              *rod.Page
	taggedNodes       []taggedNode
	fileInputs        []taggedFileInput
	providerSessionID string
	replayGeneration  uint64
}

type taggedNode struct {
	dto     types.TaggedNode
	element *rod.Element
}

type taggedFileInput struct {
	dto     types.TaggedFileInput
	element *rod.Element
}

func NewRodBrowserClient(cfg Config) (*RodBrowserClient, error) {
	return &RodBrowserClient{
		store:    cfg.SessionStore,
		tempFS:   cfg.TempFS,
		sessions: make(map[types.ApplicationBrowserID]*applicationSession),
	}, nil
}

func (*RodBrowserClient) GetBrowserProvider() string { return types.BrowserProviderRod }

// StartBrowser is idempotent within the owning worker process. Rod browser handles
// are process-local, so a persisted active session without a local handle is an error.
func (c *RodBrowserClient) StartBrowser(ctx context.Context, id types.ApplicationBrowserID, opts types.BrowserOptions) error {
	if id == "" {
		return fmt.Errorf("application browser ID is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if sess, ok := c.sessions[id]; ok {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return c.ensureHealthy(ctx, id, sess)
	}
	replayGeneration := uint64(1)
	if c.store != nil {
		ref, found, err := c.store.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("load browser session: %w", err)
		}
		if found && ref.Provider != types.BrowserProviderRod {
			return fmt.Errorf("application browser session belongs to provider %q", ref.Provider)
		}
		if found && ref.Status == "active" {
			return fmt.Errorf("active Rod browser session %q has no local handle; Rod sessions require the owning worker process", id)
		}
		if found && ref.ReplayGeneration > 0 {
			replayGeneration = ref.ReplayGeneration
		}
	}

	sess, err := launchApplication(ctx, opts.StartingURL)
	if err != nil {
		return err
	}
	sess.providerSessionID = sessionID()
	sess.replayGeneration = replayGeneration
	if c.store != nil {
		if err := c.store.Save(ctx, id, types.SessionRef{
			Provider:          types.BrowserProviderRod,
			ProviderSessionID: sess.providerSessionID,
			Status:            "active",
			ReplayGeneration:  replayGeneration,
			ReplayPending:     true,
		}); err != nil {
			_ = closeSession(sess)
			return fmt.Errorf("save browser session: %w", err)
		}
	}
	c.sessions[id] = sess
	return nil
}

func launchApplication(ctx context.Context, startingURL string) (*applicationSession, error) {
	path, found := launcher.LookPath()
	if !found {
		return nil, fmt.Errorf("find Chromium executable: none found")
	}
	l := launcher.New().Bin(path).
		Set("disable-dev-shm-usage").
		Set("disable-gpu").
		NoSandbox(true)
	url, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("launch application browser: %w", err)
	}
	browser := rod.New().ControlURL(url).NoDefaultDevice()
	if err := browser.Connect(); err != nil {
		l.Kill()
		return nil, fmt.Errorf("connect application browser: %w", err)
	}
	sess := &applicationSession{browser: browser, launcher: l}
	if err := rod.Try(func() {
		sess.page = stealthPage(browser)
		if startingURL != "" {
			sess.page.MustNavigate(startingURL)
		}
	}); err != nil {
		_ = closeSession(sess)
		return nil, fmt.Errorf("open application page: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = closeSession(sess)
		return nil, err
	}
	return sess, nil
}

func sessionID() string {
	return uuid.NewString()
}

func stealthPage(browser *rod.Browser) *rod.Page {
	page := stealth.MustPage(browser)
	page.MustWindowFullscreen()
	return page
}

func (c *RodBrowserClient) get(ctx context.Context, id types.ApplicationBrowserID) (*applicationSession, error) {
	c.mu.Lock()
	sess := c.sessions[id]
	c.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("no active browser for application %s", id)
	}
	sess.mu.Lock()
	err := c.ensureHealthy(ctx, id, sess)
	sess.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (c *RodBrowserClient) ensureHealthy(ctx context.Context, id types.ApplicationBrowserID, sess *applicationSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess.closed || sess.browser == nil {
		return fmt.Errorf("browser session is closed")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := (proto.BrowserGetVersion{}).Call(sess.browser.Context(checkCtx)); err == nil {
		return nil
	} else if ctx.Err() != nil {
		return ctx.Err()
	}

	restoreURL := ""
	if sess.page != nil {
		if info, err := sess.page.Info(); err == nil {
			restoreURL = info.URL
		}
	}
	slog.Warn("Rod application browser unhealthy; relaunching", "applicationBrowserID", id)
	old := &applicationSession{browser: sess.browser, launcher: sess.launcher}
	_ = closeSession(old)
	fresh, err := launchApplication(ctx, restoreURL)
	if err != nil {
		sess.browser, sess.launcher, sess.page = nil, nil, nil
		sess.closed = true
		return fmt.Errorf("relaunch application browser: %w", err)
	}
	fresh.providerSessionID = sessionID()
	fresh.replayGeneration = sess.replayGeneration
	sess.browser, sess.launcher, sess.page = fresh.browser, fresh.launcher, fresh.page
	sess.providerSessionID = fresh.providerSessionID
	sess.replayGeneration = fresh.replayGeneration
	sess.taggedNodes, sess.fileInputs = nil, nil
	if c.store != nil {
		if err := c.store.Save(ctx, id, types.SessionRef{
			Provider: types.BrowserProviderRod, ProviderSessionID: sess.providerSessionID,
			Status: "active", ReplayGeneration: sess.replayGeneration,
		}); err != nil {
			_ = closeSession(sess)
			sess.closed = true
			_ = c.store.ClearActive(context.WithoutCancel(ctx), id)
			return fmt.Errorf("save relaunched browser session: %w", err)
		}
	}
	return nil
}

func (c *RodBrowserClient) screenshotPath(fileName string) string {
	if c.tempFS != nil {
		return c.tempFS.ConcatenatePath(fileName)
	}
	return filepath.Join(os.TempDir(), fileName)
}

func (c *RodBrowserClient) CloseBrowser(ctx context.Context, id types.ApplicationBrowserID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	sess := c.sessions[id]
	var closeErr error
	if sess != nil {
		sess.mu.Lock()
		delete(c.sessions, id)
		closeErr = closeSession(sess)
		sess.closed = true
		sess.mu.Unlock()
	}
	if c.store != nil {
		if err := c.store.ClearActive(context.WithoutCancel(ctx), id); err != nil {
			closeErr = joinErrors(closeErr, fmt.Errorf("clear browser session: %w", err))
		}
	}
	return closeErr
}

func closeSession(sess *applicationSession) error {
	var err error
	if sess.browser != nil {
		if closeErr := sess.browser.Close(); closeErr != nil {
			err = closeErr
		}
		sess.browser = nil
	}
	if sess.launcher != nil {
		sess.launcher.Kill()
		sess.launcher = nil
	}
	sess.page = nil
	sess.taggedNodes = nil
	sess.fileInputs = nil
	return err
}

func joinErrors(first, next error) error {
	if first == nil {
		return next
	}
	return errors.Join(first, next)
}

func ensureSessionOpen(sess *applicationSession) error {
	if sess == nil || sess.closed || sess.browser == nil || sess.page == nil {
		return fmt.Errorf("browser session is closed")
	}
	return nil
}
