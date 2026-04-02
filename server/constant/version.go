package constant

// ServerVersion is the current version of the oneclickvirt server.
const ServerVersion = "0.2.0"

// CompatibleAgentVersion is the agent version compatible with this server.
// When deploying the agent without specifying a version, this value is used.
// It must match the Cargo.toml version of server/agent.
const CompatibleAgentVersion = "v0.2.0"
