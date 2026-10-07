package jobapplication

import (
	"time"

	browseractivity "github.com/SomtoJF/iris-worker/activity/browser"
	jobapplicationprofile "github.com/SomtoJF/iris-worker/workflow/jobapplication/profile"
	"go.temporal.io/sdk/workflow"
)

// DeterministicFillResult contains metrics about what was filled
type DeterministicFillResult struct {
	ResumeFieldsFilled    int
	StructuredFieldsFilled int
	FailedResumeFill   bool
	FailedStructured   []string // List of structured field labels that failed
}

// fillDeterministicFields fills resume + structured fields without calling the LLM.
// Returns how many fields were filled and any errors encountered.
func fillDeterministicFields(
	ctx workflow.Context,
	workflowID string,
	userID uint,
	applicationID uint,
	classified *FieldClassificationResult,
	userProfile jobapplicationprofile.UserProfile,
	resumePath string,
) (*DeterministicFillResult, error) {
	logger := workflow.GetLogger(ctx)
	result := &DeterministicFillResult{
		FailedStructured: []string{},
	}

	if classified == nil || len(classified.Fields) == 0 {
		logger.Info("No classified fields to fill deterministically")
		return result, nil
	}

	formatter := NewFieldFormatter()

	for _, field := range classified.Fields {
		switch field.Type {
		case FieldTypeResume:
			// Resume upload is critical; if it fails, we need to know
			logger.Info("Filling resume field", "field_index", field.Index, "label", field.Label)

			err := workflow.ExecuteActivity(ctx, "UploadFile", browseractivity.UploadFileInput{
				WorkflowID:     workflowID,
				FilePath:       resumePath,
				FileInputIndex: field.Index,
				Target:         nil, // JEV classification doesn't provide Target, will be inferred from index
			}).Get(ctx, nil)

			if err != nil {
				logger.Error("Failed to upload resume", "field_index", field.Index, "error", err)
				result.FailedResumeFill = true
				// Continue anyway; planner will handle this
			} else {
				result.ResumeFieldsFilled++
			}

		case FieldTypeStructured:
			// Structured fields are nice-to-have; failures don't stop the application
			if field.ProfileFieldName == "" {
				logger.Warn("Structured field without profile field mapping", "field_index", field.Index, "label", field.Label)
				continue
			}

			// Detect format from this field's labels
			formatter.DetectPhoneFormatFromPlaceholder(field.Label, field.Description, field.Description)
			if field.StructuredType == "country" {
				formatter.DetectCountryFormatFromField(field.Label, field.Description, field.Description)
			}

			// Get and format the value
			value := formatter.GetFormattedValue(field.ProfileFieldName, userProfile)
			if value == "" {
				logger.Warn("No value for structured field", "field_index", field.Index, "profile_field", field.ProfileFieldName)
				continue
			}

			logger.Info("Filling structured field", "field_index", field.Index, "label", field.Label, "type", field.StructuredType)

			err := workflow.ExecuteActivity(ctx, "Type", browseractivity.TypeInput{
				WorkflowID:   workflowID,
				ElementIndex: field.Index,
				Text:         value,
				Replace:      true,
				Target:       nil, // JEV classification doesn't provide Target
			}).Get(ctx, nil)

			if err != nil {
				logger.Warn("Failed to fill structured field", "field_index", field.Index, "label", field.Label, "error", err)
				result.FailedStructured = append(result.FailedStructured, field.Label)
				// Continue with next field; LLM can retry if needed
			} else {
				result.StructuredFieldsFilled++
			}

		case FieldTypeOpenEnded, FieldTypeIgnore:
			// Skip; let LLM handle these
			logger.Debug("Skipping non-deterministic field", "field_index", field.Index, "type", field.Type)
		}
	}

	logger.Info("Deterministic fill complete", 
		"resume_filled", result.ResumeFieldsFilled,
		"structured_filled", result.StructuredFieldsFilled,
		"failed_structured_count", len(result.FailedStructured),
	)

	return result, nil
}

// filterClassifiedFieldsForPlanner removes already-filled fields from consideration,
// so the planner only sees open-ended fields that need LLM reasoning.
// Returns a subset of classified fields for the planner to handle.
func filterClassifiedFieldsForPlanner(classified *FieldClassificationResult) []ClassifiedField {
	if classified == nil {
		return []ClassifiedField{}
	}

	var openEnded []ClassifiedField
	for _, field := range classified.Fields {
		// Only return open-ended and ignore fields; structured/resume are already filled
		if field.Type == FieldTypeOpenEnded || field.Type == FieldTypeIgnore {
			openEnded = append(openEnded, field)
		}
	}
	return openEnded
}

// extractRequiredFieldsFromClassified converts ClassifiedField back to matching
// TaggedNode indices so the planner knows which fields remain.
// extractRequiredFieldsFromClassified rebuilds SerializableTaggedNodes for open-ended fields
// that still need planner reasoning, filtered from the full screenshot.
func extractRequiredFieldsFromClassified(openEndedClassified []ClassifiedField, allTaggedNodes []browseractivity.SerializableTaggedNode) []browseractivity.SerializableTaggedNode {
	// Create a set of indices for quick lookup
	openEndedIndices := make(map[int]bool)
	for _, field := range openEndedClassified {
		openEndedIndices[field.Index] = true
	}

	// Filter taggedNodes to only include open-ended fields
	var required []browseractivity.SerializableTaggedNode
	for _, node := range allTaggedNodes {
		if openEndedIndices[node.Index] {
			// Copy to avoid accidental mutation
			copied := node
			if node.Value != nil {
				v := *node.Value
				copied.Value = &v
			}
			if node.Required != nil {
				r := *node.Required
				copied.Required = &r
			}
			required = append(required, copied)
		}
	}
	return required
}

// ActivityOptions returns standard activity options for deterministic fills
func ActivityOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
}
