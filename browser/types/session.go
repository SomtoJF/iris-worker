package types

import (
	"context"
	"time"
)

type SessionRef struct {
	Provider             string    `json:"provider"`
	ProviderSessionID    string    `json:"provider_session_id"`
	LiveViewURL          string    `json:"live_view_url,omitempty"`
	Status               string    `json:"status"`
	ReplayGeneration     uint64    `json:"replay_generation"`
	ReplayPending        bool      `json:"replay_pending"`
	BrowserProfileID     *uint     `json:"browser_profile_id,omitempty"`
	BrowserVaultID       *uint     `json:"browser_vault_id,omitempty"`
	CheckpointCreatedAt  time.Time `json:"checkpoint_created_at,omitempty"`
	CheckpointMutationID uint64    `json:"checkpoint_mutation_id,omitempty"`
	ExpiresAt            time.Time `json:"expires_at,omitempty"`
}

type SessionStore interface {
	Get(ctx context.Context, id ApplicationBrowserID) (SessionRef, bool, error)
	Save(ctx context.Context, id ApplicationBrowserID, ref SessionRef) error
	ClearActive(ctx context.Context, id ApplicationBrowserID) error
	CompleteReplay(ctx context.Context, id ApplicationBrowserID, providerSessionID string) error
}

type KernelProfileVault struct {
	ProfileID         uint
	ProfileProviderID string
	ProfileName       string
	VaultID           uint
	VaultProviderID   string
	VaultName         string
}

type KernelProfileVaultStore interface {
	EnsureKernelProfileVault(ctx context.Context, id ApplicationBrowserID) (KernelProfileVault, error)
	SaveKernelProfileVault(ctx context.Context, id ApplicationBrowserID, resources KernelProfileVault) error
	AcquireKernelProfileLease(ctx context.Context, profileID uint, owner string, expiresAt time.Time) (bool, error)
	ReleaseKernelProfileLease(ctx context.Context, profileID uint, owner string) error
}
