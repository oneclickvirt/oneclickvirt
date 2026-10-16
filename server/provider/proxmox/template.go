package proxmox

import (
	"context"
	_ "embed"
	"fmt"

	"oneclickvirt/utils"
)

//go:embed unprivileged_template.py
var unprivilegedTemplateHelper string

func (p *ProxmoxProvider) prepareUnprivilegedTemplate(ctx context.Context, path string) error {
	command := "python3 - " + utils.ShellSingleQuote(path) + " <<'OCV_TEMPLATE_PY'\n" + unprivilegedTemplateHelper + "\nOCV_TEMPLATE_PY\n"
	if _, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, command); err != nil {
		return fmt.Errorf("准备非特权PVE容器模板失败: %w", err)
	}
	return nil
}
