package llm

import (
	"context"

	"github.com/SomtoJF/iris-worker/aipi/types"
)

type Activity struct {
	aipi types.AIPI
}

func NewActivity(aipi types.AIPI) *Activity {
	return &Activity{aipi: aipi}
}

func (a *Activity) CallLLM(ctx context.Context, req types.AIPIRequest) (types.AIPIResponse, error) {
	return a.aipi.GetCompletion(ctx, req)
}
