package initiateapplication

import (
	"testing"

	"github.com/SomtoJF/iris-worker/aipi/types"
)

func TestJevNoulDecisionIsYes(t *testing.T) {
	tests := []struct {
		name    string
		answers map[string]types.JevAnswer
		want    bool
		wantErr bool
	}{
		{
			name: "above threshold",
			answers: map[string]types.JevAnswer{
				"valid": {Type: "noul", Noul: floatPointer(0.8)},
			},
			want: true,
		},
		{
			name: "below threshold",
			answers: map[string]types.JevAnswer{
				"valid": {Type: "noul", Noul: floatPointer(0.49)},
			},
			want: false,
		},
		{
			name: "wrong answer type",
			answers: map[string]types.JevAnswer{
				"valid": {Type: "choice", Choice: "yes"},
			},
			wantErr: true,
		},
		{name: "missing answer", answers: map[string]types.JevAnswer{}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := jevNoulDecisionIsYes(test.answers, "valid", 0.5)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("decision = %v, want %v", got, test.want)
			}
		})
	}
}

func floatPointer(value float64) *float64 {
	return &value
}
