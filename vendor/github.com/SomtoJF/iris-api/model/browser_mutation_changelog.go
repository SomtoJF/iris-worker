package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BrowserMutationStatus string

const (
	BrowserMutationPending           BrowserMutationStatus = "pending"
	BrowserMutationApplied           BrowserMutationStatus = "applied"
	BrowserMutationFailed            BrowserMutationStatus = "failed"
	BrowserMutationReconcileRequired BrowserMutationStatus = "reconcile_required"
)

type BrowserMutationOutcome string

const (
	BrowserMutationOutcomeApplied   BrowserMutationOutcome = "applied"
	BrowserMutationOutcomeFailed    BrowserMutationOutcome = "failed"
	BrowserMutationOutcomeUncertain BrowserMutationOutcome = "uncertain"
)

type BrowserMutationTarget struct {
	Role     string `json:"role,omitempty"`
	Name     string `json:"name,omitempty"`
	Label    string `json:"label,omitempty"`
	Selector string `json:"selector,omitempty"`
	Submit   bool   `json:"submit,omitempty"`
}

type BrowserMutationContext struct {
	URL            string                 `json:"url,omitempty"`
	Target         *BrowserMutationTarget `json:"target,omitempty"`
	ElementIndex   *int                   `json:"element_index,omitempty"`
	FileInputIndex *int                   `json:"file_input_index,omitempty"`
	ReplaySafe     bool                   `json:"replay_safe"`
}

type BrowserMutationChangelog struct {
	IdBrowserMutationChangelog uint                   `gorm:"primaryKey;autoIncrement;column:id_browser_mutation_changelog"`
	IdExternal                 uuid.UUID              `gorm:"unique;type:uuid;default:gen_random_uuid()"`
	BrowserSessionID           uint                   `gorm:"column:id_browser_session;not null;index:idx_browser_mutation_replay,priority:1;uniqueIndex:idx_browser_mutation_generation_key"`
	Session                    BrowserSession         `gorm:"foreignKey:BrowserSessionID;references:IdBrowserSession;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Provider                   BrowserProvider        `gorm:"type:varchar(16);not null;index;check:chk_browser_mutation_provider,provider IN ('rod','kernel')"`
	ReplayGeneration           uint64                 `gorm:"not null;index:idx_browser_mutation_replay,priority:2;uniqueIndex:idx_browser_mutation_generation_key"`
	Operation                  string                 `gorm:"type:text;not null"`
	ArgumentsCiphertext        []byte                 `gorm:"column:arguments_ciphertext;type:bytea;not null"`
	Context                    BrowserMutationContext `gorm:"column:context;type:jsonb;not null"`
	IdempotencyKey             string                 `gorm:"type:text;not null;uniqueIndex:idx_browser_mutation_generation_key"`
	Status                     BrowserMutationStatus  `gorm:"type:varchar(32);not null;default:'pending';index:idx_browser_mutation_replay,priority:3;check:chk_browser_mutation_status,status IN ('pending','applied','failed','reconcile_required')"`
	Result                     *BrowserMutationResult `gorm:"column:result;type:jsonb"`
	CreatedAt                  time.Time              `gorm:"not null;default:CURRENT_TIMESTAMP;index:idx_browser_mutation_order;index:idx_browser_mutation_replay,priority:4"`
	UpdatedAt                  time.Time              `gorm:"not null;default:CURRENT_TIMESTAMP;autoUpdateTime"`
	CompletedAt                *time.Time             `gorm:"index"`
}

func (s BrowserMutationStatus) Valid() bool {
	switch s {
	case BrowserMutationPending, BrowserMutationApplied, BrowserMutationFailed, BrowserMutationReconcileRequired:
		return true
	default:
		return false
	}
}

func (c *BrowserMutationContext) Scan(value any) error {
	if value == nil {
		*c = BrowserMutationContext{}
		return nil
	}
	return scanBrowserMutationJSON(value, c)
}

func (c BrowserMutationContext) Value() (driver.Value, error) {
	return browserMutationJSONValue(c)
}

func (o BrowserMutationOutcome) Valid() bool {
	return o == BrowserMutationOutcomeApplied || o == BrowserMutationOutcomeFailed || o == BrowserMutationOutcomeUncertain
}

type BrowserMutationResult struct {
	Outcome         BrowserMutationOutcome `json:"outcome"`
	ReplaySafe      bool                   `json:"replay_safe"`
	ReplaySessionID string                 `json:"replay_session_id,omitempty"`
}

func (r BrowserMutationResult) Validate() error {
	if !r.Outcome.Valid() {
		return fmt.Errorf("browser mutation result outcome is invalid")
	}
	return nil
}

func (r *BrowserMutationResult) Scan(value any) error {
	if value == nil {
		*r = BrowserMutationResult{}
		return nil
	}
	return scanBrowserMutationJSON(value, r)
}

func (r BrowserMutationResult) Value() (driver.Value, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return browserMutationJSONValue(r)
}

func scanBrowserMutationJSON(value any, destination any) error {
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("browser mutation JSON scan: unsupported type %T", value)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("decode browser mutation JSON: %w", err)
	}
	return nil
}

func browserMutationJSONValue(value any) (driver.Value, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode browser mutation JSON: %w", err)
	}
	return string(data), nil
}

func (BrowserMutationChangelog) TableName() string { return "browser_mutation_changelog" }

func (m BrowserMutationChangelog) Validate() error {
	if m.BrowserSessionID == 0 || m.ReplayGeneration == 0 || m.Operation == "" || m.IdempotencyKey == "" {
		return fmt.Errorf("browser mutation session, generation, operation, and idempotency key are required")
	}
	if !m.Provider.Valid() {
		return fmt.Errorf("browser mutation provider is invalid")
	}
	if !m.Status.Valid() {
		return fmt.Errorf("browser mutation status is invalid")
	}
	if len(m.ArgumentsCiphertext) == 0 {
		return fmt.Errorf("browser mutation arguments must be supplied as ciphertext")
	}
	if m.Result != nil {
		if err := m.Result.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (m *BrowserMutationChangelog) BeforeCreate(*gorm.DB) error { return m.Validate() }
