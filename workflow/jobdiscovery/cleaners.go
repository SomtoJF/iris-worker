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

// leverAggregatorSlugs are Lever "company" accounts that syndicate many employers'
// roles (not the hiring company itself). Extend as more aggregators show up.
var leverAggregatorSlugs = map[string]struct{}{
	"jobgether": {},
}

// cleanGreenhouseResults keeps only Greenhouse job-board URLs matching
// https://job-boards.greenhouse.io/<company_name>/jobs/<job_id>.
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
// Drops /apply, query params, and known aggregator company boards (e.g. Jobgether).
func cleanLeverResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	out := make([]web.SerperOrganicResult, 0, len(items))
	for i := range items {
		newLink, company, ok := normalizeLeverJobURL(items[i].Link)
		if !ok {
			continue
		}
		if isLeverAggregator(company, items[i].Title) {
			continue
		}
		row := items[i]
		row.Link = newLink
		out = append(out, row)
	}
	return out
}

// cleanWellfoundResults keeps only URLs matching https://wellfound.com/jobs/<job_id>.
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

// cleanWorkableResults normalizes to https://apply.workable.com/<company>/j/<job_id>,
// stripping a trailing /apply segment and query params.
func cleanWorkableResults(items []web.SerperOrganicResult) []web.SerperOrganicResult {
	out := make([]web.SerperOrganicResult, 0, len(items))
	for i := range items {
		newLink, ok := normalizeWorkableJobURL(items[i].Link)
		if !ok {
			continue
		}
		row := items[i]
		row.Link = newLink
		out = append(out, row)
	}
	return out
}

// cleanAshbyResults normalizes Ashby URLs to https://jobs.ashbyhq.com/<company>/<job_id>.
// Application flows append a final "application" path segment and query params; that segment is dropped.
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

func isLeverAggregator(companySlug, title string) bool {
	if _, ok := leverAggregatorSlugs[strings.ToLower(strings.TrimSpace(companySlug))]; ok {
		return true
	}
	// Titles are often "Jobgether - Role Name - Lever"
	return strings.Contains(strings.ToLower(title), "jobgether")
}

func urlPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	segs := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		decoded, err := url.PathUnescape(p)
		if err != nil {
			decoded = p
		}
		segs = append(segs, decoded)
	}
	return segs
}

// parseJobURLHost parses raw and returns the URL plus lowercase host without www.
func parseJobURLHost(raw string) (*url.URL, string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, "", false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	return u, host, true
}

// canonicalizeHTTPS builds https://host/seg/... with no query or fragment.
// Path segments should already be decoded; url.URL re-encodes as needed.
func canonicalizeHTTPS(host string, segs []string) string {
	u := &url.URL{
		Scheme: "https",
		Host:   host,
		Path:   "/" + strings.Join(segs, "/"),
	}
	return u.String()
}

func normalizeAshbyJobURL(raw string) (string, bool) {
	u, host, ok := parseJobURLHost(raw)
	if !ok {
		return "", false
	}
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

	return canonicalizeHTTPS("jobs.ashbyhq.com", segs), true
}

func normalizeLeverJobURL(raw string) (canonical string, company string, ok bool) {
	u, host, parsed := parseJobURLHost(raw)
	if !parsed || host != "jobs.lever.co" {
		return "", "", false
	}

	segs := urlPathSegments(u.Path)
	if len(segs) == 0 {
		return "", "", false
	}

	last := segs[len(segs)-1]
	if strings.EqualFold(last, "apply") {
		segs = segs[:len(segs)-1]
	}

	if len(segs) != 2 {
		return "", "", false
	}

	return canonicalizeHTTPS(jobSourceLever, segs), segs[0], true
}

func normalizeWellfoundJobURL(raw string) (string, bool) {
	u, host, ok := parseJobURLHost(raw)
	if !ok || host != jobSourceWellfound {
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

	return canonicalizeHTTPS(jobSourceWellfound, segs), true
}

func normalizeGreenhouseJobURL(raw string) (string, bool) {
	u, host, ok := parseJobURLHost(raw)
	if !ok || host != jobSourceGreenhouse {
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

	return canonicalizeHTTPS(jobSourceGreenhouse, segs), true
}

// normalizeWorkableJobURL keeps https://apply.workable.com/<company>/j/<job_id>.
// Trailing /apply (application step) is stripped.
func normalizeWorkableJobURL(raw string) (string, bool) {
	u, host, ok := parseJobURLHost(raw)
	if !ok || host != jobSourceWorkable {
		return "", false
	}

	segs := urlPathSegments(u.Path)
	if len(segs) == 0 {
		return "", false
	}

	if strings.EqualFold(segs[len(segs)-1], "apply") {
		segs = segs[:len(segs)-1]
	}

	// /<company>/j/<job_id>
	if len(segs) != 3 {
		return "", false
	}
	if !strings.EqualFold(segs[1], "j") {
		return "", false
	}
	if segs[0] == "" || segs[2] == "" {
		return "", false
	}

	return canonicalizeHTTPS(jobSourceWorkable, segs), true
}
