package constant

// ServerVersion is the source-build fallback. Official controller builds use
// the release workflow to replace this value with a vYYYYMMDD-HHMMSS tag.
// It must stay independent from the latest public release because this source
// tree may contain changes that have not been released yet.
const ServerVersion = "0.3.0"

// AgentReleaseVersion is the controller release tag containing the Agent
// assets. It is separate from CompatibleAgentVersion: the latter is the
// Agent's API compatibility version and intentionally uses Cargo's semver.
const AgentReleaseVersion = ServerVersion

// CompatibleAgentVersion is the minimum agent version compatible with this server.
// This value follows server/agent/Cargo.toml's semver and is independent of
// the controller's timestamp release tags. Older agents that predate version
// reporting use an empty string and remain compatible for backward compatibility.
const CompatibleAgentVersion = "0.4.0"

const APIContractVersion = "2026-08-16.1"

// Build verification - these are set at compile time via ldflags in CI/CD
// Official builds will have these set; unofficial builds will show "unofficial"
var (
	BuildCommit    = "unofficial" // Git commit hash
	BuildTime      = "unofficial" // Build timestamp
	BuildSignature = "unofficial" // Official build signature (set by CI/CD)
)

// IsOfficialBuild checks if this is an official build from CI/CD
func IsOfficialBuild() bool {
	return BuildSignature != "unofficial" && BuildSignature != ""
}

// IsReleaseVersion reports whether value follows the controller's published
// release format. CI validation images may carry a build signature while
// using labels such as validator-<id>; those labels must remain visibly
// separate from a published timestamp release.
func IsReleaseVersion(value string) bool {
	if len(value) != 16 || value[0] != 'v' || value[9] != '-' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if index == 9 {
			continue
		}
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

// DisplayVersion returns the version string for display.
// Official builds show the release tag (e.g. v20260511-143022).
// Self-compiled builds append "(unofficial)" to indicate the source.
func DisplayVersion() string {
	if IsOfficialBuild() && IsReleaseVersion(ServerVersion) {
		return ServerVersion
	}
	return ServerVersion + " (unofficial)"
}
