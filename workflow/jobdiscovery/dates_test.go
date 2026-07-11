package jobdiscovery

import (
	"testing"
	"time"
)

func TestNormalizeSerperDate(t *testing.T) {
	now := time.Date(2026, 7, 11, 15, 30, 0, 0, time.UTC)

	tests := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"2026-07-01", "2026-07-01"},
		{"today", "2026-07-11"},
		{"yesterday", "2026-07-10"},
		{"12 hours ago", "2026-07-11"}, // 15:30 - 12h = 03:30 same day
		{"1 day ago", "2026-07-10"},
		{"5 days ago", "2026-07-06"},
		{"1 week ago", "2026-07-04"},
		{"2 weeks ago", "2026-06-27"},
		{"18 hours ago", "2026-07-10"}, // 15:30 - 18h = previous evening
		{"30 minutes ago", "2026-07-11"},
		{"1 month ago", "2026-06-11"},
		{"12 Apr, 2026", "2026-04-12"},
		{"Apr 12, 2026", "2026-04-12"},
		{"not a date", ""},
	}

	for _, tt := range tests {
		got := normalizeSerperDate(tt.raw, now)
		if got != tt.want {
			t.Errorf("normalizeSerperDate(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestApplyDeterministicDates(t *testing.T) {
	merged := []mergedSearchHit{
		{Link: "https://jobs.ashbyhq.com/acme/abc", Date: "2026-07-09"},
		{Link: "http://www.jobs.lever.co/acme/xyz/", Date: "2026-07-08"},
	}
	jobs := []DiscoveredJob{
		{Url: "https://jobs.ashbyhq.com/acme/abc", DatePosted: "wrong"},
		{Url: "https://jobs.lever.co/acme/xyz", DatePosted: ""},
		{Url: "https://unknown.example/job", DatePosted: "keep-me"},
	}
	applyDeterministicDates(jobs, merged)
	if jobs[0].DatePosted != "2026-07-09" {
		t.Fatalf("jobs[0]=%q", jobs[0].DatePosted)
	}
	if jobs[1].DatePosted != "2026-07-08" {
		t.Fatalf("jobs[1]=%q", jobs[1].DatePosted)
	}
	if jobs[2].DatePosted != "keep-me" {
		t.Fatalf("jobs[2]=%q", jobs[2].DatePosted)
	}
}
