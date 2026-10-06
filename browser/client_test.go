package browser

import (
	"testing"

	"github.com/SomtoJF/iris-worker/browser/types"
)

func TestNewBrowserClientRod(t *testing.T) {
	client, err := NewBrowserClient(ClientTypeRod, Config{})
	if err != nil {
		t.Fatalf("NewBrowserClient: %v", err)
	}
	if got := client.GetBrowserProvider(); got != types.BrowserProviderRod {
		t.Fatalf("provider = %q, want %q", got, types.BrowserProviderRod)
	}
}

func TestNewBrowserClientRejectsUnknownProvider(t *testing.T) {
	if _, err := NewBrowserClient(ClientType("other"), Config{}); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}
