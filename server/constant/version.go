package constant

// ServerVersion is the current version of the oneclickvirt server.
const ServerVersion = "0.3.0"

// AgentReleaseVersion is the release tag used when downloading the Agent.
// Official builds rewrite ServerVersion to the release tag, so this value
// follows the controller release without conflating it with Agent API
// compatibility.
const AgentReleaseVersion = ServerVersion

// CompatibleAgentVersion is the minimum agent version compatible with this server.
// The version comparison normalises a leading "v" prefix and uses semantic
// versioning rules (agentVersion >= CompatibleAgentVersion).  Older agents
// that predate the version-tracking feature report an empty string and are
// always considered compatible.
const CompatibleAgentVersion = "0.4.0"

const APIContractVersion = "2026-07-29.1"

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

// DisplayVersion returns the version string for display.
// Official builds show the release tag (e.g. v20260511-143022).
// Self-compiled builds append "(unofficial)" to indicate the source.
func DisplayVersion() string {
	if IsOfficialBuild() {
		return ServerVersion
	}
	return ServerVersion + " (unofficial)"
}
