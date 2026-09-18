# Migration of Browser Infrastructure from Go-rod to Kernel

## Why am I doing this?

Go-rod is a great library for automating browser actions. However, it has a few issues that are preventing us from using it in production:

1. It is not easy to use in a serverless environment.
2. We is really difficult to scale because of management complexity.
3. It is not exactly wonderfully documented.
4. Bot avoidance is very minimal. I'll have to implement a lot of it myself including proxying and user agents.
5. Chromium browsers are resource intensive and severly slows down my VPS.

## What is the plan?

We will need to migrate to Kernel. Kernel allows us to connect to managed browsers with a lot of bot avoidance and proxying capabilities out of the box. It is also relatively cheap and scales way better than our current solution.

## Implementation Details

### 1. Build a deeper browser abstraction layer

We will maintain the go-rod implementation but will build an abstraction layer (single interface) that allows us to use the same APIs for both go-rod and Kernel.

```go
// /browserfactory/browser.go
type Browser interface {
    Connect(ctx context.Context, url string) error
    Close() error
    Page(ctx context.Context) (Page, error)
    Screenshot(ctx context.Context, fileName string) ( error)
}


func NewBrowser(ctx context.Context, provider string, ...browserOptions) (Browser, error) {
    switch provider {
    case "kernel":
        return NewKernelBrowser(ctx, ...browserOptions)
    case "go-rod":
        return NewGoRodBrowser(ctx, ...browserOptions)
    default:
        return nil, fmt.Errorf("unknown browser provider: %s", provider)
    }
}
```

```go
// /common/dependencies.go

func MakeDependencies() (Dependencies, error) {
    // other dependencies...

    browserClient, err := browserfactory.NewBrowser(ctx, "kernel")
    if err != nil {
        return nil, err
    }

    return Dependencies{
        // other dependencies...
        BrowserClient: browserClient,
    }, nil
}
```

### 2. We will move from a one-browser architecture to a one-browser-per-application architecture

Instead of having a single browser instance that is shared across all the applications, we will create a new browser instance for each application. This will allow us to have a more isolated environment for each application, which will help us to improve the performance and stability of the application.

To manage critical resources like the VPS memory and remain within the kernel concurrency limits, we will use a pool of browsers. This will allow us to manage the number of browsers that are running concurrently and ensure that the resources are used efficiently.

#### 2.1 Create a BrowserPool workflow

This worflow will be responsible for creating and managing the browser pool. It will have a timeout of 1 year and will have to be incredible fault tolerant. We will start it on application startup and it will be responsible for triggering the job application workflows. It will recieve a signal from the API when a new job application is initiated. The job application will be in a `pending` state until the BrowserPool workflow is picks it up from the queue and starts the job application workflow.

```go
// iris-api /endpoints/jobapplication/endpoint.go

type Endpoint struct {
    // other fields...
    temporalClient client.Client
    browserPoolWorkflowId string
}

type BrowserPoolSignal struct {
    JobApplicationID uint
    ExternalID       string
    URL              string
    IDUser           uint
    IDResume         uint
    Action           string // "enqueue" | "cancel"
}

func (e *Endpoint) ApplyForJob(c *gin.Context) {
    var req ApplyJobRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // 1. Persist a pending application row so the UI can track state immediately.
    jobApp, err := e.jobApplicationService.CreatePending(req)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create job application"})
        return
    }

    // 2. TODO: Call the InitiateApplicationWorkflow workflow to extract the job description, company name, and job title.

    // 3. Enqueue the job in the browser scheduler. The browser pool owns browser availability,
    // so the API just notifies the pool that a new worker slot is needed.
    err = e.temporalClient.SignalWorkflow(
        c.Request.Context(),
        e.browserPoolWorkflowId,
        "",
        "EnqueueJobApplication",
        BrowserPoolSignal{
            JobApplicationID: jobApp.ID,
            ExternalID:       jobApp.ExternalID,
            URL:              req.URL,
            IDUser:           req.IDUser,
            IDResume:         req.IDResume,
            Action:           "enqueue",
        },
    )
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue browser session"})
        return
    }

    // 4. The API waits for the workflow result so the client gets a definitive status instead of
    // returning before the browser pool decides whether the application can start.
    var result workflow.JobApplicationResult
    if err := run.Get(c.Request.Context(), &result); err != nil {
        c.JSON(http.StatusAccepted, gin.H{
            "job_application_id": jobApp.ID,
            "status":            "pending",
            "error":            err.Error(),
        })
        return
    }

    c.JSON(http.StatusAccepted, gin.H{
        "job_application_id": jobApp.ID,
        "status":             result.Status,
        "message":            result.Message,
    })
}
```

```go
// iris-worker /workflow/browserpool/workflow.go

type BrowserPoolConfig struct {
    MaxConcurrentBrowsers int
    MaxIdleSeconds        int
    QueueTimeoutSeconds   int
}

type BrowserPoolQueueItem struct {
    JobApplicationID uint
    ExternalID       string
    URL              string
    IDUser           uint
    IDResume         uint
}

func BrowserPoolWorkflow(ctx workflow.Context, cfg BrowserPoolConfig) error {
    // The browser pool runs as a long-lived workflow on startup. It owns the lifecycle of
    // browser sessions and decides when a new job application can be scheduled.
    queue := make(chan BrowserPoolQueueItem, cfg.MaxConcurrentBrowsers*4)
    available := make(chan string, cfg.MaxConcurrentBrowsers)

    // TODO: bootstrap browser session pool with Kernel-managed browser instances.
    // Each browser is isolated per application so that we can reuse a single session while
    // keeping failures contained and avoiding cross-application memory leakage.

    for {
        select {
        case <-ctx.Done():
            return nil

        case sig := <-workflow.GetSignalChannel(ctx, "EnqueueJobApplication"):
            var item BrowserPoolQueueItem
            if err := sig.Get(&item); err != nil {
                return err
            }

            // Queue the new application until a browser is available or a timeout is reached.
            select {
            case queue <- item:
            case <-workflow.NewTimer(ctx, time.Duration(cfg.QueueTimeoutSeconds)*time.Second):
                return fmt.Errorf("browser pool queue timeout for job application %d", item.JobApplicationID)
            }

        case item := <-queue:
            // If a browser slot is free, immediately hand the item off to the job application workflow.
            select {
            case <-available:
                // actual scheduling logic moves here:
                // - claim browser session
                // - start workflow or update job status to 'processing'
            default:
                // if no slot is free, keep item in the queue and retry later
                // this is where we would wait for a browser to free up
            }
        }
    }
}
```

This keeps the API thin, the browser pool as the scheduler, and the heavy browser lifecycle contained in one long-lived workflow that can scale by concurrency limits instead of a single shared browser instance.
