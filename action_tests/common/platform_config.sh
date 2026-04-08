#!/bin/bash
# Platform Configuration - Central config for all cloud platform providers
# Edit this file to enable/disable platforms, set priority order, and configure behavior.

# ============================================================================
# Platform Enable/Disable (set to "true" to enable)
# Each platform requires its own set of secrets to be configured in GitHub Actions.
# ============================================================================
PLATFORM_ALICE_ENABLED="${PLATFORM_ALICE_ENABLED:-true}"
PLATFORM_LIGHTNODE_ENABLED="${PLATFORM_LIGHTNODE_ENABLED:-false}"
PLATFORM_RACKDOG_ENABLED="${PLATFORM_RACKDOG_ENABLED:-true}"
PLATFORM_SKRIME_ENABLED="${PLATFORM_SKRIME_ENABLED:-false}"
PLATFORM_PREPAIDHOST_ENABLED="${PLATFORM_PREPAIDHOST_ENABLED:-false}"
PLATFORM_CUBEPATH_ENABLED="${PLATFORM_CUBEPATH_ENABLED:-false}"
PLATFORM_VULTR_ENABLED="${PLATFORM_VULTR_ENABLED:-false}"
PLATFORM_HETZNER_ENABLED="${PLATFORM_HETZNER_ENABLED:-false}"
PLATFORM_LINODE_ENABLED="${PLATFORM_LINODE_ENABLED:-false}"
PLATFORM_CLOUDSIGMA_ENABLED="${PLATFORM_CLOUDSIGMA_ENABLED:-false}"

# ============================================================================
# Platform Priority Order (space-separated, first = highest priority)
# Auto-fallback: if the first enabled platform fails, try the next one.
# ============================================================================
PLATFORM_PRIORITY_ORDER="${PLATFORM_PRIORITY_ORDER:-alice lightnode vultr hetzner linode rackdog cubepath skrime prepaidhost cloudsigma}"

# ============================================================================
# Instance Lifecycle Settings
# ============================================================================
# Whether to delete test instances after test completion.
# When set to "true", instances are NEVER deleted regardless of platform billing type.
# Instead, to achieve a clean state for the next run, the existing instance's OS
# will be reinstalled (if the platform supports it).
# Monthly/prepaid platforms (skrime, prepaidhost) also default to this behavior.
SKIP_INSTANCE_DELETE="${SKIP_INSTANCE_DELETE:-false}"

# ============================================================================
# Platform Billing Types (hourly = safe to delete, monthly/prepaid = prefer reinstall)
# ============================================================================
declare -A PLATFORM_BILLING_TYPE=(
    [alice]="hourly"
    [lightnode]="hourly"
    [rackdog]="hourly"
    [skrime]="monthly"
    [prepaidhost]="prepaid"
    [cubepath]="hourly"
    [vultr]="hourly"
    [hetzner]="hourly"
    [linode]="hourly"
    [cloudsigma]="hourly"
)

# ============================================================================
# Platform Authentication Methods
# Possible values: ssh_key, root_password, non_root_password, ssh_key_or_password
# ============================================================================
declare -A PLATFORM_AUTH_METHOD=(
    [alice]="ssh_key"
    [lightnode]="ssh_key_or_password"
    [rackdog]="ssh_key_or_password"
    [skrime]="root_password"
    [prepaidhost]="root_password"
    [cubepath]="ssh_key_or_password"
    [vultr]="ssh_key_or_password"
    [hetzner]="ssh_key"
    [linode]="ssh_key_or_password"
    [cloudsigma]="ssh_key_or_password"
)

# ============================================================================
# Platform SSH Users (root vs non-root)
# ============================================================================
declare -A PLATFORM_SSH_USER=(
    [alice]="root"
    [lightnode]="root"
    [rackdog]="root"
    [skrime]="root"
    [prepaidhost]="root"
    [cubepath]="root"
    [vultr]="root"
    [hetzner]="root"
    [linode]="root"
    [cloudsigma]="cloudsigma"
)

# ============================================================================
# Platform Reinstall Support
# ============================================================================
declare -A PLATFORM_SUPPORTS_REINSTALL=(
    [alice]="false"
    [lightnode]="true"
    [rackdog]="true"
    [skrime]="true"
    [prepaidhost]="true"
    [cubepath]="true"
    [vultr]="true"
    [hetzner]="true"
    [linode]="true"
    [cloudsigma]="true"
)

# ============================================================================
# Shared SSH settings - used by all platforms for SSH connections
# ============================================================================
PLATFORM_SSH_KEY_FILE=""
PLATFORM_SSH_PASSWORD=""

# ============================================================================
# Helper: get ordered list of enabled platforms
# ============================================================================
get_enabled_platforms() {
    local result=()
    for p in ${PLATFORM_PRIORITY_ORDER}; do
        local var_name="PLATFORM_${p^^}_ENABLED"
        if [[ "${!var_name:-false}" == "true" ]]; then
            result+=("$p")
        fi
    done
    echo "${result[*]}"
}

# Helper: check if a specific platform is enabled
is_platform_enabled() {
    local name="$1"
    local var_name="PLATFORM_${name^^}_ENABLED"
    [[ "${!var_name:-false}" == "true" ]]
}

# Helper: should we skip instance deletion for this platform?
should_skip_delete() {
    local platform="$1"
    if [[ "${SKIP_INSTANCE_DELETE}" == "true" ]]; then
        return 0
    fi
    local billing="${PLATFORM_BILLING_TYPE[$platform]:-hourly}"
    if [[ "$billing" == "monthly" || "$billing" == "prepaid" ]]; then
        return 0
    fi
    return 1
}

# Helper: should we reinstall instead of creating a new instance?
# Returns 0 (true) when: deletion is skipped (SKIP_INSTANCE_DELETE=true or monthly/prepaid)
# AND the platform supports OS reinstall. No separate PREFER_REINSTALL flag needed.
should_reinstall() {
    local platform="$1"
    if should_skip_delete "$platform"; then
        local supports="${PLATFORM_SUPPORTS_REINSTALL[$platform]:-false}"
        [[ "$supports" == "true" ]]
        return $?
    fi
    return 1
}

# Helper: get SSH user for a platform
get_platform_ssh_user() {
    local platform="$1"
    echo "${PLATFORM_SSH_USER[$platform]:-root}"
}

# Helper: get auth method for a platform
get_platform_auth_method() {
    local platform="$1"
    echo "${PLATFORM_AUTH_METHOD[$platform]:-ssh_key}"
}
