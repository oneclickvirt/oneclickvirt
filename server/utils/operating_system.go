package utils

import "strings"

// NormalizeOSType converts image OS names and aliases into a stable canonical value.
// Unknown custom values are preserved as a lower-case, delimiter-normalized string so
// administrators can still manage uncommon systems without being blocked by validation.
func NormalizeOSType(osType string) string {
	value := normalizeOSInput(osType)
	if value == "" {
		return ""
	}
	if canonical := matchKnownOSType(value); canonical != "" {
		return canonical
	}
	return value
}

// DetectOSTypeFromText extracts a known OS type from an image name or URL.
func DetectOSTypeFromText(text string) string {
	value := normalizeOSInput(text)
	if value == "" {
		return ""
	}
	if canonical := matchKnownOSType(value); canonical != "" {
		return canonical
	}
	return ""
}

func normalizeOSInput(input string) string {
	value := strings.ToLower(strings.TrimSpace(input))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"%20", "-",
		"_", "-",
		".", "-",
		" ", "-",
		"/", "-",
		"\\", "-",
		":", "-",
	)
	value = replacer.Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}

func matchKnownOSType(value string) string {
	aliases := []struct {
		patterns  []string
		canonical string
	}{
		{[]string{"windows-server", "windows", "windows10", "windows11", "winserver", "win", "win-10", "win-11", "win10", "win11", "winos"}, "windows"},
		{[]string{"macos", "mac-os", "osx", "darwin"}, "macos"},
		{[]string{"android", "android-x86", "androidx86", "redroid", "blissos", "bliss-os"}, "android"},
		{[]string{"archlinux", "arch-linux", "arch"}, "archlinux"},
		{[]string{"rockylinux", "rocky-linux", "rocky"}, "rockylinux"},
		{[]string{"almalinux", "alma-linux", "alma"}, "almalinux"},
		{[]string{"openeuler", "open-euler"}, "openeuler"},
		{[]string{"openwrt", "open-wrt"}, "openwrt"},
		{[]string{"opensuse", "open-suse", "sles", "suse-linux-enterprise", "suse"}, "opensuse"},
		{[]string{"oraclelinux", "oracle-linux", "oracle"}, "oracle"},
		{[]string{"ubuntu"}, "ubuntu"},
		{[]string{"debian"}, "debian"},
		{[]string{"alpinelinux", "alpine-linux", "alpine"}, "alpine"},
		{[]string{"fedora"}, "fedora"},
		{[]string{"centos", "cent-os"}, "centos"},
		{[]string{"gentoo"}, "gentoo"},
		{[]string{"kali"}, "kali"},
		{[]string{"freebsd", "free-bsd"}, "freebsd"},
		{[]string{"openbsd", "open-bsd"}, "openbsd"},
		{[]string{"netbsd", "net-bsd"}, "netbsd"},
	}
	for _, alias := range aliases {
		for _, pattern := range alias.patterns {
			if matchesOSPattern(value, pattern) {
				return alias.canonical
			}
		}
	}
	return ""
}

func matchesOSPattern(value, pattern string) bool {
	if value == pattern || strings.HasPrefix(value, pattern+"-") || strings.Contains(value, "-"+pattern+"-") || strings.HasSuffix(value, "-"+pattern) {
		return true
	}

	// Several published qcow2 images use compact names such as debian12,
	// ubuntu24 and almalinux9. Accept a version digit immediately after a
	// known OS token while retaining the existing token-boundary checks so
	// unrelated words that merely contain an OS alias are not classified.
	for start := 0; start < len(value); {
		index := strings.Index(value[start:], pattern)
		if index < 0 {
			return false
		}
		index += start
		beforeBoundary := index == 0 || value[index-1] == '-'
		after := index + len(pattern)
		if beforeBoundary && after < len(value) && value[after] >= '0' && value[after] <= '9' {
			return true
		}
		start = index + 1
	}
	return false
}
