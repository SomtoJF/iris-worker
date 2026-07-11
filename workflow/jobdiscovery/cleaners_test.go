package jobdiscovery

import (
	"testing"

	"github.com/SomtoJF/iris-worker/activity/web"
)

func TestNormalizeGreenhouseJobURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{
			"http://job-boards.greenhouse.io/energyhub/jobs/8617111002",
			"https://job-boards.greenhouse.io/energyhub/jobs/8617111002",
			true,
		},
		{
			"https://job-boards.greenhouse.io/andurilindustries/jobs/5162263007?gh_jid=5162263007",
			"https://job-boards.greenhouse.io/andurilindustries/jobs/5162263007",
			true,
		},
		{
			"https://job-boards.greenhouse.io/acme",
			"",
			false,
		},
	}
	for _, tt := range tests {
		got, ok := normalizeGreenhouseJobURL(tt.raw)
		if ok != tt.ok || got != tt.want {
			t.Errorf("normalizeGreenhouseJobURL(%q) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNormalizeAshbyJobURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{
			"https://jobs.ashbyhq.com/crogl/c3ae1140-2ec8-4298-9dcd-3f22a18ac27e?embed=js",
			"https://jobs.ashbyhq.com/crogl/c3ae1140-2ec8-4298-9dcd-3f22a18ac27e",
			true,
		},
		{
			"https://jobs.ashbyhq.com/flock%20safety/c54cdc7f-7d31-4983-824d-37fdc60d21e8",
			"https://jobs.ashbyhq.com/flock%20safety/c54cdc7f-7d31-4983-824d-37fdc60d21e8",
			true,
		},
		{
			"https://jobs.ashbyhq.com/acme/abc/application",
			"https://jobs.ashbyhq.com/acme/abc",
			true,
		},
		{
			"https://jobs.ashby.com/acme/abc?utm_source=x",
			"https://jobs.ashbyhq.com/acme/abc",
			true,
		},
		{
			"https://jobs.ashbyhq.com/acme",
			"",
			false,
		},
	}
	for _, tt := range tests {
		got, ok := normalizeAshbyJobURL(tt.raw)
		if ok != tt.ok || got != tt.want {
			t.Errorf("normalizeAshbyJobURL(%q) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNormalizeLeverJobURL(t *testing.T) {
	got, company, ok := normalizeLeverJobURL("https://jobs.lever.co/qrypt/c1ed6b7c-eadd-4476-9abc-b4b1e1272573?lever-origin=applied&lever-source%5B%5D=BuiltInNationwide")
	if !ok || company != "qrypt" {
		t.Fatalf("got ok=%v company=%q link=%q", ok, company, got)
	}
	want := "https://jobs.lever.co/qrypt/c1ed6b7c-eadd-4476-9abc-b4b1e1272573"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got, _, ok = normalizeLeverJobURL("https://jobs.lever.co/acme/uuid/apply")
	if !ok || got != "https://jobs.lever.co/acme/uuid" {
		t.Fatalf("apply strip: %q ok=%v", got, ok)
	}
}

func TestNormalizeWorkableJobURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{
			"https://apply.workable.com/atec-spine/j/0DB6568C6C/apply/",
			"https://apply.workable.com/atec-spine/j/0DB6568C6C",
			true,
		},
		{
			"http://apply.workable.com/valsoft-corp/j/DF7F412055?foo=1",
			"https://apply.workable.com/valsoft-corp/j/DF7F412055",
			true,
		},
		{
			"https://apply.workable.com/valsoft-corp/",
			"",
			false,
		},
	}
	for _, tt := range tests {
		got, ok := normalizeWorkableJobURL(tt.raw)
		if ok != tt.ok || got != tt.want {
			t.Errorf("normalizeWorkableJobURL(%q) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCleanLeverDropsJobgether(t *testing.T) {
	items := []web.SerperOrganicResult{
		{
			Title: "Jobgether - Software Engineer, Design Systems - Lever",
			Link:  "https://jobs.lever.co/jobgether/7c454f26-ad63-4d1e-b88b-7edac3fb8f28",
		},
		{
			Title: "Embedded Software Engineer - Qrypt - Lever",
			Link:  "https://jobs.lever.co/qrypt/c1ed6b7c-eadd-4476-9abc-b4b1e1272573?x=1",
		},
	}
	out := cleanLeverResults(items)
	if len(out) != 1 {
		t.Fatalf("len=%d want 1", len(out))
	}
	if out[0].Link != "https://jobs.lever.co/qrypt/c1ed6b7c-eadd-4476-9abc-b4b1e1272573" {
		t.Fatalf("link=%q", out[0].Link)
	}
}

func TestCleanWorkableDedupesApplyVariant(t *testing.T) {
	items := []web.SerperOrganicResult{
		{Title: "A", Link: "https://apply.workable.com/atec-spine/j/0DB6568C6C/"},
		{Title: "B", Link: "https://apply.workable.com/atec-spine/j/0DB6568C6C/apply/"},
	}
	out := cleanWorkableResults(items)
	if len(out) != 2 {
		t.Fatalf("cleaner keeps both rows (dedupe is LLM); len=%d", len(out))
	}
	for _, row := range out {
		if row.Link != "https://apply.workable.com/atec-spine/j/0DB6568C6C" {
			t.Fatalf("link=%q", row.Link)
		}
	}
}

func TestNormalizeWellfoundJobURL(t *testing.T) {
	got, ok := normalizeWellfoundJobURL("http://www.wellfound.com/jobs/4446152-senior-software-engineer-ii?ref=x")
	if !ok {
		t.Fatal("expected ok")
	}
	want := "https://wellfound.com/jobs/4446152-senior-software-engineer-ii"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
