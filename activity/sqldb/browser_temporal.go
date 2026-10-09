package sqldb

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BeginBrowserReplayGenerationInput struct {
	ApplicationBrowserID string `json:"application_browser_id"`
	WorkflowID           string `json:"workflow_id"`
}

func (a *Activity) BeginBrowserReplayGeneration(ctx context.Context, input BeginBrowserReplayGenerationInput) (uint64, error) {
	applicationBrowserID, err := uuid.Parse(input.ApplicationBrowserID)
	if err != nil {
		return 0, fmt.Errorf("parse application browser ID for retry: %w", err)
	}
	generation, err := NewBrowserStore(a.db).BeginBrowserReplayGeneration(ctx, applicationBrowserID, input.WorkflowID)
	if err != nil {
		return 0, fmt.Errorf("begin browser replay generation: %w", err)
	}
	return generation, nil
}

type GetApplicationBrowserIDResult struct {
	Found bool   `json:"found"`
	ID    string `json:"id"`
}

type CreateApplicationBrowserIDInput struct {
	IdJobApplication uint   `json:"id_job_application"`
	ID               string `json:"id"`
}

type browserSessionStore struct {
	store *BrowserStore
}

func NewBrowserSessionStore(db *gorm.DB) (types.SessionStore, error) {
	if db == nil {
		return nil, fmt.Errorf("browser session store requires a database")
	}
	return &browserSessionStore{store: NewBrowserStore(db)}, nil
}

func (s *browserSessionStore) Get(ctx context.Context, id types.ApplicationBrowserID) (types.SessionRef, bool, error) {
	applicationBrowserID, err := parseApplicationBrowserID(id)
	if err != nil {
		return types.SessionRef{}, false, err
	}

	stored, found, err := s.store.GetBrowserSession(ctx, applicationBrowserID)
	if err != nil {
		return types.SessionRef{}, false, fmt.Errorf("get browser session: %w", err)
	}
	if !found {
		return types.SessionRef{}, false, nil
	}
	liveViewURL := ""
	if len(stored.LiveViewURLCiphertext) > 0 {
		liveViewURL, err = decryptBrowserSecret(stored.LiveViewURLCiphertext, applicationBrowserID)
		if err != nil {
			return types.SessionRef{}, false, fmt.Errorf("decrypt browser session Live View URL: %w", err)
		}
	}

	ref := types.SessionRef{
		Provider:             string(stored.Provider),
		ProviderSessionID:    stored.ProviderSessionID,
		LiveViewURL:          liveViewURL,
		Status:               string(stored.Status),
		ReplayGeneration:     stored.ReplayGeneration,
		ReplayPending:        stored.ReplayPending,
		BrowserProfileID:     stored.IdBrowserProfile,
		BrowserVaultID:       stored.IdBrowserVault,
		CheckpointMutationID: stored.CheckpointMutationID,
	}
	if !stored.CheckpointCreatedAt.IsZero() {
		ref.CheckpointCreatedAt = stored.CheckpointCreatedAt
	}
	if !stored.ExpiresAt.IsZero() {
		ref.ExpiresAt = stored.ExpiresAt
	}
	return ref, true, nil
}

func (s *browserSessionStore) Save(ctx context.Context, id types.ApplicationBrowserID, ref types.SessionRef) error {
	applicationBrowserID, err := parseApplicationBrowserID(id)
	if err != nil {
		return err
	}
	var liveViewURLCiphertext []byte
	if ref.LiveViewURL != "" {
		parsedURL, parseErr := url.Parse(ref.LiveViewURL)
		if parseErr != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil {
			return fmt.Errorf("browser Live View URL must be a valid HTTPS URL")
		}
		liveViewURLCiphertext, err = encryptBrowserSecret(ref.LiveViewURL, applicationBrowserID)
		if err != nil {
			return fmt.Errorf("encrypt browser session Live View URL: %w", err)
		}
	}

	stored := BrowserSessionRef{
		Provider:              BrowserProvider(ref.Provider),
		ProviderSessionID:     ref.ProviderSessionID,
		LiveViewURLCiphertext: liveViewURLCiphertext,
		Status:                BrowserSessionStatus(ref.Status),
		ReplayGeneration:      ref.ReplayGeneration,
		ReplayPending:         ref.ReplayPending,
		IdBrowserProfile:      ref.BrowserProfileID,
		IdBrowserVault:        ref.BrowserVaultID,
		CheckpointCreatedAt:   ref.CheckpointCreatedAt,
		CheckpointMutationID:  ref.CheckpointMutationID,
		ExpiresAt:             ref.ExpiresAt,
	}
	if err := s.store.SaveBrowserSession(ctx, applicationBrowserID, stored); err != nil {
		return fmt.Errorf("save browser session: %w", err)
	}
	return nil
}

func (s *browserSessionStore) ClearActive(ctx context.Context, id types.ApplicationBrowserID) error {
	applicationBrowserID, err := parseApplicationBrowserID(id)
	if err != nil {
		return err
	}
	if err := s.store.PromoteBrowserCheckpoint(ctx, applicationBrowserID); err != nil {
		return fmt.Errorf("commit closed browser checkpoint: %w", err)
	}
	if err := s.store.ClearBrowserSessionActive(ctx, applicationBrowserID); err != nil {
		return fmt.Errorf("clear active browser session: %w", err)
	}
	return nil
}

func (s *browserSessionStore) CompleteReplay(ctx context.Context, id types.ApplicationBrowserID, providerSessionID string) error {
	applicationBrowserID, err := parseApplicationBrowserID(id)
	if err != nil {
		return err
	}
	if providerSessionID == "" {
		return fmt.Errorf("provider browser session ID is required to complete replay")
	}
	if err := s.store.CompleteBrowserReplay(ctx, applicationBrowserID, providerSessionID); err != nil {
		return fmt.Errorf("complete browser replay: %w", err)
	}
	return nil
}

func (s *browserSessionStore) EnsureKernelProfileVault(ctx context.Context, id types.ApplicationBrowserID) (types.KernelProfileVault, error) {
	applicationBrowserID, err := parseApplicationBrowserID(id)
	if err != nil {
		return types.KernelProfileVault{}, err
	}
	profile, vault, err := s.store.EnsureKernelProfileVault(ctx, applicationBrowserID)
	if err != nil {
		return types.KernelProfileVault{}, fmt.Errorf("ensure Kernel profile and vault: %w", err)
	}
	return types.KernelProfileVault{
		ProfileID:         profile.IdBrowserProfile,
		ProfileProviderID: profile.ProviderProfileID,
		ProfileName:       profile.ProviderProfileName,
		VaultID:           vault.IdBrowserVault,
		VaultProviderID:   vault.ProviderVaultID,
		VaultName:         vault.ProviderVaultName,
	}, nil
}

func (s *browserSessionStore) SaveKernelProfileVault(ctx context.Context, id types.ApplicationBrowserID, resources types.KernelProfileVault) error {
	applicationBrowserID, err := parseApplicationBrowserID(id)
	if err != nil {
		return err
	}
	if err := s.store.SaveKernelProfileVault(ctx, applicationBrowserID, resources.ProfileID, resources.VaultID,
		resources.ProfileProviderID, resources.ProfileName, resources.VaultProviderID, resources.VaultName); err != nil {
		return fmt.Errorf("save Kernel profile and vault: %w", err)
	}
	return nil
}

func (s *browserSessionStore) AcquireKernelProfileLease(ctx context.Context, profileID uint, owner string, expiresAt time.Time) (bool, error) {
	return s.store.AcquireBrowserProfileLease(ctx, profileID, owner, expiresAt)
}

func (s *browserSessionStore) ReleaseKernelProfileLease(ctx context.Context, profileID uint, owner string) error {
	return s.store.ReleaseBrowserProfileLease(ctx, profileID, owner)
}

func parseApplicationBrowserID(id types.ApplicationBrowserID) (uuid.UUID, error) {
	parsed, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse application browser ID: %w", err)
	}
	return parsed, nil
}

func (a *Activity) GetApplicationBrowserID(ctx context.Context, idJobApplication uint) (GetApplicationBrowserIDResult, error) {
	if !a.browserProvider.Valid() {
		return GetApplicationBrowserIDResult{}, fmt.Errorf("get application browser ID: unsupported browser provider %q", a.browserProvider)
	}
	result, err := NewBrowserStore(a.db).GetApplicationBrowserID(ctx, idJobApplication, a.browserProvider)
	if err != nil {
		return GetApplicationBrowserIDResult{}, fmt.Errorf("get application browser ID: %w", err)
	}
	if !result.Found {
		return GetApplicationBrowserIDResult{Found: false}, nil
	}
	return GetApplicationBrowserIDResult{Found: true, ID: result.ID.String()}, nil
}

func (a *Activity) CreateApplicationBrowserID(ctx context.Context, input CreateApplicationBrowserIDInput) (string, error) {
	if !a.browserProvider.Valid() {
		return "", fmt.Errorf("create application browser ID: unsupported browser provider %q", a.browserProvider)
	}
	requestedID, err := uuid.Parse(input.ID)
	if err != nil {
		return "", fmt.Errorf("parse application browser ID: %w", err)
	}

	persistedID, err := NewBrowserStore(a.db).GetOrCreateApplicationBrowserID(
		ctx,
		input.IdJobApplication,
		a.browserProvider,
		requestedID,
	)
	if err != nil {
		return "", fmt.Errorf("create application browser ID: %w", err)
	}
	return persistedID.String(), nil
}

var _ types.SessionStore = (*browserSessionStore)(nil)
var _ types.KernelProfileVaultStore = (*browserSessionStore)(nil)
