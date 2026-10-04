# Engineering Report: Deploying Go-Rod Browser Automation on Modal

## Executive Summary

This report defines the architecture, configuration, and deployment strategy for running Go-based browser automation (via go-rod) on Modal's serverless micro-VM platform. By combining Modal's sub-second scaling infrastructure with container-level proxy configuration, this setup achieves true scale-to-zero billing efficiency. Utilizing the Modal Starter Plan, this architecture scales up to 100 concurrent VMs simultaneously while remaining completely within a recurring $30/month free tier allowance (covering ~150–250 monthly compute hours depending on resource allocation).

### 1. Architectural Overview

Modal requires a hybrid control plane where Python defines the container image infrastructure, while a compiled Go binary handles runtime execution. The infrastructure operates via a three-step cycle:

```
   [ Local Go App / Trigger ]
   │
   ▼ (Invokes SDK)
   [ Modal Control Plane ] ────► Spins up on-demand gVisor VM (under 1 second)
   │
   ▼ (Applies Global Proxy Env Vars)
   [ Ubuntu/Debian Container ]
   │── Runs compiled Go-Rod binary
   └── Spawns Headless Chromium
   └── Terminates & scales to $0 instantly
```

### 2. Infrastructure Setup (Infrastructure-as-Code)

This script acts as the one-time layout blueprint. It constructs a lightweight Debian container, installs the essential operating system libraries required to run a headless Chromium engine, and configures VM-wide network proxy variables.
Save this file as `deploy_env.py` and deploy it using the command: `modal deploy deploy_env.py`.

```python
# deploy_env.py
import modal

# 1. Define the Modal App Namespace
app = modal.App("go-rod-runner")

# 2. Define global proxy variables (Supports http, https, or socks5)
# Packaging this at the VM layer keeps credentials out of your application code
PROXY_URL = "http://your-proxy-provider.com"

# 3. Build the container image with Chromium & Go compilation dependencies
rod_image = (
    modal.Image.debian_slim()
    .apt_install(
        "golang",
        "chromium",
        "libnss3",
        "libatk-bridge2.0-0",
        "libx11-xcb1",
        "libxcomposite1",
        "libxdamage1",
        "libxrandr2",
        "libgbm1",
        "libasound2",
        "ca-certificates"
    )
)

# 4. Register the execution environment block
@app.function(
    image=rod_image,
    cpu=1.0,      # Allocates 1 vCPU Core
    memory=2048,  # Allocates 2 GiB RAM (Perfect for single-tab browser flows)
    timeout=600,  # 10-minute safety timeout threshold per VM instance
    env={
        "HTTP_PROXY": PROXY_URL,
        "HTTPS_PROXY": PROXY_URL,
        "http_proxy": PROXY_URL,
        "https_proxy": PROXY_URL,
        "NO_PROXY": "localhost,127.0.0.1,modal.local"
    }
)
def run_job():
    """Dummy hook used by Modal to pin and cache the container configuration."""
    pass

```

### 3. Go-Rod Implementation Inside the VM

Because the container handles proxy routing at the Linux networking namespace layer, your Go code remains exceptionally clean. However, Chromium requires specific flags to prevent memory crashes inside an isolated root-level serverless container.
Save this file as main.go inside your remote execution directory.

```go
go
package main

import (
"fmt"
"os"

    "://github.com"
    "://github.com/lib/launcher"

)

func main() {
// 1. Initialize Chromium using Go-Rod's launcher utility
l := launcher.New().
Headless(true).
// Mandatory for root-user execution inside Linux container environments
NoSandbox(true).
// Overrides Chromium's default /dev/shm small allocation block to prevent crashes
Set("disable-dev-shm-usage", "").
Set("disable-gpu", "")

    // 2. Explicitly bind Chromium to the VM-level proxy environment variable
    if os.Getenv("HTTP_PROXY") != "" {
    	l.Set("proxy-server", os.Getenv("HTTP_PROXY"))
    }

    // 3. Launch the browser process and fetch the DevTools Protocol (CDP) WebSocket URL
    controlURL := l.MustLaunch()

    // 4. Attach Go-Rod to the running instance
    browser := rod.New().ControlURL(controlURL).MustConnect()
    // Ensure the browser process completely terminates when the main function closes
    defer browser.MustClose()

    // 5. Execute your browser automation script
    page := browser.MustPage("https://httpbin.org")
    page.MustWaitLoad()

    // Print out the page text to verify the proxy routing is successful
    bodyText := page.MustElement("body").MustText()
    fmt.Println("Resulting Content:\n", bodyText)

}
```

### 4. Orchestration & Concurrency Control

To scale your Go-Rod tasks up to 100 concurrent virtual machines simultaneously, use the libmodal-go client package within your local orchestration app.

```go
package main

import (
"context"
"fmt"
"log"

    "://github.com"

)

func main() {
ctx := context.Background()
client, err := modal.NewClient(ctx)
if err != nil {
log.Fatalf("Failed to initialize Modal Client: %v", err)
}

    // Spin up an on-demand container matching your blueprint setup
    sandbox, err := client.Sandbox.Create(ctx, modal.SandboxParams{
    	AppName: "go-rod-runner",
    	Cmd:     []string{"go", "run", "main.go"},
    })
    if err != nil {
    	log.Fatalf("Failed to spin up Sandbox VM: %v", err)
    }

    // Stream stdout logs from the VM browser run back to your terminal window
    stdout, _ := sandbox.Stdout(ctx)
    fmt.Println(stdout)

}
```

### 5. Capacity & Billing Analytics

- Scale-To-Zero Performance: The millisecond main.go terminates execution, Modal shuts down the gVisor instance. You pay zero baseline or idle retention fees.
- Financial Calculations (Starter Plan):
  - Allocation Costs: 1 vCPU + 2 GiB RAM = ~$0.1903 per active hour.
  - Free Allocation Capacity: The $30 recurring monthly credit allows for ~157 hours of continuous browser orchestration entirely free of charge every month.
  - Task Volume Throughput: If a single browser navigation run takes roughly 90 seconds, you can execute ~6,280 automated browser jobs per month completely inside the free tier limit.
