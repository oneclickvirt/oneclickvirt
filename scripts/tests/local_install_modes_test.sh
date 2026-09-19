#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
source <(sed '/^main "\$@"$/d' "$repo_root/scripts/local_install.sh")
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
log() { :; }

(
    DRY_RUN=true
    have_cmd() { fail 'dry run must not require uninstalled service units'; }
    as_root() { fail 'dry run must not execute host commands'; }
    run apt-get install example || fail 'dry run package plan failed'
    enable_services && verify_runtime || fail 'dry run should succeed before packages exist'
)
(
    START_SERVICES=false
    have_cmd() { fail 'package-only mode must not probe services'; }
    as_root() { fail 'package-only mode must not probe libvirt'; }
    enable_services && verify_runtime || fail 'package-only mode should succeed with stopped daemons'
)
(
    INSTALL_QEMU=false INSTALL_LXC=false
    detect_pm() { fail 'no selection must not require a package manager'; }
    main || fail 'empty selection should be a no-op'
)
(
    detect_pm() { printf '%s\n' brew; }
    brew() { [[ "$1" == install ]] || fail 'unexpected brew operation'; }
    as_root() { fail 'Homebrew must not run as root through sudo'; }
    enable_services() { fail 'macOS dependency installation must not start Linux services'; }
    verify_runtime() { fail 'macOS must not be checked as a Linux LXC host'; }
    main || fail 'Homebrew dependency installation failed'
)
(
    INSTALL_QEMU=false INSTALL_LXC=true SKIP_UPDATE=true
    package_args=()
    run() { package_args+=("$@"); }
    install_packages apt-get || fail 'LXC-only package install failed'
    for required in libvirt-daemon-system libvirt-clients libvirt-daemon-driver-lxc; do
        [[ " ${package_args[*]} " == *" $required "* ]] || fail "LXC-only install omitted $required"
    done
)
(
    SKIP_UPDATE=false
    run() {
        [[ "$*" == 'apt-get update -y' ]] || fail 'failed update must stop before installation'
        return 100
    }
    if install_packages apt-get; then fail 'package index failure must propagate'; fi
)

mock_units=$'libvirtd.service\nvirtqemud.socket\nvirtlxcd.socket\nvirtlogd.socket'
mock_active="" mock_failed_unit="" mock_started=()
have_cmd() { [[ "$1" == systemctl ]]; }
systemctl() {
    case "$*" in
        'show --property=LoadState --value '*)
            if grep -Fxq "$4" <<<"$mock_units"; then printf 'loaded\n'; else printf 'not-found\n'; fi ;;
        'is-active --quiet '*) grep -Fxq "$3" <<<"$mock_active" ;;
        *) fail "Unexpected systemctl: $*" ;;
    esac
}
run() {
    [[ "$1 $2 $3" == 'systemctl enable --now' ]] || fail "Unexpected service operation: $*"
    mock_started+=("$4")
    [[ "$4" != "$mock_failed_unit" ]]
}
(
    mock_active=libvirtd.service
    enable_services || fail 'active monolithic libvirt should be retained'
    [[ "${mock_started[*]}" == libvirtd.service ]] || fail 'monolithic and modular daemons must not start together'
)
(
    enable_services || fail 'modular libvirt should start through socket units'
    [[ "${mock_started[*]}" == 'virtlogd.socket virtqemud.socket virtlxcd.socket' ]] || fail 'wrong modular service selection'
)
(
    INSTALL_QEMU=false INSTALL_LXC=true
    enable_services || fail 'LXC-only modular libvirt should start'
    [[ "${mock_started[*]}" == 'virtlogd.socket virtlxcd.socket' ]] || fail 'LXC-only install started QEMU'
)
(
    mock_units=libvirtd.service
    enable_services || fail 'distribution with only monolithic libvirt must remain supported'
    [[ "${mock_started[*]}" == libvirtd.service ]] || fail 'monolithic fallback did not start'
)
(
    mock_units=$'libvirtd.service\nvirtqemud.socket' mock_active=virtqemud.socket
    if enable_services; then fail 'missing modular LXC driver must be reported'; fi
    [[ "${#mock_started[@]}" == 0 ]] || fail 'must not start monolithic libvirt beside active modular libvirt'
)
(
    mock_failed_unit=virtqemud.socket
    if enable_services; then fail 'selected driver startup failure must propagate'; fi
)
(
    mock_units=""
    if enable_services; then fail 'missing runtime services must fail'; fi
)
(
    have_cmd() { [[ "$1" == rc-service ]]; }
    rc-service() { [[ "$*" == '--exists libvirtd' ]]; }
    run() { mock_started+=("$*"); }
    enable_services || fail 'OpenRC must support installed but stopped libvirtd'
    [[ "${mock_started[*]}" == 'rc-update add libvirtd default rc-service libvirtd start' ]] || fail 'wrong OpenRC startup'
)
(
    as_root() { return 1; }
    if verify_runtime; then fail 'unreachable libvirt after startup must fail'; fi
)
(
    INSTALL_QEMU=false INSTALL_LXC=true
    as_root() { [[ "$*" == 'virsh -c lxc:/// uri' ]]; }
    verify_runtime || fail 'runtime verification must honor LXC-only selection'
)
printf 'Local installation modes and libvirt startup passed (16 scenarios)\n'
