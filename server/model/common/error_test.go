package common

import (
	"errors"
	"testing"
)

func TestClassifyErrorTreatsSelectionRequirementsAsBadRequest(t *testing.T) {
	for _, message := range []string{
		"必须选择至少一条端口映射",
		"至少选择一个Provider",
	} {
		t.Run(message, func(t *testing.T) {
			err := ClassifyError(errors.New(message))
			if err.Code != CodeBadRequest {
				t.Fatalf("ClassifyError(%q) code = %d, want %d", message, err.Code, CodeBadRequest)
			}
			if err.Details != message {
				t.Fatalf("ClassifyError(%q) details = %q", message, err.Details)
			}
		})
	}
}
