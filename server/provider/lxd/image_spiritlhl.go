package lxd

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (l *LXDProvider) spiritlhlLocalAlias(imageName, instanceType string) string {
	base := strings.TrimSpace(imageName)
	if base == "" {
		return ""
	}
	base = strings.TrimPrefix(base, "local:")
	base = strings.TrimPrefix(base, "images:")
	base = strings.TrimPrefix(base, "spiritlhl:")
	base = strings.TrimPrefix(base, "oneclickvirt_")
	if idx := strings.Index(base, "_container_"); idx > 0 {
		base = base[:idx]
	}
	if idx := strings.Index(base, "_vm_"); idx > 0 {
		base = base[:idx]
	}
	base = strings.ReplaceAll(base, "/", "-")
	base = strings.ReplaceAll(base, ":", "-")
	base = sanitizeLockKey(base)
	if base == "" || base == "unknown" {
		return ""
	}
	arch := sanitizeLockKey(l.getCurrentArchitecture())
	return fmt.Sprintf("oneclickvirt_%s_%s_%s-spiritlhl", base, instanceType, arch)
}

func (l *LXDProvider) formatImageContext(config provider.InstanceConfig, requestedImage string) string {
	parts := []string{"provider=lxd"}
	if config.Name != "" {
		parts = append(parts, "instance="+config.Name)
	}
	if config.InstanceType != "" {
		parts = append(parts, "instanceType="+config.InstanceType)
	}
	if requestedImage != "" {
		parts = append(parts, "requestedImage="+requestedImage)
	}
	if config.Image != "" {
		label := "image"
		if requestedImage != "" && requestedImage != config.Image {
			label = "imageAlias"
		}
		parts = append(parts, label+"="+config.Image)
	}
	if config.ImageURL != "" {
		parts = append(parts, "imageURL="+utils.TruncateString(config.ImageURL, 300))
	}
	if config.UseCDN {
		parts = append(parts, "useCDN=true")
	}
	return strings.Join(parts, ", ")
}

func (l *LXDProvider) ensureSpiritlhlRemote() error {
	checkCmd := "lxc remote list --format csv 2>/dev/null | awk -F, '$1==\"spiritlhl\"{found=1} END{exit !found}'"
	if _, err := l.sshClient.Execute(checkCmd); err == nil {
		return nil
	}
	cmd := "lxc remote add spiritlhl https://lxdimages.spiritlhl.net --protocol simplestreams --public"
	output, err := l.sshClient.Execute(cmd)
	if err == nil {
		return nil
	}
	// 如果名称存在但配置损坏或 URL 不对，重建一次 remote。
	rebuildCmd := "lxc remote remove spiritlhl >/dev/null 2>&1 || true; lxc remote add spiritlhl https://lxdimages.spiritlhl.net --protocol simplestreams --public"
	output, err = l.sshClient.Execute(rebuildCmd)
	if err != nil {
		return fmt.Errorf("添加spiritlhl LXD远程镜像源失败: %w (output: %s)", err, utils.TruncateString(output, 300))
	}
	return nil
}

func (l *LXDProvider) copySpiritlhlImageToLocal(imageName, targetAlias, instanceType string) error {
	if strings.TrimSpace(targetAlias) == "" {
		return fmt.Errorf("目标镜像alias为空")
	}
	if l.imageExists(targetAlias) {
		return nil
	}
	candidates := buildSpiritlhlImageCandidates(imageName)
	if len(candidates) == 0 {
		return fmt.Errorf("无法从镜像名生成spiritlhl候选路径: %s", imageName)
	}
	if err := l.ensureSpiritlhlRemote(); err != nil {
		return err
	}
	if err := l.copySpiritlhlImageCandidates(targetAlias, instanceType, candidates); err == nil {
		return nil
	}
	// remote 可能存在但指向旧地址或协议不对，重建一次再重试。
	_, _ = l.sshClient.Execute("lxc remote remove spiritlhl >/dev/null 2>&1 || true; lxc remote add spiritlhl https://lxdimages.spiritlhl.net --protocol simplestreams --public")
	return l.copySpiritlhlImageCandidates(targetAlias, instanceType, candidates)
}

func (l *LXDProvider) copySpiritlhlImageCandidates(targetAlias, instanceType string, candidates []string) error {
	var lastErr error
	for _, candidate := range candidates {
		source := "spiritlhl:" + candidate
		vmFlag := ""
		if instanceType == "vm" {
			vmFlag = " --vm"
		}
		cmd := fmt.Sprintf("lxc image alias delete %s >/dev/null 2>&1 || true; lxc image copy %s local: --alias %s%s --auto-update=false", shellSingleQuote(targetAlias), shellSingleQuote(source), shellSingleQuote(targetAlias), vmFlag)
		output, err := l.sshClient.ExecuteWithTimeout(cmd, 1*time.Hour)
		if err == nil && l.imageExists(targetAlias) {
			global.APP_LOG.Info("已复制spiritlhl LXD镜像到本地", zap.String("source", source), zap.String("alias", utils.TruncateString(targetAlias, 100)), zap.String("type", instanceType))
			return nil
		}
		if l.imageExists(targetAlias) {
			return nil
		}
		errText := output
		if err != nil {
			errText += "\n" + err.Error()
		}
		if strings.Contains(strings.ToLower(errText), "fingerprint") {
			if fp := extractFingerprintFromOutput(errText); fp != "" {
				if aliasErr := l.ensureImageAliasFromFingerprint(targetAlias, fp); aliasErr == nil {
					return nil
				}
			}
			if fp, _ := l.findImageFingerprintByURLAliasSuffix(targetAlias, instanceType); fp != "" {
				if aliasErr := l.ensureImageAliasFromFingerprint(targetAlias, fp); aliasErr == nil {
					return nil
				}
			}
		}
		lastErr = fmt.Errorf("复制候选镜像 %s 失败: %v (output: %s)", source, err, utils.TruncateString(output, 1200))
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的spiritlhl候选镜像")
	}
	return lastErr
}

func buildSpiritlhlImageCandidates(imageName string) []string {
	osName, version, variant := parseSpiritlhlImageName(imageName)
	if osName == "" {
		return nil
	}
	variants := uniqueNonEmpty([]string{variant, "cloud", "default"})
	var out []string
	if version != "" {
		for _, v := range variants {
			out = append(out, fmt.Sprintf("%s/%s/%s", osName, version, v))
		}
	}
	// archlinux 在部分 simplestreams 源里也可能有 archlinux/cloud 这种别名。
	if osName == "archlinux" || osName == "arch" {
		out = append(out, "archlinux/cloud")
	}
	return uniqueNonEmpty(out)
}

func parseSpiritlhlImageName(imageName string) (string, string, string) {
	s := strings.ToLower(strings.TrimSpace(imageName))
	for _, p := range []string{"local:", "images:", "spiritlhl:", "oneclickvirt_", "spiritlhl_"} {
		s = strings.TrimPrefix(s, p)
	}
	if idx := strings.Index(s, "_container_"); idx > 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, "_vm_"); idx > 0 {
		s = s[:idx]
	}
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.')
	})
	if len(tokens) == 0 {
		return "", "", ""
	}
	knownOS := map[string]bool{
		"almalinux": true, "alpine": true, "archlinux": true, "arch": true, "centos": true,
		"debian": true, "fedora": true, "gentoo": true, "kali": true, "openeuler": true,
		"opensuse": true, "oracle": true, "oraclelinux": true, "rockylinux": true, "ubuntu": true, "openwrt": true,
	}
	variants := map[string]bool{"cloud": true, "default": true, "openrc": true, "systemd": true}
	archTokens := map[string]bool{"amd64": true, "x86": true, "x86_64": true, "arm64": true, "aarch64": true, "container": true, "vm": true, "kvm": true}
	osName := ""
	osIdx := -1
	for idx, t := range tokens {
		if knownOS[t] {
			osName = t
			osIdx = idx
			break
		}
		for known := range knownOS {
			if strings.HasPrefix(t, known) && len(t) > len(known) {
				osName = known
				osIdx = idx
				tokens = append(tokens[:idx+1], append([]string{strings.TrimPrefix(t, known)}, tokens[idx+1:]...)...)
				break
			}
		}
		if osName != "" {
			break
		}
	}
	if osName == "arch" {
		osName = "archlinux"
	}
	if osName == "oraclelinux" {
		osName = "oracle"
	}
	if osName == "" {
		return "", "", ""
	}
	variant := "cloud"
	for _, t := range tokens {
		if variants[t] {
			variant = t
			break
		}
	}
	version := ""
	for _, t := range tokens[osIdx+1:] {
		if t == "" || archTokens[t] || variants[t] || strings.HasPrefix(t, "sha256") {
			continue
		}
		version = t
		break
	}
	if version == "" {
		switch osName {
		case "archlinux", "gentoo":
			version = "current"
		case "kali":
			version = "latest"
		}
	}
	return osName, version, variant
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sanitizeLockKey(s string) string {
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}
