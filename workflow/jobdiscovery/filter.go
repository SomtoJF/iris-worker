package jobdiscovery

import (
	"fmt"
	"strings"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/workflow"
)

const jobPostingJevThreshold = 0.5

func filterJobHitsWithJev(ctx workflow.Context, hits []mergedSearchHit, idUser uint) ([]mergedSearchHit, error) {
	if len(hits) == 0 {
		return nil, nil
	}

	promptHits := userPromptHitsFromMerged(hits)
	questions := make(map[string]types.JevQuestion, len(hits))
	for _, hit := range promptHits {
		questions[jobHitQuestionKey(hit.Index)] = types.JevQuestion{
			Type: "noul",
			Instructions: "Does this search result point to one specific, concrete job posting for a single role? " +
				"Reject career indexes, search/filter pages, company pages, articles, and results whose destination is ambiguous.",
			Criteria: map[string]string{
				"true":  "The URL and result text identify one specific job-detail or application page.",
				"false": "The URL leads to a listing, search page, non-job page, or ambiguous destination.",
			},
		}
	}

	var response types.JevResponse
	if err := workflow.ExecuteActivity(ctx, "CallDecisions", types.JevRequest{
		State: map[string]any{
			"search_results": promptHits,
		},
		Questions: questions,
		IdUser:    idUser,
	}).Get(ctx, &response); err != nil {
		return nil, fmt.Errorf("filter job-search hits with JEV: %w", err)
	}

	approved := make([]mergedSearchHit, 0, len(hits))
	for i, hit := range hits {
		answer, ok := response.Answers[jobHitQuestionKey(i)]
		if !ok || answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
			continue
		}
		if *answer.Noul >= jobPostingJevThreshold {
			approved = append(approved, hit)
		}
	}
	return approved, nil
}

func jobHitQuestionKey(index int) string {
	return fmt.Sprintf("job_hit_%d", index)
}

func postFilterExtractedJobs(extracted []extractedJob, approvedHits []mergedSearchHit) []DiscoveredJob {
	out := make([]DiscoveredJob, 0, len(extracted))
	seen := make(map[string]struct{}, len(extracted))
	for _, fields := range extracted {
		if fields.HitIndex < 0 || fields.HitIndex >= len(approvedHits) {
			continue
		}
		hit := approvedHits[fields.HitIndex]
		title := strings.TrimSpace(fields.Title)
		if title == "" || !isConcreteJobPostingURL(hit.Link) {
			continue
		}

		key := urlKey(hit.Link)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, DiscoveredJob{
			Title:       title,
			Url:         hit.Link,
			CompanyName: strings.TrimSpace(fields.CompanyName),
			DatePosted:  hit.Date,
		})
	}
	return out
}

func isConcreteJobPostingURL(raw string) bool {
	u, host, ok := parseJobURLHost(raw)
	if !ok || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}

	var normalized string
	switch host {
	case jobSourceGreenhouse:
		normalized, ok = normalizeGreenhouseJobURL(raw)
	case jobSourceLever:
		normalized, _, ok = normalizeLeverJobURL(raw)
	case jobSourceWellfound:
		normalized, ok = normalizeWellfoundJobURL(raw)
	case jobSourceWorkable:
		normalized, ok = normalizeWorkableJobURL(raw)
	case jobSourceAshby:
		normalized, ok = normalizeAshbyJobURL(raw)
	case jobSourceRemotefront:
		segments := urlPathSegments(u.Path)
		if len(segments) < 2 {
			return false
		}
		for _, segment := range segments {
			switch strings.ToLower(segment) {
			case "search", "category", "categories", "location", "locations":
				return false
			}
		}
		return true
	default:
		return false
	}
	return ok && urlKey(normalized) == urlKey(raw)
}
