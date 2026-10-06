package browser

import (
	"context"
	"fmt"

	kernelprovider "github.com/SomtoJF/iris-worker/browser/kernel"
	rodprovider "github.com/SomtoJF/iris-worker/browser/rod"
	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/SomtoJF/iris-worker/initializers/fs"
)

type ClientType string

const (
	ClientTypeRod    ClientType = types.BrowserProviderRod
	ClientTypeKernel ClientType = types.BrowserProviderKernel
)

const (
	BrowserProviderRod    = types.BrowserProviderRod
	BrowserProviderKernel = types.BrowserProviderKernel
)

type Config struct {
	SessionStore types.SessionStore
	TempFS       *fs.TemporaryFileSystem
	KernelAPIKey string
	WorkerID     string
}

type BrowserClient interface {
	GetBrowserProvider() string
	StartBrowser(ctx context.Context, id types.ApplicationBrowserID, opts types.BrowserOptions) error
	Navigate(ctx context.Context, id types.ApplicationBrowserID, url string) error
	ScreenshotForLLM(ctx context.Context, id types.ApplicationBrowserID, fileName string) (types.Screenshot, error)
	Click(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) error
	Type(ctx context.Context, id types.ApplicationBrowserID, field types.FieldInput) error
	Scroll(ctx context.Context, id types.ApplicationBrowserID, direction string, ratio float64) error
	UploadFile(ctx context.Context, id types.ApplicationBrowserID, fileInputIndex int, filePath string) error
	ScrapeRenderedPage(ctx context.Context, id types.ApplicationBrowserID) (string, error)
	DetectCaptcha(ctx context.Context, id types.ApplicationBrowserID) (types.Captcha, error)
	InjectCaptchaToken(ctx context.Context, id types.ApplicationBrowserID, captchaType, token string) (types.CaptchaResult, error)
	ClickCaptchaButton(ctx context.Context, id types.ApplicationBrowserID, selector string) (bool, error)
	ClickSubmit(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) (types.SubmissionAttempt, error)
	VerifySubmission(ctx context.Context, id types.ApplicationBrowserID, beforeURL string) (types.SubmissionState, error)
	CloseBrowser(ctx context.Context, id types.ApplicationBrowserID) error
}

func NewBrowserClient(clientType ClientType, cfg Config) (BrowserClient, error) {
	switch clientType {
	case ClientTypeRod:
		return rodprovider.NewRodBrowserClient(rodprovider.Config{
			SessionStore: cfg.SessionStore,
			TempFS:       cfg.TempFS,
			WorkerID:     cfg.WorkerID,
		})
	case ClientTypeKernel:
		return kernelprovider.NewKernelBrowserClient(kernelprovider.Config{
			SessionStore: cfg.SessionStore,
			TempFS:       cfg.TempFS,
			APIKey:       cfg.KernelAPIKey,
		})
	default:
		return nil, fmt.Errorf("unsupported browser provider %q", clientType)
	}
}
