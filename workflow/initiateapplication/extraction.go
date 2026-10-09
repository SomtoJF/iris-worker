package initiateapplication

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/workflow"
)

func extractJobDetailsFromText(ctx workflow.Context, pageText string, userID, applicationID uint) (jobDetails, error) {
	request := types.AIPIRequest{
		SystemMessage: "Extract the job title, company name, and job description from the provided scraped webpage content. Return the data in JSON format. Most job descriptions have a 'Who we are' or 'About us' or 'Company Description' section that contains the company's details. This is where you should look for the company name. The job description should be returned in its entirety and formatted in markdown. If this page doesn't include the job description, it is invalid and you should set is_valid_job_posting to false. For invalid job postings, return an empty string for the job description, job title, and company name.",
		UserMessage:   fmt.Sprintf("Scraped content:\n\n%s", pageText),
		Model:         "deepseek/deepseek-v4-flash",
		ResponseSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_title":            map[string]interface{}{"type": "string"},
				"company_name":         map[string]interface{}{"type": "string"},
				"job_description":      map[string]interface{}{"type": "string"},
				"is_valid_job_posting": map[string]interface{}{"type": "boolean"},
			},
			"required": []string{"job_title", "company_name", "job_description", "is_valid_job_posting"},
		},
		IdUser:           userID,
		IdJobApplication: &applicationID,
	}

	var response types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", request).Get(ctx, &response); err != nil {
		return jobDetails{}, err
	}

	var details jobDetails
	if err := json.Unmarshal([]byte(stripJSONFences(response.Content)), &details); err != nil {
		return jobDetails{}, fmt.Errorf("unmarshal job details: %w", err)
	}
	return details, nil
}

func stripJSONFences(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimSpace(strings.TrimPrefix(content, "```"))
	if len(content) >= 4 && strings.EqualFold(content[:4], "json") {
		content = strings.TrimSpace(content[4:])
	}
	if index := strings.LastIndex(content, "```"); index >= 0 {
		content = strings.TrimSpace(content[:index])
	}
	return content
}
