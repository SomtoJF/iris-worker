package kernel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/SomtoJF/iris-worker/initializers/fs"
	kernelsdk "github.com/kernel/kernel-go-sdk"
	"github.com/kernel/kernel-go-sdk/option"
)

const (
	defaultTimeoutSeconds = 60 * 2 // timeout after 2 minutes of inactivity
	cleanupTimeout        = 30 * time.Second
	profileLeaseBuffer    = 5 * time.Minute
)

type Config struct {
	SessionStore types.SessionStore
	TempFS       *fs.TemporaryFileSystem
	APIKey       string
}

type KernelBrowserClient struct {
	api          kernelAPI
	sessionStore types.SessionStore
	tempFS       *fs.TemporaryFileSystem
	locks        sync.Map
}

type kernelAPI interface {
	newBrowser(context.Context, kernelsdk.BrowserNewParams) (*kernelsdk.BrowserNewResponse, error)
	getBrowser(context.Context, string) (*kernelsdk.BrowserGetResponse, error)
	deleteBrowser(context.Context, string) error
	execute(context.Context, string, string) (*kernelsdk.BrowserPlaywrightExecuteResponse, error)
	newProfile(context.Context, string) (*kernelsdk.Profile, error)
	getProfile(context.Context, string) (*kernelsdk.Profile, error)
	upsertVault(context.Context, string) (*kernelsdk.Vault, error)
}

type sdkAPI struct{ client kernelsdk.Client }

func (a sdkAPI) newBrowser(ctx context.Context, params kernelsdk.BrowserNewParams) (*kernelsdk.BrowserNewResponse, error) {
	return a.client.Browsers.New(ctx, params)
}

func (a sdkAPI) getBrowser(ctx context.Context, id string) (*kernelsdk.BrowserGetResponse, error) {
	return a.client.Browsers.Get(ctx, id, kernelsdk.BrowserGetParams{})
}

func (a sdkAPI) deleteBrowser(ctx context.Context, id string) error {
	return a.client.Browsers.DeleteByID(ctx, id)
}

func (a sdkAPI) execute(ctx context.Context, id, code string) (*kernelsdk.BrowserPlaywrightExecuteResponse, error) {
	return a.client.Browsers.Playwright.Execute(ctx, id, kernelsdk.BrowserPlaywrightExecuteParams{Code: code})
}

func (a sdkAPI) newProfile(ctx context.Context, name string) (*kernelsdk.Profile, error) {
	return a.client.Profiles.New(ctx, kernelsdk.ProfileNewParams{Name: kernelsdk.String(name)})
}

func (a sdkAPI) getProfile(ctx context.Context, idOrName string) (*kernelsdk.Profile, error) {
	return a.client.Profiles.Get(ctx, idOrName)
}

func (a sdkAPI) upsertVault(ctx context.Context, name string) (*kernelsdk.Vault, error) {
	return a.client.Vaults.Upsert(ctx, kernelsdk.VaultUpsertParams{Name: name})
}

func NewKernelBrowserClient(cfg Config) (*KernelBrowserClient, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("kernel API key is required")
	}
	if cfg.SessionStore == nil {
		return nil, errors.New("kernel session store is required")
	}
	if cfg.TempFS == nil {
		return nil, errors.New("kernel temporary filesystem is required")
	}
	client := kernelsdk.NewClient(option.WithAPIKey(cfg.APIKey))
	return newKernelBrowserClient(cfg, sdkAPI{client: client}), nil
}

func newKernelBrowserClient(cfg Config, api kernelAPI) *KernelBrowserClient {
	return &KernelBrowserClient{api: api, sessionStore: cfg.SessionStore, tempFS: cfg.TempFS}
}

func (c *KernelBrowserClient) GetBrowserProvider() string { return types.BrowserProviderKernel }

func (c *KernelBrowserClient) StartBrowser(ctx context.Context, id types.ApplicationBrowserID, opts types.BrowserOptions) (retErr error) {
	if id == "" {
		return errors.New("application browser ID is required")
	}
	unlock := c.lock(id)
	defer unlock()

	ref, found, err := c.sessionStore.Get(ctx, id)
	if err != nil {
		return safeError("load Kernel browser session", err)
	}
	if found {
		if ref.Provider != types.BrowserProviderKernel {
			return fmt.Errorf("application browser session belongs to provider %q", ref.Provider)
		}
		if ref.ProviderSessionID != "" && ref.Status == "active" {
			if _, err := c.api.getBrowser(ctx, ref.ProviderSessionID); err != nil {
				if !isNotFound(err) {
					return safeSDKError("reconnect Kernel browser session", err)
				}
				if err := c.sessionStore.ClearActive(ctx, id); err != nil {
					return safeError("clear expired Kernel browser session", err)
				}
			} else {
				return c.navigateInitial(ctx, id, opts.StartingURL)
			}
		}
	}
	if ref.ReplayGeneration == 0 {
		ref.ReplayGeneration = 1
	}

	var resources types.KernelProfileVault
	resourceStore, persistResources := c.sessionStore.(types.KernelProfileVaultStore)
	leaseOwner := string(id)
	leaseAcquired := false
	keepLease := false
	if persistResources {
		resources, err = resourceStore.EnsureKernelProfileVault(ctx, id)
		if err != nil {
			return safeError("load Kernel profile and vault references", err)
		}
		if resources.ProfileID == 0 || resources.VaultID == 0 {
			return errors.New("Kernel profile and vault references are incomplete")
		}
		leaseExpiresAt := time.Now().UTC().Add(time.Duration(defaultTimeoutSeconds)*time.Second + profileLeaseBuffer)
		leaseAcquired, err = resourceStore.AcquireKernelProfileLease(ctx, resources.ProfileID, leaseOwner, leaseExpiresAt)
		if err != nil {
			return safeError("acquire Kernel profile lease", err)
		}
		if !leaseAcquired {
			return errors.New("Kernel profile is in use by another browser session")
		}
		defer func() {
			if !leaseAcquired || keepLease {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			defer cancel()
			if err := resourceStore.ReleaseKernelProfileLease(cleanupCtx, resources.ProfileID, leaseOwner); err != nil {
				retErr = errors.Join(retErr, safeError("release Kernel profile lease", err))
			}
		}()
		resources, err = c.ensureKernelResources(ctx, resources)
		if err != nil {
			return err
		}
		if err := resourceStore.SaveKernelProfileVault(ctx, id, resources); err != nil {
			return safeError("persist Kernel profile and vault references", err)
		}
		ref.BrowserProfileID = &resources.ProfileID
		ref.BrowserVaultID = &resources.VaultID
	}

	params := browserParams(opts.StartingURL)
	if persistResources {
		params.Profile = kernelsdk.BrowserProfileParam{
			ID:          kernelsdk.String(resources.ProfileProviderID),
			SaveChanges: kernelsdk.Bool(true),
		}
		params.Vaults = []kernelsdk.VaultReferenceParam{{ID: kernelsdk.String(resources.VaultProviderID)}}
	}
	created, err := c.api.newBrowser(ctx, params)
	if err != nil {
		return safeSDKError("create Kernel browser", err)
	}
	if created == nil || created.SessionID == "" {
		return errors.New("Kernel returned an empty browser session ID")
	}
	ref = types.SessionRef{
		Provider:          types.BrowserProviderKernel,
		ProviderSessionID: created.SessionID,
		LiveViewURL:       created.BrowserLiveViewURL,
		Status:            "active",
		ReplayGeneration:  ref.ReplayGeneration,
		ReplayPending:     true,
		BrowserProfileID:  ref.BrowserProfileID,
		BrowserVaultID:    ref.BrowserVaultID,
	}
	if err := c.sessionStore.Save(ctx, id, ref); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		deleteErr := c.api.deleteBrowser(cleanupCtx, created.SessionID)
		clearErr := c.sessionStore.ClearActive(cleanupCtx, id)
		return errors.Join(
			safeError("persist Kernel browser session", err),
			wrapCleanupError("delete unpersisted Kernel browser", safeSDKError("Kernel delete", deleteErr)),
			wrapCleanupError("clear incomplete Kernel browser record", safeError("clear incomplete Kernel browser record", clearErr)),
		)
	}
	keepLease = leaseAcquired
	return c.navigateInitial(ctx, id, opts.StartingURL)
}

func browserParams(startingURL string) kernelsdk.BrowserNewParams {
	params := kernelsdk.BrowserNewParams{
		Headless:       kernelsdk.Bool(false),
		Stealth:        kernelsdk.Bool(true),
		TimeoutSeconds: kernelsdk.Int(defaultTimeoutSeconds),
	}
	if startingURL != "" {
		params.StartURL = kernelsdk.String(startingURL)
	}
	return params
}

func (c *KernelBrowserClient) ensureKernelResources(ctx context.Context, resources types.KernelProfileVault) (types.KernelProfileVault, error) {
	profileName := resources.ProfileName
	if profileName == "" {
		return resources, errors.New("Kernel profile name is required")
	}
	var profile *kernelsdk.Profile
	var err error
	if resources.ProfileProviderID != "" {
		profile, err = c.api.getProfile(ctx, resources.ProfileProviderID)
	}
	if resources.ProfileProviderID == "" || isNotFound(err) {
		profile, err = c.api.getProfile(ctx, profileName)
		if isNotFound(err) {
			profile, err = c.api.newProfile(ctx, profileName)
			if isConflict(err) {
				profile, err = c.api.getProfile(ctx, profileName)
			}
		}
	}
	if err != nil {
		return resources, safeSDKError("load Kernel profile", err)
	}
	if profile == nil || profile.ID == "" {
		return resources, errors.New("Kernel returned an empty profile ID")
	}
	resources.ProfileProviderID = profile.ID
	if profile.Name != "" {
		resources.ProfileName = profile.Name
	}

	vaultName := resources.VaultName
	if vaultName == "" {
		return resources, errors.New("Kernel vault name is required")
	}
	vault, err := c.api.upsertVault(ctx, vaultName)
	if err != nil {
		return resources, safeSDKError("load Kernel vault", err)
	}
	if vault == nil || vault.ID == "" {
		return resources, errors.New("Kernel returned an empty vault ID")
	}
	resources.VaultProviderID = vault.ID
	if vault.Name != "" {
		resources.VaultName = vault.Name
	}
	return resources, nil
}

func (c *KernelBrowserClient) navigateInitial(ctx context.Context, id types.ApplicationBrowserID, startingURL string) error {
	if startingURL == "" {
		return nil
	}
	return c.Navigate(ctx, id, startingURL)
}

func (c *KernelBrowserClient) CloseBrowser(ctx context.Context, id types.ApplicationBrowserID) error {
	if id == "" {
		return errors.New("application browser ID is required")
	}
	unlock := c.lock(id)
	defer unlock()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	ref, found, err := c.sessionStore.Get(cleanupCtx, id)
	if err != nil {
		return safeError("load Kernel browser session for close", err)
	}
	if !found || ref.ProviderSessionID == "" {
		if !found {
			return nil
		}
		if err := c.sessionStore.ClearActive(cleanupCtx, id); err != nil {
			return safeError("clear inactive Kernel browser session", err)
		}
		return c.releaseProfileLease(cleanupCtx, id, ref)
	}
	if ref.Provider != types.BrowserProviderKernel {
		return fmt.Errorf("application browser session belongs to provider %q", ref.Provider)
	}
	if err := c.api.deleteBrowser(cleanupCtx, ref.ProviderSessionID); err != nil && !isNotFound(err) {
		return safeSDKError("delete Kernel browser session", err)
	}
	if err := c.sessionStore.ClearActive(cleanupCtx, id); err != nil {
		return safeError("clear Kernel browser session", err)
	}
	return c.releaseProfileLease(cleanupCtx, id, ref)
}

func (c *KernelBrowserClient) releaseProfileLease(ctx context.Context, id types.ApplicationBrowserID, ref types.SessionRef) error {
	if ref.BrowserProfileID == nil {
		return nil
	}
	store, ok := c.sessionStore.(types.KernelProfileVaultStore)
	if !ok {
		return errors.New("Kernel profile persistence is unavailable")
	}
	if err := store.ReleaseKernelProfileLease(ctx, *ref.BrowserProfileID, string(id)); err != nil {
		return safeError("release Kernel profile lease", err)
	}
	return nil
}

func (c *KernelBrowserClient) sessionID(ctx context.Context, id types.ApplicationBrowserID) (string, error) {
	if id == "" {
		return "", errors.New("application browser ID is required")
	}
	ref, found, err := c.sessionStore.Get(ctx, id)
	if err != nil {
		return "", safeError("load Kernel browser session", err)
	}
	if !found || ref.ProviderSessionID == "" {
		return "", errors.New("Kernel browser session is not active")
	}
	if ref.Provider != types.BrowserProviderKernel {
		return "", fmt.Errorf("application browser session belongs to provider %q", ref.Provider)
	}
	if ref.BrowserProfileID != nil {
		store, ok := c.sessionStore.(types.KernelProfileVaultStore)
		if !ok {
			return "", errors.New("Kernel profile persistence is unavailable")
		}
		expiresAt := time.Now().UTC().Add(time.Duration(defaultTimeoutSeconds)*time.Second + profileLeaseBuffer)
		acquired, err := store.AcquireKernelProfileLease(ctx, *ref.BrowserProfileID, string(id), expiresAt)
		if err != nil {
			return "", safeError("renew Kernel profile lease", err)
		}
		if !acquired {
			return "", errors.New("Kernel profile lease is no longer owned by this browser session")
		}
	}
	return ref.ProviderSessionID, nil
}

func (c *KernelBrowserClient) lock(id types.ApplicationBrowserID) func() {
	value, _ := c.locks.LoadOrStore(id, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func isNotFound(err error) bool {
	var apiErr *kernelsdk.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func isConflict(err error) bool {
	var apiErr *kernelsdk.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict
}

type safeCauseError struct {
	message string
	cause   error
}

func (e safeCauseError) Error() string { return e.message }
func (e safeCauseError) Unwrap() error { return e.cause }

func safeError(action string, cause error) error {
	if cause == nil {
		return nil
	}
	return safeCauseError{message: action + " failed", cause: cause}
}

func safeSDKError(action string, cause error) error {
	if cause == nil {
		return nil
	}
	var apiErr *kernelsdk.Error
	if errors.As(cause, &apiErr) {
		return safeCauseError{message: fmt.Sprintf("%s failed (Kernel HTTP %d)", action, apiErr.StatusCode), cause: cause}
	}
	return safeError(action, cause)
}

func wrapCleanupError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

var _ kernelAPI = sdkAPI{}

// This mirrors browser.BrowserClient without importing the root package, which
// constructs providers and would create an import cycle.
type browserClientContract interface {
	GetBrowserProvider() string
	StartBrowser(context.Context, types.ApplicationBrowserID, types.BrowserOptions) error
	Navigate(context.Context, types.ApplicationBrowserID, string) error
	ScreenshotForLLM(context.Context, types.ApplicationBrowserID, string) (types.Screenshot, error)
	Click(context.Context, types.ApplicationBrowserID, int) error
	Type(context.Context, types.ApplicationBrowserID, types.FieldInput) error
	Scroll(context.Context, types.ApplicationBrowserID, string, float64) error
	UploadFile(context.Context, types.ApplicationBrowserID, int, string) error
	ScrapeRenderedPage(context.Context, types.ApplicationBrowserID) (string, error)
	DetectCaptcha(context.Context, types.ApplicationBrowserID) (types.Captcha, error)
	InjectCaptchaToken(context.Context, types.ApplicationBrowserID, string, string) (types.CaptchaResult, error)
	ClickCaptchaButton(context.Context, types.ApplicationBrowserID, string) (bool, error)
	ClickSubmit(context.Context, types.ApplicationBrowserID, int) (types.SubmissionAttempt, error)
	VerifySubmission(context.Context, types.ApplicationBrowserID, string) (types.SubmissionState, error)
	CloseBrowser(context.Context, types.ApplicationBrowserID) error
}

var _ browserClientContract = (*KernelBrowserClient)(nil)
