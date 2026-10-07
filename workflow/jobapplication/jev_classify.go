package jobapplication

import (
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/aipi/types"
	jobapplicationprofile "github.com/SomtoJF/iris-worker/workflow/jobapplication/profile"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// FieldClassificationType categorizes form fields into fillable categories
type FieldClassificationType string

const (
	FieldTypeResume      FieldClassificationType = "resume_file"
	FieldTypeStructured  FieldClassificationType = "structured"
	FieldTypeOpenEnded   FieldClassificationType = "open_ended"
	FieldTypeIgnore      FieldClassificationType = "ignore"
)

// ClassifiedField represents a form field and how it should be filled
type ClassifiedField struct {
	Index            int
	Label            string
	Description      string
	Type             FieldClassificationType
	StructuredType   string // For structured fields: email, phone, country, first_name, last_name, linkedin
	ProfileFieldName string // Maps to UserProfile field
}

// FieldClassificationResult contains all classified fields for a form
type FieldClassificationResult struct {
	Fields []ClassifiedField
}

// classifyFieldsWithJev calls JEV to classify all form fields into categories.
// Returns a list of ClassifiedField structs with type and profile field mapping.
func classifyFieldsWithJev(
	ctx workflow.Context,
	workflowID string,
	userID uint,
	applicationID uint,
	taggedNodes []browser.SerializableTaggedNode,
	userProfile jobapplicationprofile.UserProfile,
) (*FieldClassificationResult, error) {
	if len(taggedNodes) == 0 {
		return &FieldClassificationResult{Fields: []ClassifiedField{}}, nil
	}

	// Serialize tagged nodes as accessibility tree state for JEV
	stateText := serializeTaggedNodesForJev(taggedNodes, userProfile)

	jevCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    2,
		},
	})

	// Build the JEV request: ask JEV to classify each field
	questions := make(map[string]types.JevQuestion)
	for _, node := range taggedNodes {
		fieldKey := fmt.Sprintf("field_%d_type", node.Index)
		questions[fieldKey] = types.JevQuestion{
			Type: "choice",
			Instructions: fmt.Sprintf(
				"For form field at index %d (label: '%s', description: '%s'), what type of field is this?",
				node.Index, node.Label, node.Description,
			),
			Criteria: map[string]string{
				"resume_file":  "File upload field specifically for resume/CV PDF",
				"structured":   "Structured field like email, phone, country, name, or LinkedIn URL",
				"open_ended":   "Open-ended text field like motivation, why join, cover letter, or essay",
				"ignore":       "Hidden field, already filled, or not meant to be filled by applicant",
			},
		}
	}

	// Also ask JEV to identify what type of structured field it is (if structured)
	for _, node := range taggedNodes {
		fieldKey := fmt.Sprintf("field_%d_structured_type", node.Index)
		questions[fieldKey] = types.JevQuestion{
			Type: "choice",
			Instructions: fmt.Sprintf(
				"If field %d is a structured field, what structured data type is it?",
				node.Index,
			),
			Criteria: map[string]string{
				"email":      "Email address field",
				"phone":      "Phone number field",
				"country":    "Country or country code field",
				"first_name": "First name field",
				"last_name":  "Last name field",
				"linkedin":   "LinkedIn profile URL field",
				"unknown":    "Unknown structured field or not applicable",
			},
		}
	}

	var result types.JevResponse
	if err := workflow.ExecuteActivity(jevCtx, "CallJev", types.JevRequest{
		State: map[string]string{
			"form_state": stateText,
		},
		Questions:       questions,
		IdUser:          userID,
		IdJobApplication: &applicationID,
	}).Get(ctx, &result); err != nil {
		return nil, fmt.Errorf("JEV field classification: %w", err)
	}

	// Parse JEV response and build classified fields list
	classified, err := parseClassificationResponse(result, taggedNodes)
	if err != nil {
		return nil, fmt.Errorf("parse JEV classification: %w", err)
	}

	return &FieldClassificationResult{Fields: classified}, nil
}

// serializeTaggedNodesForJev converts the accessibility tree into a text representation
// that JEV can process without images.
func serializeTaggedNodesForJev(
	taggedNodes []browser.SerializableTaggedNode,
	userProfile jobapplicationprofile.UserProfile,
) string {
	var sb strings.Builder

	sb.WriteString("FORM FIELDS:\n")
	sb.WriteString("============\n\n")

	for _, node := range taggedNodes {
		sb.WriteString(fmt.Sprintf("Field Index %d:\n", node.Index))
		sb.WriteString(fmt.Sprintf("  Label: %s\n", node.Label))
		sb.WriteString(fmt.Sprintf("  Description: %s\n", node.Description))
		sb.WriteString(fmt.Sprintf("  Name: %s\n", node.Name))
		sb.WriteString(fmt.Sprintf("  Role: %s\n", node.Role))
		value := ""
		if node.Value != nil {
			value = *node.Value
		}
		sb.WriteString(fmt.Sprintf("  Value: %s\n", value))
		sb.WriteString(fmt.Sprintf("  Required: %v\n", node.Required))
		sb.WriteString(fmt.Sprintf("  Selector: %s\n", node.Selector))
		sb.WriteString("\n")
	}

	sb.WriteString("\nUSER PROFILE DATA AVAILABLE:\n")
	sb.WriteString("=============================\n")
	sb.WriteString(fmt.Sprintf("First Name: %s\n", userProfile.FirstName))
	sb.WriteString(fmt.Sprintf("Last Name: %s\n", userProfile.LastName))
	sb.WriteString(fmt.Sprintf("Email: %s\n", userProfile.Email))
	sb.WriteString(fmt.Sprintf("Phone: %s\n", userProfile.Phone))
	sb.WriteString(fmt.Sprintf("Country: %s\n", userProfile.CountryOfResidence))
	sb.WriteString(fmt.Sprintf("LinkedIn: %v\n", userProfile.LinkedInUrl))

	return sb.String()
}

// parseClassificationResponse converts JEV's response into ClassifiedField structs
func parseClassificationResponse(
	result types.JevResponse,
	taggedNodes []browser.SerializableTaggedNode,
) ([]ClassifiedField, error) {
	var classified []ClassifiedField

	for _, node := range taggedNodes {
		fieldKey := fmt.Sprintf("field_%d_type", node.Index)
		typeAnswer, ok := result.Answers[fieldKey]
		if !ok || typeAnswer.Type != "choice" || typeAnswer.Choice == "" {
			// If JEV didn't classify this field, skip it
			continue
		}

		fieldType := FieldClassificationType(typeAnswer.Choice)

		cf := ClassifiedField{
			Index:       node.Index,
			Label:       node.Label,
			Description: node.Description,
			Type:        fieldType,
		}

		// If it's a structured field, determine the structured type
		if fieldType == FieldTypeStructured {
			structuredKey := fmt.Sprintf("field_%d_structured_type", node.Index)
			structuredAnswer, ok := result.Answers[structuredKey]
			if ok && structuredAnswer.Type == "choice" && structuredAnswer.Choice != "" {
				cf.StructuredType = structuredAnswer.Choice
				cf.ProfileFieldName = structuredFieldToProfileField(cf.StructuredType)
			}
		}

		classified = append(classified, cf)
	}

	return classified, nil
}

// structuredFieldToProfileField maps a structured field type to the UserProfile field name
func structuredFieldToProfileField(fieldType string) string {
	switch fieldType {
	case "email":
		return "Email"
	case "phone":
		return "Phone"
	case "country":
		return "CountryOfResidence"
	case "first_name":
		return "FirstName"
	case "last_name":
		return "LastName"
	case "linkedin":
		return "LinkedInUrl"
	default:
		return ""
	}
}
