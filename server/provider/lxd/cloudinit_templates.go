package lxd

import (
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func lxdStartNeedsCloudInitTemplateRepair(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "failed to read template file") &&
		strings.Contains(lower, "cloud-init") &&
		strings.Contains(lower, "templates")
}

func (l *LXDProvider) ensureVMCloudInitTemplates(instanceName string) error {
	script := fmt.Sprintf(`set -eu
name=%s
itype="$(lxc info "$name" 2>/dev/null | awk -F': ' '/^Type:/{print tolower($2); exit}' || true)"
case "$itype" in
  virtual-machine|vm) ;;
  *) exit 0 ;;
esac
for base in /var/snap/lxd/common/lxd /var/lib/lxd; do
  dir="$base/virtual-machines/$name"
  [ -d "$dir" ] || continue
  mkdir -p "$dir/templates"
  for tpl in cloud-init-vendor-data.tpl cloud-init-user-data.tpl cloud-init-network-config.tpl cloud-init-meta-data.tpl; do
    [ -e "$dir/templates/$tpl" ] || : > "$dir/templates/$tpl"
  done
done
`, shellSingleQuote(instanceName))

	output, err := l.sshClient.Execute(script)
	if err != nil {
		return fmt.Errorf("修复LXD VM cloud-init模板失败: %w; output: %s", err, strings.TrimSpace(output))
	}
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		global.APP_LOG.Debug("LXD VM cloud-init模板检查输出",
			zap.String("instance", instanceName),
			zap.String("output", utils.TruncateString(trimmed, 1000)))
	}
	return nil
}
