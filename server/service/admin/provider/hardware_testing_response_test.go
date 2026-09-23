package provider

import (
	"strings"
	"testing"
)

func TestParsePasteContent(t *testing.T) {
	for _, tc := range []struct {
		name, body, want, wantError string
	}{
		{"legacy", `{"code":0,"data":"report\n"}`, "report\n", ""},
		{"current", `{"code":0,"data":{"shortCode":"abc","content":"report\n"}}`, "report\n", ""},
		{"missing paste", `{"code":7,"data":{},"msg":"粘贴不存在"}`, "", "粘贴不存在"},
		{"empty legacy", `{"code":0,"data":"  "}`, "", "内容为空"},
		{"empty current", `{"code":0,"data":{"content":""}}`, "", "内容为空"},
		{"empty object", `{"code":0,"data":{}}`, "", "内容为空"},
		{"null", `{"code":0,"data":null}`, "", "内容为空"},
		{"wrong type", `{"code":0,"data":42}`, "", "解析粘贴板内容失败"},
		{"wrong content type", `{"code":0,"data":{"content":42}}`, "", "解析粘贴板内容失败"},
		{"malformed", `{`, "", "解析粘贴板API响应失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePasteContent([]byte(tc.body))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error=%v, want %q", err, tc.wantError)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("content=%q, error=%v; want %q", got, err, tc.want)
			}
		})
	}
}
