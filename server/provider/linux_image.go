package provider

import (
	"regexp"
	"strings"
)

var legacyLinuxImage = regexp.MustCompile(`(?i)^(almalinux|alpine|centos|debian|fedora|openeuler|opensuse|oraclelinux|oracle|rockylinux|ubuntu)[-_]?([0-9]+(\.[0-9]+)*)$`)
var quotedMissingImage = regexp.MustCompile(`(?i)\bimage\s+("[^"\r\n]+"|'[^'\r\n]+')\s+not found\b`)

// NormalizeLinuxImageAlias recognizes the versioned names used by the script
// installers while leaving explicit remotes, fingerprints and custom aliases intact.
func NormalizeLinuxImageAlias(name string) string {
	parts := legacyLinuxImage.FindStringSubmatch(strings.TrimSpace(name))
	if parts == nil {
		return name
	}
	return strings.ToLower(parts[1]) + ":" + parts[2]
}

// RuntimeImageNotFound covers both older CLI errors and the newer quoted-name form.
func RuntimeImageNotFound(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "failed to find image") ||
		strings.Contains(lower, "image not found") ||
		strings.Contains(lower, "no such image") || quotedMissingImage.MatchString(message)
}
