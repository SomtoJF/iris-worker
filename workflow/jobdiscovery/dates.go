package jobdiscovery

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reAlreadyISODate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reRelativeAgo    = regexp.MustCompile(`^(\d+)\s+(minute|minutes|hour|hours|day|days|week|weeks|month|months)\s+ago$`)
)

// normalizeSerperDate converts a Serper organic "date" field to YYYY-MM-DD.
// Relative phrases are resolved against noon on now's calendar date (same convention
// as the job-discovery prompt). Unknown or empty values return "".
func normalizeSerperDate(raw string, now time.Time) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if reAlreadyISODate.MatchString(s) {
		return s
	}

	lower := strings.ToLower(s)
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)

	switch lower {
	case "today":
		return today.Format("2006-01-02")
	case "yesterday":
		return today.AddDate(0, 0, -1).Format("2006-01-02")
	}

	if m := reRelativeAgo.FindStringSubmatch(lower); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 0 {
			return ""
		}
		unit := m[2]
		switch unit {
		case "minute", "minutes", "hour", "hours":
			ref := now.UTC()
			var d time.Duration
			if unit == "minute" || unit == "minutes" {
				d = time.Duration(n) * time.Minute
			} else {
				d = time.Duration(n) * time.Hour
			}
			return ref.Add(-d).Format("2006-01-02")
		case "day", "days":
			return today.AddDate(0, 0, -n).Format("2006-01-02")
		case "week", "weeks":
			return today.AddDate(0, 0, -7*n).Format("2006-01-02")
		case "month", "months":
			return today.AddDate(0, -n, 0).Format("2006-01-02")
		}
	}

	layouts := []string{
		"2 Jan, 2006",
		"2 Jan 2006",
		"Jan 2, 2006",
		"Jan 2 2006",
		"2 January, 2006",
		"2 January 2006",
		"January 2, 2006",
		"January 2 2006",
		"2006-01-02",
		"01/02/2006",
		"1/2/2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}

	return ""
}

// applyDeterministicDates overwrites each job's DatePosted from the matching merged hit
// (by URL), so posting dates come from Serper parsing rather than the LLM.
func applyDeterministicDates(jobs []DiscoveredJob, merged []mergedSearchHit) {
	byURL := make(map[string]string, len(merged))
	for _, m := range merged {
		if m.Date == "" {
			continue
		}
		byURL[urlKey(m.Link)] = m.Date
	}
	for i := range jobs {
		if d, ok := byURL[urlKey(jobs[i].Url)]; ok {
			jobs[i].DatePosted = d
		}
	}
}

func urlKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	}
	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	path := strings.TrimRight(u.Path, "/")
	return host + path
}
