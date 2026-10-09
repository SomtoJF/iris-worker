package sqldb

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm/schema"
)

func TestBrowserModelValidation(t *testing.T) {
	profile := BrowserProfile{UserId: 1, Provider: BrowserProviderRod}
	if err := profile.Validate(); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
	profile.Provider = "unsupported"
	if err := profile.Validate(); err == nil {
		t.Fatal("invalid profile provider accepted")
	}

	vault := BrowserVault{UserId: 1, Provider: BrowserProviderKernel}
	if err := vault.Validate(); err != nil {
		t.Fatalf("valid vault rejected: %v", err)
	}
	vault.Provider = "unsupported"
	if err := vault.Validate(); err == nil {
		t.Fatal("invalid vault provider accepted")
	}

	connection := BrowserAuthConnection{BrowserProfileID: 1, Provider: BrowserProviderKernel, Domain: "example.com", Status: BrowserAuthConnectionNeedsAuth}
	if err := connection.Validate(); err != nil {
		t.Fatalf("valid auth connection rejected: %v", err)
	}
	connection.Status = "unknown"
	if err := connection.Validate(); err == nil {
		t.Fatal("invalid auth status accepted")
	}

	session := BrowserSession{JobApplicationID: 1, ApplicationBrowserID: uuid.New(), Provider: BrowserProviderRod, Status: BrowserSessionActive, ReplayGeneration: 1}
	if err := session.Validate(); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	session.ReplayGeneration = 0
	if err := session.Validate(); err == nil {
		t.Fatal("zero replay generation accepted")
	}
	session.ReplayGeneration = 1
	cursor := time.Now()
	session.CheckpointCreatedAt = &cursor
	if err := session.Validate(); err == nil {
		t.Fatal("incomplete checkpoint accepted")
	}
}

func TestNewActivitiesWithBrowserProvider(t *testing.T) {
	activities := NewActivitiesWithBrowserProvider(nil, BrowserProviderKernel)
	if activities.browserProvider != BrowserProviderKernel {
		t.Fatalf("browser provider = %q, want %q", activities.browserProvider, BrowserProviderKernel)
	}
	if NewActivities(nil).browserProvider != BrowserProviderRod {
		t.Fatal("NewActivities should preserve the Rod default")
	}
}

func TestBrowserMutationRequiresCiphertextAndValidStatus(t *testing.T) {
	mutation := BrowserMutationChangelog{
		BrowserSessionID: 1, Provider: BrowserProviderKernel, ReplayGeneration: 1,
		Operation: "input_text", Context: BrowserMutationContext{URL: "https://example.com"},
		IdempotencyKey: "call-1", Status: BrowserMutationPending,
	}
	if err := mutation.Validate(); err == nil {
		t.Fatal("mutation without encrypted arguments accepted")
	}
	mutation.ArgumentsCiphertext = []byte("opaque-ciphertext")
	if err := mutation.Validate(); err != nil {
		t.Fatalf("valid mutation rejected: %v", err)
	}
	mutation.Result = &BrowserMutationResult{Outcome: BrowserMutationOutcomeApplied, ReplaySafe: true}
	if err := mutation.Validate(); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	mutation.Result.Outcome = "unexpected"
	if err := mutation.Validate(); err == nil {
		t.Fatal("invalid mutation result outcome accepted")
	}
	mutation.Status = "unknown"
	if err := mutation.Validate(); err == nil {
		t.Fatal("invalid mutation status accepted")
	}
}

func TestBrowserMutationJSONSchemaDriverValue(t *testing.T) {
	elementIndex := 3
	context := BrowserMutationContext{
		URL:          "https://example.com/form",
		Target:       &BrowserMutationTarget{Role: "textbox", Name: "Email"},
		ElementIndex: &elementIndex,
		ReplaySafe:   true,
	}
	value, err := context.Value()
	if err != nil {
		t.Fatalf("encode context: %v", err)
	}
	const expectedContext = `{"url":"https://example.com/form","target":{"role":"textbox","name":"Email"},"element_index":3,"replay_safe":true}`
	if value != expectedContext {
		t.Fatalf("context JSON = %#v, want %#v", value, expectedContext)
	}

	var decodedContext BrowserMutationContext
	if err := decodedContext.Scan([]byte(expectedContext)); err != nil {
		t.Fatalf("scan context: %v", err)
	}
	if decodedContext.URL != context.URL || decodedContext.Target == nil || decodedContext.Target.Name != "Email" ||
		decodedContext.ElementIndex == nil || *decodedContext.ElementIndex != elementIndex || !decodedContext.ReplaySafe {
		t.Fatalf("decoded context differs from input: %+v", decodedContext)
	}

	result := BrowserMutationResult{Outcome: BrowserMutationOutcomeApplied, ReplaySafe: true}
	resultValue, err := result.Value()
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	const expectedResult = `{"outcome":"applied","replay_safe":true}`
	if resultValue != expectedResult {
		t.Fatalf("result JSON = %#v, want %#v", resultValue, expectedResult)
	}
	var decodedResult BrowserMutationResult
	if err := decodedResult.Scan(expectedResult); err != nil {
		t.Fatalf("scan result: %v", err)
	}
	if decodedResult != result {
		t.Fatalf("decoded result = %+v, want %+v", decodedResult, result)
	}

	replayedResult := BrowserMutationResult{
		Outcome:         BrowserMutationOutcomeApplied,
		ReplaySafe:      true,
		ReplaySessionID: "provider-session-1",
	}
	replayedValue, err := replayedResult.Value()
	if err != nil {
		t.Fatalf("encode replay result: %v", err)
	}
	const expectedReplayResult = `{"outcome":"applied","replay_safe":true,"replay_session_id":"provider-session-1"}`
	if replayedValue != expectedReplayResult {
		t.Fatalf("replay result JSON = %#v, want %#v", replayedValue, expectedReplayResult)
	}
	var decodedReplayResult BrowserMutationResult
	if err := decodedReplayResult.Scan(expectedReplayResult); err != nil {
		t.Fatalf("scan replay result: %v", err)
	}
	if decodedReplayResult != replayedResult {
		t.Fatalf("decoded replay result = %+v, want %+v", decodedReplayResult, replayedResult)
	}
}

func TestBrowserCompositeUniqueIndexes(t *testing.T) {
	cases := []struct {
		model  any
		name   string
		fields []string
	}{
		{&BrowserProfile{}, "idx_browser_profile_user_provider", []string{"UserId", "Provider"}},
		{&BrowserVault{}, "idx_browser_vault_user_provider", []string{"UserId", "Provider"}},
		{&BrowserAuthConnection{}, "idx_browser_auth_profile_provider_domain", []string{"BrowserProfileID", "Provider", "Domain"}},
		{&BrowserSession{}, "idx_browser_session_job_provider", []string{"JobApplicationID", "Provider"}},
		{&BrowserMutationChangelog{}, "idx_browser_mutation_generation_key", []string{"BrowserSessionID", "ReplayGeneration", "IdempotencyKey"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := schema.Parse(tc.model, &sync.Map{}, schema.NamingStrategy{})
			if err != nil {
				t.Fatalf("parse model: %v", err)
			}
			for _, idx := range parsed.ParseIndexes() {
				if idx.Name != tc.name {
					continue
				}
				if idx.Class != "UNIQUE" {
					t.Fatalf("index %q is not unique: %q", tc.name, idx.Class)
				}
				if len(idx.Fields) != len(tc.fields) {
					t.Fatalf("index %q has %d fields, want %d", tc.name, len(idx.Fields), len(tc.fields))
				}
				for i, field := range tc.fields {
					if idx.Fields[i].Field.Name != field {
						t.Fatalf("index %q field %d = %q, want %q", tc.name, i, idx.Fields[i].Field.Name, field)
					}
				}
				return
			}
			t.Fatalf("unique index %q missing", tc.name)
		})
	}
}

func TestMigrateBrowserSchemaRejectsNilDatabase(t *testing.T) {
	if err := MigrateBrowserSchema(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}
