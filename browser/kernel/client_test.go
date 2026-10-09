package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/SomtoJF/iris-worker/initializers/fs"
	kernelsdk "github.com/kernel/kernel-go-sdk"
)

type testKernelAPI struct {
	newParams    kernelsdk.BrowserNewParams
	newCalls     int
	getCalls     int
	deleted      []string
	executed     []string
	deleteErr    error
	failExec     int
	events       *[]string
	profile      *kernelsdk.Profile
	vault        *kernelsdk.Vault
	profileErr   error
	vaultErr     error
	profileGets  []string
	profileNews  []string
	vaultUpserts []string
}

func (a *testKernelAPI) newBrowser(_ context.Context, params kernelsdk.BrowserNewParams) (*kernelsdk.BrowserNewResponse, error) {
	a.newCalls++
	a.newParams = params
	a.addEvent("new")
	return &kernelsdk.BrowserNewResponse{SessionID: "kernel-session-1", BrowserLiveViewURL: "https://live.example/token"}, nil
}

func (a *testKernelAPI) getBrowser(_ context.Context, id string) (*kernelsdk.BrowserGetResponse, error) {
	a.getCalls++
	a.addEvent("get:" + id)
	return &kernelsdk.BrowserGetResponse{SessionID: id}, nil
}

func (a *testKernelAPI) deleteBrowser(_ context.Context, id string) error {
	a.deleted = append(a.deleted, id)
	a.addEvent("delete:" + id)
	return a.deleteErr
}

func (a *testKernelAPI) execute(_ context.Context, id, code string) (*kernelsdk.BrowserPlaywrightExecuteResponse, error) {
	a.executed = append(a.executed, id+":"+code)
	a.addEvent("execute")
	if a.failExec > 0 {
		a.failExec--
		return &kernelsdk.BrowserPlaywrightExecuteResponse{Success: false, Error: "private remote detail"}, nil
	}
	return &kernelsdk.BrowserPlaywrightExecuteResponse{Success: true, Result: true}, nil
}

func (a *testKernelAPI) newProfile(_ context.Context, name string) (*kernelsdk.Profile, error) {
	a.profileNews = append(a.profileNews, name)
	if a.profileErr != nil {
		return nil, a.profileErr
	}
	if a.profile == nil {
		a.profile = &kernelsdk.Profile{ID: "kernel-profile-1", Name: name}
	}
	return a.profile, nil
}

func (a *testKernelAPI) getProfile(_ context.Context, idOrName string) (*kernelsdk.Profile, error) {
	a.profileGets = append(a.profileGets, idOrName)
	if a.profileErr != nil {
		return nil, a.profileErr
	}
	if a.profile != nil && (a.profile.ID == idOrName || a.profile.Name == idOrName) {
		return a.profile, nil
	}
	return nil, &kernelsdk.Error{StatusCode: http.StatusNotFound}
}

func (a *testKernelAPI) upsertVault(_ context.Context, name string) (*kernelsdk.Vault, error) {
	a.vaultUpserts = append(a.vaultUpserts, name)
	if a.vaultErr != nil {
		return nil, a.vaultErr
	}
	if a.vault == nil {
		a.vault = &kernelsdk.Vault{ID: "kernel-vault-1", Name: name}
	}
	return a.vault, nil
}

func (a *testKernelAPI) addEvent(event string) {
	if a.events != nil {
		*a.events = append(*a.events, event)
	}
}

type testSessionStore struct {
	ref      types.SessionRef
	found    bool
	saveErr  error
	clearErr error
	events   *[]string
	saves    int
	clears   int
}

type testProfileVaultStore struct {
	*testSessionStore
	resources      types.KernelProfileVault
	ensureErr      error
	saveErr        error
	leaseErr       error
	releaseErr     error
	leaseAcquired  bool
	leasesAcquired int
	leasesReleased int
}

func (s *testProfileVaultStore) EnsureKernelProfileVault(context.Context, types.ApplicationBrowserID) (types.KernelProfileVault, error) {
	if s.ensureErr != nil {
		return types.KernelProfileVault{}, s.ensureErr
	}
	return s.resources, nil
}

func (s *testProfileVaultStore) SaveKernelProfileVault(_ context.Context, _ types.ApplicationBrowserID, resources types.KernelProfileVault) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.resources = resources
	return nil
}

func (s *testProfileVaultStore) AcquireKernelProfileLease(context.Context, uint, string, time.Time) (bool, error) {
	s.leasesAcquired++
	if s.leaseErr != nil {
		return false, s.leaseErr
	}
	if !s.leaseAcquired && s.leasesAcquired == 1 {
		s.leaseAcquired = true
	}
	return s.leaseAcquired, nil
}

func (s *testProfileVaultStore) ReleaseKernelProfileLease(context.Context, uint, string) error {
	s.leasesReleased++
	s.leaseAcquired = false
	return s.releaseErr
}

func (s *testSessionStore) Get(context.Context, types.ApplicationBrowserID) (types.SessionRef, bool, error) {
	return s.ref, s.found, nil
}

func (s *testSessionStore) Save(_ context.Context, _ types.ApplicationBrowserID, ref types.SessionRef) error {
	s.saves++
	if s.events != nil {
		*s.events = append(*s.events, "save")
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	s.ref, s.found = ref, true
	return nil
}

func (s *testSessionStore) ClearActive(context.Context, types.ApplicationBrowserID) error {
	s.clears++
	if s.events != nil {
		*s.events = append(*s.events, "clear")
	}
	if s.clearErr != nil {
		return s.clearErr
	}
	s.ref, s.found = types.SessionRef{}, false
	return nil
}

func (s *testSessionStore) CompleteReplay(context.Context, types.ApplicationBrowserID, string) error {
	s.ref.ReplayPending = false
	return nil
}

func TestNewKernelBrowserClientRequiresAPIKey(t *testing.T) {
	filesystem := fs.NewTemporaryFilesystem()
	t.Cleanup(filesystem.Cleanup)
	_, err := NewKernelBrowserClient(Config{SessionStore: &testSessionStore{}, TempFS: filesystem})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing API key error, got %v", err)
	}
}

func TestStartBrowserCreatesHeadfulStealthSessionAndPersistsBeforeNavigation(t *testing.T) {
	events := []string{}
	store := &testSessionStore{events: &events}
	api := &testKernelAPI{events: &events}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: fs.NewTemporaryFilesystem()}, api)
	t.Cleanup(client.tempFS.Cleanup)

	if err := client.StartBrowser(context.Background(), "application-1", types.BrowserOptions{StartingURL: "https://example.test"}); err != nil {
		t.Fatalf("StartBrowser returned error: %v", err)
	}
	if api.newCalls != 1 {
		t.Fatalf("expected one remote browser creation, got %d", api.newCalls)
	}
	if !store.found || store.ref.ProviderSessionID != "kernel-session-1" || store.ref.LiveViewURL != "https://live.example/token" {
		t.Fatalf("unexpected stored session ref: %+v", store.ref)
	}
	if len(events) < 3 || events[0] != "new" || events[1] != "save" || events[2] != "execute" {
		t.Fatalf("expected create, persist, navigate order; got %v", events)
	}
	if strings.Contains(api.executed[0], "const page") || !strings.Contains(api.executed[0], "if (!page)") {
		t.Fatalf("initial navigation must use Kernel's injected page variable without redeclaring it: %s", api.executed[0])
	}
	encoded, err := json.Marshal(api.newParams)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(encoded, &params); err != nil {
		t.Fatal(err)
	}
	if params["headless"] != false || params["stealth"] != true || params["timeout_seconds"] != float64(defaultTimeoutSeconds) {
		t.Fatalf("unexpected Kernel creation parameters: %s", encoded)
	}
	if params["start_url"] != "https://example.test" {
		t.Fatalf("expected initial URL in browser request, got %s", encoded)
	}
	if _, ok := params["proxy"]; ok {
		t.Fatalf("Free-plan creation must not configure a proxy: %s", encoded)
	}
	if _, ok := params["proxy_id"]; ok {
		t.Fatalf("Free-plan creation must not select a proxy: %s", encoded)
	}
}

func TestStartBrowserAttachesPersistentKernelProfileAndVault(t *testing.T) {
	filesystem := fs.NewTemporaryFilesystem()
	t.Cleanup(filesystem.Cleanup)
	store := &testProfileVaultStore{
		testSessionStore: &testSessionStore{},
		resources: types.KernelProfileVault{
			ProfileID:   7,
			ProfileName: "iris-user-12-profile",
			VaultID:     9,
			VaultName:   "iris-user-12-vault",
		},
	}
	api := &testKernelAPI{}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: filesystem}, api)

	if err := client.StartBrowser(context.Background(), "application-1", types.BrowserOptions{}); err != nil {
		t.Fatalf("StartBrowser returned error: %v", err)
	}
	if len(api.profileNews) != 1 || api.profileNews[0] != "iris-user-12-profile" {
		t.Fatalf("expected named profile creation, got %v", api.profileNews)
	}
	if len(api.vaultUpserts) != 1 || api.vaultUpserts[0] != "iris-user-12-vault" {
		t.Fatalf("expected named vault upsert, got %v", api.vaultUpserts)
	}
	if store.resources.ProfileProviderID != "kernel-profile-1" || store.resources.VaultProviderID != "kernel-vault-1" {
		t.Fatalf("provider resource IDs were not persisted: %+v", store.resources)
	}
	if store.ref.BrowserProfileID == nil || *store.ref.BrowserProfileID != 7 || store.ref.BrowserVaultID == nil || *store.ref.BrowserVaultID != 9 {
		t.Fatalf("session was not linked to persistent resources: %+v", store.ref)
	}
	if store.leasesAcquired != 1 || store.leasesReleased != 0 {
		t.Fatalf("profile lease should remain held for the active session: acquired=%d released=%d", store.leasesAcquired, store.leasesReleased)
	}
	if err := client.Navigate(context.Background(), "application-1", "https://example.test"); err != nil {
		t.Fatalf("navigate did not renew profile lease: %v", err)
	}
	if store.leasesAcquired != 2 {
		t.Fatalf("expected active browser operations to renew the lease, got %d acquisitions", store.leasesAcquired)
	}
	encoded, err := json.Marshal(api.newParams)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(encoded, &params); err != nil {
		t.Fatal(err)
	}
	profile, ok := params["profile"].(map[string]any)
	if !ok || profile["id"] != "kernel-profile-1" || profile["save_changes"] != true {
		t.Fatalf("persistent profile was not attached with save_changes: %s", encoded)
	}
	vaults, ok := params["vaults"].([]any)
	if !ok || len(vaults) != 1 || vaults[0].(map[string]any)["id"] != "kernel-vault-1" {
		t.Fatalf("persistent vault was not attached: %s", encoded)
	}
}

func TestStartBrowserReleasesProfileLeaseWhenKernelCreationFails(t *testing.T) {
	filesystem := fs.NewTemporaryFilesystem()
	t.Cleanup(filesystem.Cleanup)
	store := &testProfileVaultStore{
		testSessionStore: &testSessionStore{},
		resources:        types.KernelProfileVault{ProfileID: 7, ProfileName: "profile", VaultID: 9, VaultName: "vault"},
	}
	api := &testKernelAPI{profileErr: errors.New("profile lookup unavailable")}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: filesystem}, api)

	if err := client.StartBrowser(context.Background(), "application-1", types.BrowserOptions{}); err == nil {
		t.Fatal("expected profile lookup failure")
	}
	if store.leasesAcquired != 1 || store.leasesReleased != 1 {
		t.Fatalf("profile lease was not released after startup failure: acquired=%d released=%d", store.leasesAcquired, store.leasesReleased)
	}
	if api.newCalls != 0 {
		t.Fatal("browser was created after profile setup failed")
	}
}

func TestStartBrowserReusesPersistedKernelProfileAndVault(t *testing.T) {
	filesystem := fs.NewTemporaryFilesystem()
	t.Cleanup(filesystem.Cleanup)
	store := &testProfileVaultStore{
		testSessionStore: &testSessionStore{},
		resources: types.KernelProfileVault{
			ProfileID: 7, ProfileProviderID: "saved-profile-id", ProfileName: "saved-profile",
			VaultID: 9, VaultProviderID: "saved-vault-id", VaultName: "saved-vault",
		},
	}
	api := &testKernelAPI{
		profile: &kernelsdk.Profile{ID: "saved-profile-id", Name: "saved-profile"},
		vault:   &kernelsdk.Vault{ID: "saved-vault-id", Name: "saved-vault"},
	}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: filesystem}, api)

	if err := client.StartBrowser(context.Background(), "application-1", types.BrowserOptions{}); err != nil {
		t.Fatalf("StartBrowser returned error: %v", err)
	}
	if len(api.profileNews) != 0 || len(api.profileGets) != 1 || api.profileGets[0] != "saved-profile-id" {
		t.Fatalf("saved profile was not reused: gets=%v creates=%v", api.profileGets, api.profileNews)
	}
	if store.resources.ProfileProviderID != "saved-profile-id" || store.resources.VaultProviderID != "saved-vault-id" {
		t.Fatalf("persisted resource identifiers changed: %+v", store.resources)
	}
}

func TestStartBrowserRetryReusesPersistedSession(t *testing.T) {
	store := &testSessionStore{}
	api := &testKernelAPI{failExec: 1}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: fs.NewTemporaryFilesystem()}, api)
	t.Cleanup(client.tempFS.Cleanup)
	id := types.ApplicationBrowserID("application-1")
	opts := types.BrowserOptions{StartingURL: "https://example.test"}

	if err := client.StartBrowser(context.Background(), id, opts); err == nil {
		t.Fatal("expected first navigation to fail")
	}
	if !store.found {
		t.Fatal("session must be persisted before navigation failure")
	}
	if err := client.StartBrowser(context.Background(), id, opts); err != nil {
		t.Fatalf("retry returned error: %v", err)
	}
	if api.newCalls != 1 || api.getCalls != 1 || store.saves != 1 {
		t.Fatalf("retry created or persisted another session: creates=%d reconnects=%d saves=%d", api.newCalls, api.getCalls, store.saves)
	}
}

func TestCloseBrowserDeletesRemoteSessionBeforeClearingStore(t *testing.T) {
	events := []string{}
	store := &testSessionStore{
		found:  true,
		ref:    types.SessionRef{Provider: types.BrowserProviderKernel, ProviderSessionID: "kernel-session-1", LiveViewURL: "https://live.example/token", Status: "active"},
		events: &events,
	}
	api := &testKernelAPI{events: &events}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: fs.NewTemporaryFilesystem()}, api)
	t.Cleanup(client.tempFS.Cleanup)

	if err := client.CloseBrowser(context.Background(), "application-1"); err != nil {
		t.Fatalf("CloseBrowser returned error: %v", err)
	}
	if len(api.deleted) != 1 || api.deleted[0] != "kernel-session-1" {
		t.Fatalf("expected explicit remote delete, got %v", api.deleted)
	}
	if len(events) != 2 || events[0] != "delete:kernel-session-1" || events[1] != "clear" {
		t.Fatalf("expected remote delete before session clear, got %v", events)
	}
	if store.found {
		t.Fatal("active session metadata was not cleared")
	}
}

func TestCloseBrowserReleasesPersistentProfileLease(t *testing.T) {
	filesystem := fs.NewTemporaryFilesystem()
	t.Cleanup(filesystem.Cleanup)
	profileID := uint(7)
	store := &testProfileVaultStore{
		testSessionStore: &testSessionStore{
			found: true,
			ref: types.SessionRef{
				Provider: types.BrowserProviderKernel, ProviderSessionID: "kernel-session-1",
				Status: "active", BrowserProfileID: &profileID,
			},
		},
	}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: filesystem}, &testKernelAPI{})

	if err := client.CloseBrowser(context.Background(), "application-1"); err != nil {
		t.Fatalf("CloseBrowser returned error: %v", err)
	}
	if store.leasesReleased != 1 {
		t.Fatalf("expected profile lease release, got %d", store.leasesReleased)
	}
}

func TestCloseBrowserKeepsSessionWhenRemoteDeleteFailsAndCanRetry(t *testing.T) {
	store := &testSessionStore{found: true, ref: types.SessionRef{Provider: types.BrowserProviderKernel, ProviderSessionID: "kernel-session-1", Status: "active"}}
	api := &testKernelAPI{deleteErr: errors.New("remote failure")}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: fs.NewTemporaryFilesystem()}, api)
	t.Cleanup(client.tempFS.Cleanup)

	if err := client.CloseBrowser(context.Background(), "application-1"); err == nil {
		t.Fatal("expected delete error")
	}
	if !store.found || store.clears != 0 {
		t.Fatal("session metadata must remain until remote delete succeeds")
	}
	api.deleteErr = nil
	if err := client.CloseBrowser(context.Background(), "application-1"); err != nil {
		t.Fatalf("delete retry returned error: %v", err)
	}
	if store.found || store.clears != 1 {
		t.Fatal("session metadata was not cleared after successful retry")
	}
}

func TestSDKErrorsDoNotExposeSessionIdentifiers(t *testing.T) {
	apiErr := &kernelsdk.Error{StatusCode: http.StatusInternalServerError}
	err := safeSDKError("delete Kernel browser session", apiErr)
	if strings.Contains(err.Error(), "kernel-session-secret") {
		t.Fatalf("sanitized error exposed provider ID: %v", err)
	}
	if !errors.Is(err, apiErr) {
		t.Fatal("sanitizing error should preserve its cause")
	}
}

func TestCloseBrowserTreatsAlreadyDeletedRemoteSessionAsClosed(t *testing.T) {
	store := &testSessionStore{found: true, ref: types.SessionRef{Provider: types.BrowserProviderKernel, ProviderSessionID: "kernel-session-1", Status: "active"}}
	api := &testKernelAPI{deleteErr: &kernelsdk.Error{StatusCode: http.StatusNotFound}}
	client := newKernelBrowserClient(Config{SessionStore: store, TempFS: fs.NewTemporaryFilesystem()}, api)
	t.Cleanup(client.tempFS.Cleanup)

	if err := client.CloseBrowser(context.Background(), "application-1"); err != nil {
		t.Fatalf("idempotent close returned error: %v", err)
	}
	if store.found || store.clears != 1 {
		t.Fatal("active metadata was not cleared after remote session was already deleted")
	}
}
