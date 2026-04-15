package jobdiscovery

import (
	"net/url"
	"strings"

	"github.com/SomtoJF/iris-worker/activity/web"
)

const (
	jobSourceGreenhouse  = "job-boards.greenhouse.io"
	jobSourceLever       = "jobs.lever.co"
	jobSourceWellfound   = "wellfound.com"
	jobSourceWorkable    = "apply.workable.com"
	jobSourceAshby       = "jobs.ashbyhq.com"
	jobSourceRemotefront = "remotefront.com"
)

// cleanGreenhouseResults keeps only Greenhouse job-board URLs matching
// https://job-boards.greenhouse.io/<company_name>/jobs/<job_id> (no trailing segment stripping).
func cleanGreenhouseResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	out := make([]web.SerperOrganicResult, 0, len(items))
	for i := range items {
		newLink, ok := normalizeGreenhouseJobURL(items[i].Link)
		if !ok {
			continue
		}
		row := items[i]
		row.Link = newLink
		out = append(out, row)
	}
	return out
}

// cleanLeverResults normalizes Lever URLs to https://jobs.lever.co/<company>/<job_id>.
// Some links end with an /apply segment; that trailing segment is dropped (same shape as Ashby after stripping the apply step).
// Exactly two path segments must remain; otherwise the hit is dropped.
func cleanLeverResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	out := make([]web.SerperOrganicResult, 0, len(items))
	for i := range items {
		newLink, ok := normalizeLeverJobURL(items[i].Link)
		if !ok {
			continue
		}
		row := items[i]
		row.Link = newLink
		out = append(out, row)
	}
	return out
}

// cleanWellfoundResults keeps only URLs matching https://wellfound.com/jobs/<job_id> (no trailing segment stripping).
func cleanWellfoundResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	out := make([]web.SerperOrganicResult, 0, len(items))
	for i := range items {
		newLink, ok := normalizeWellfoundJobURL(items[i].Link)
		if !ok {
			continue
		}
		row := items[i]
		row.Link = newLink
		out = append(out, row)
	}
	return out
}

func cleanWorkableResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	return items
}

// cleanAshbyResults normalizes Ashby URLs to https://jobs.ashbyhq.com/<company>/<job_id>.
// Application flows append a final "application" path segment and query params; that segment is dropped.
// After cleanup, exactly two path segments must remain (company slug, job id); otherwise the hit is dropped.
func cleanAshbyResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	out := make([]web.SerperOrganicResult, 0, len(items))
	for i := range items {
		newLink, ok := normalizeAshbyJobURL(items[i].Link)
		if !ok {
			continue
		}
		row := items[i]
		row.Link = newLink
		out = append(out, row)
	}
	return out
}

func cleanRemotefrontResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	return items
}

func cleanResultsForJobSource(jobSource string, items []web.SerperOrganicResult) []web.SerperOrganicResult {
	switch jobSource {
	case jobSourceGreenhouse:
		return cleanGreenhouseResults(items)
	case jobSourceLever:
		return cleanLeverResults(items)
	case jobSourceWellfound:
		return cleanWellfoundResults(items)
	case jobSourceWorkable:
		return cleanWorkableResults(items)
	case jobSourceAshby:
		return cleanAshbyResults(items)
	case jobSourceRemotefront:
		return cleanRemotefrontResults(items)
	default:
		return items
	}
}

func urlPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	segs := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			segs = append(segs, p)
		}
	}
	return segs
}

func normalizeAshbyJobURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}

	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	if host != "jobs.ashbyhq.com" && host != "jobs.ashby.com" {
		return "", false
	}

	segs := urlPathSegments(u.Path)
	if len(segs) == 0 {
		return "", false
	}

	last := segs[len(segs)-1]
	if strings.HasPrefix(strings.ToLower(last), "application") {
		segs = segs[:len(segs)-1]
	}

	// Job posting: /<company_name>/<job_id>
	if len(segs) != 2 {
		return "", false
	}

	u.Path = "/" + strings.Join(segs, "/")
	return u.String(), true
}

func normalizeLeverJobURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}

	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	if host != "jobs.lever.co" {
		return "", false
	}

	segs := urlPathSegments(u.Path)
	if len(segs) == 0 {
		return "", false
	}

	last := segs[len(segs)-1]
	if strings.EqualFold(last, "apply") {
		segs = segs[:len(segs)-1]
	}

	if len(segs) != 2 {
		return "", false
	}

	u.Path = "/" + strings.Join(segs, "/")
	return u.String(), true
}

func normalizeWellfoundJobURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}

	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	if host != jobSourceWellfound {
		return "", false
	}

	segs := urlPathSegments(u.Path)
	if len(segs) != 2 {
		return "", false
	}
	if !strings.EqualFold(segs[0], "jobs") {
		return "", false
	}
	if segs[1] == "" {
		return "", false
	}

	u.Path = "/" + strings.Join(segs, "/")
	return u.String(), true
}

func normalizeGreenhouseJobURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}

	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	if host != jobSourceGreenhouse {
		return "", false
	}

	segs := urlPathSegments(u.Path)
	if len(segs) != 3 {
		return "", false
	}
	if !strings.EqualFold(segs[1], "jobs") {
		return "", false
	}
	if segs[0] == "" || segs[2] == "" {
		return "", false
	}

	u.Path = "/" + strings.Join(segs, "/")
	return u.String(), true
}
