package incus

import (
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func incusStartNeedsCloudInitTemplateRepair(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "failed to read template file") &&
		strings.Contains(lower, "templates")
}

func (i *IncusProvider) ensureVMCloudInitTemplates(instanceName string) error {
	script := fmt.Sprintf(`set -eu
name=%s
itype="$(incus info "$name" 2>/dev/null | awk -F': ' '/^Type:/{print tolower($2); exit}' || true)"
is_vm=0
case "$itype" in
  virtual-machine|vm) is_vm=1 ;;
esac
for base in /var/lib/incus /var/snap/incus/common/incus; do
  dir="$base/virtual-machines/$name"
  [ -d "$dir" ] && is_vm=1
done
[ "$is_vm" = "1" ] || exit 0
for base in /var/lib/incus /var/snap/incus/common/incus; do
  dir="$base/virtual-machines/$name"
  [ -d "$dir" ] || continue
  mkdir -p "$dir/templates"
  for tpl in hostname.tpl hosts.tpl cloud-init-vendor-data.tpl cloud-init-user-data.tpl cloud-init-network-config.tpl cloud-init-network-data.tpl cloud-init-meta-data.tpl; do
    [ -e "$dir/templates/$tpl" ] || : > "$dir/templates/$tpl"
  done
done
`, shellSingleQuote(instanceName))

	output, err := i.sshClient.Execute(script)
	if err != nil {
		return fmt.Errorf("修复Incus VM cloud-init模板失败: %w; output: %s", err, strings.TrimSpace(output))
	}
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		global.APP_LOG.Debug("Incus VM cloud-init模板检查输出",
			zap.String("instance", instanceName),
			zap.String("output", utils.TruncateString(trimmed, 1000)))
	}
	return nil
}
