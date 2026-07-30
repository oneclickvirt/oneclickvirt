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

func TestClassifyErrorTreatsCapacityRejectionsAsConflict(t *testing.T) {
	for _, message := range []string{
		"创建任务失败: Provider资源不足: CPU资源不足：需要 1 核，可用 0 核",
		"用户配额不足: 内存配额不足",
		"节点容器数量已达上限：2/2",
	} {
		t.Run(message, func(t *testing.T) {
			err := ClassifyError(errors.New(message))
			if err.Code != CodeConflict {
				t.Fatalf("ClassifyError(%q) code = %d, want %d", message, err.Code, CodeConflict)
			}
			if err.Details != message {
				t.Fatalf("ClassifyError(%q) details = %q", message, err.Details)
			}
		})
	}
}
