package jobapplication

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestDeduplicateQAMergesEquivalentPairsAndPreservesAnswer(t *testing.T) {
	questions := []sqldb.JobApplicationQuestion{
		{Question: "Are you authorized to work here?", Answer: "Yes"},
		{Question: "Work authorization", Answer: " yes "},
	}
	result, request := runQADedupTest(t, questions, func(req types.JevRequest) (types.JevResponse, error) {
		return qaJevResponse(req, map[string]float64{
			"same_question_0_1":      0.99,
			"compatible_answers_0_1": 0.99,
		}), nil
	})

	if result.Error != "" {
		t.Fatalf("deduplicateQA error = %q", result.Error)
	}
	if len(result.Questions) != 1 {
		t.Fatalf("questions = %#v, want one merged entry", result.Questions)
	}
	if result.Questions[0].Question != questions[0].Question {
		t.Errorf("merged question = %q, want clearest original %q", result.Questions[0].Question, questions[0].Question)
	}
	if result.Questions[0].Answer != questions[0].Answer {
		t.Errorf("merged answer = %q, want original answer text %q", result.Questions[0].Answer, questions[0].Answer)
	}

	var state struct {
		Questions []indexedQA `json:"questions"`
	}
	stateJSON, err := json.Marshal(request.State)
	if err != nil {
		t.Fatalf("marshal JEV state: %v", err)
	}
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		t.Fatalf("unmarshal JEV state: %v", err)
	}
	if len(state.Questions) != len(questions) || state.Questions[1].Index != 1 || state.Questions[1].Answer != questions[1].Answer {
		t.Errorf("JEV indexed state = %#v, want original indexed Q&A", state.Questions)
	}
	if len(request.Questions) != 2 {
		t.Errorf("JEV pair decisions = %d, want same-question and answer-compatibility decisions", len(request.Questions))
	}
	if request.IdUser != 17 || request.IdJobApplication == nil || *request.IdJobApplication != 29 {
		t.Errorf("JEV identifiers = user %d, application %v; want 17, 29", request.IdUser, request.IdJobApplication)
	}
}

func TestDeduplicateQAKeepsDistinctQuestionsAndConflictingAnswers(t *testing.T) {
	questions := []sqldb.JobApplicationQuestion{
		{Question: "Are you authorized to work here?", Answer: "Yes"},
		{Question: "Work authorization", Answer: "No"},
		{Question: "Will you require visa sponsorship?", Answer: "No"},
	}
	result, _ := runQADedupTest(t, questions, func(req types.JevRequest) (types.JevResponse, error) {
		return qaJevResponse(req, map[string]float64{
			"same_question_0_1":      0.99,
			"compatible_answers_0_1": 0.01,
			"same_question_0_2":      0.01,
			"compatible_answers_0_2": 0.99,
			"same_question_1_2":      0.01,
			"compatible_answers_1_2": 0.99,
		}), nil
	})

	if result.Error != "" {
		t.Fatalf("deduplicateQA error = %q", result.Error)
	}
	if len(result.Questions) != len(questions) {
		t.Fatalf("questions = %#v, want distinct/conflicting Q&A preserved", result.Questions)
	}
	for i := range questions {
		if result.Questions[i] != questions[i] {
			t.Errorf("questions[%d] = %#v, want original %#v", i, result.Questions[i], questions[i])
		}
	}
}

func TestDeduplicateQAFallsBackForMalformedOrIncompleteDecisions(t *testing.T) {
	questions := []sqldb.JobApplicationQuestion{
		{Question: "Question one", Answer: "Answer one"},
		{Question: "Question two", Answer: "Answer two"},
	}
	tests := []struct {
		name    string
		answers map[string]types.JevAnswer
	}{
		{
			name: "incomplete",
			answers: map[string]types.JevAnswer{
				"same_question_0_1": {Type: "noul", Noul: qaFloatPointer(0.99)},
			},
		},
		{
			name: "invalid index",
			answers: map[string]types.JevAnswer{
				"same_question_0_2":      {Type: "noul", Noul: qaFloatPointer(0.99)},
				"compatible_answers_0_1": {Type: "noul", Noul: qaFloatPointer(0.99)},
			},
		},
		{
			name: "malformed value",
			answers: map[string]types.JevAnswer{
				"same_question_0_1":      {Type: "choice", Choice: "yes"},
				"compatible_answers_0_1": {Type: "noul", Noul: qaFloatPointer(0.99)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := runQADedupTest(t, questions, func(types.JevRequest) (types.JevResponse, error) {
				return types.JevResponse{Answers: tt.answers}, nil
			})
			if result.Error == "" {
				t.Fatal("deduplicateQA error = empty, want invalid-decision error")
			}
			assertQuestionsEqual(t, result.Questions, questions)
		})
	}
}

func TestValidateQADedupDecisionsRejectsDuplicateOrInvalidIndexes(t *testing.T) {
	pair := qaCandidatePair{first: 0, second: 1}
	answers := map[string]types.JevAnswer{
		qaSameQuestionKey(pair):      {Type: "noul", Noul: qaFloatPointer(0.99)},
		qaCompatibleAnswersKey(pair): {Type: "noul", Noul: qaFloatPointer(0.99)},
	}
	tests := []struct {
		name  string
		pairs []qaCandidatePair
	}{
		{name: "duplicate pair", pairs: []qaCandidatePair{pair, pair}},
		{name: "out of range", pairs: []qaCandidatePair{{first: 0, second: 2}}},
		{name: "reversed indexes", pairs: []qaCandidatePair{{first: 1, second: 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := validateQADedupDecisions(answers, tt.pairs, 2); err == nil {
				t.Fatal("validateQADedupDecisions() error = nil, want validation error")
			}
		})
	}
}

func TestDeduplicateQAFallsBackOnActivityError(t *testing.T) {
	questions := []sqldb.JobApplicationQuestion{
		{Question: "Question one", Answer: "Answer one"},
		{Question: "Question two", Answer: "Answer two"},
	}
	result, _ := runQADedupTest(t, questions, func(types.JevRequest) (types.JevResponse, error) {
		return types.JevResponse{}, errors.New("JEV unavailable")
	})
	if result.Error == "" {
		t.Fatal("deduplicateQA error = empty, want activity error")
	}
	assertQuestionsEqual(t, result.Questions, questions)
}

type qaDedupWorkflowResult struct {
	Questions []sqldb.JobApplicationQuestion
	Error     string
}

func runQADedupTest(t *testing.T, questions []sqldb.JobApplicationQuestion, respond func(types.JevRequest) (types.JevResponse, error)) (qaDedupWorkflowResult, types.JevRequest) {
	t.Helper()
	var received types.JevRequest
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, req types.JevRequest) (types.JevResponse, error) {
		received = req
		return respond(req)
	}, activity.RegisterOptions{Name: "CallJev"})

	env.ExecuteWorkflow(func(ctx workflow.Context) (qaDedupWorkflowResult, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		deduped, err := deduplicateQA(ctx, 17, 29, questions)
		if err != nil {
			return qaDedupWorkflowResult{Questions: questions, Error: err.Error()}, nil
		}
		return qaDedupWorkflowResult{Questions: deduped}, nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var result qaDedupWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get workflow result: %v", err)
	}
	return result, received
}

func qaJevResponse(req types.JevRequest, values map[string]float64) types.JevResponse {
	answers := make(map[string]types.JevAnswer, len(values))
	for key := range req.Questions {
		answers[key] = types.JevAnswer{Type: "noul", Noul: qaFloatPointer(values[key])}
	}
	return types.JevResponse{Answers: answers}
}

func qaFloatPointer(value float64) *float64 {
	return &value
}

func assertQuestionsEqual(t *testing.T, got, want []sqldb.JobApplicationQuestion) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("questions = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("questions[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}
