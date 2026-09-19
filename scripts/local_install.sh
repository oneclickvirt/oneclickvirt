#!/usr/bin/env bash
# Install local libvirt/QEMU/LXC dependencies for OneClickVirt local mode.
# Usage:
#   DRY_RUN=true bash scripts/local_install.sh
#   INSTALL_LXC=false bash scripts/local_install.sh

set -euo pipefail

DRY_RUN="${DRY_RUN:-false}"
INSTALL_QEMU="${INSTALL_QEMU:-true}"
INSTALL_LXC="${INSTALL_LXC:-true}"
SKIP_UPDATE="${SKIP_UPDATE:-false}"
START_SERVICES="${START_SERVICES:-true}"

log() {
    printf '[ocv-local-install] %s\n' "$*"
}

have_cmd() {
    command -v "$1" >/dev/null 2>&1
}

as_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    elif have_cmd sudo; then
        sudo "$@"
    else
        log "root privileges or sudo are required: $*"
        exit 1
    fi
}

run() {
    log "+ $*"
    if [ "$DRY_RUN" = "true" ]; then
        return 0
    fi
    if [ "$1" = "brew" ]; then
        "$@"
    else
        as_root "$@"
    fi
}

detect_pm() {
    for pm in apt-get dnf yum zypper pacman apk brew; do
        if have_cmd "$pm"; then
            printf '%s\n' "$pm"
            return 0
        fi
    done
    return 1
}

install_packages() {
    local pm="$1"
    local packages=()

    case "$pm" in
        apt-get)
            [ "$SKIP_UPDATE" = "true" ] || run apt-get update -y || return 1
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-utils libvirt-daemon-system libvirt-clients virtinst bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates libvirt-daemon-system libvirt-clients libvirt-daemon-driver-lxc uidmap)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run apt-get install -y "${packages[@]}"
            ;;
        dnf)
            [ "$SKIP_UPDATE" = "true" ] || run dnf makecache -y || return 1
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-img libvirt libvirt-daemon-kvm virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates libvirt-client libvirt-daemon-driver-lxc)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run dnf install -y "${packages[@]}"
            ;;
        yum)
            [ "$SKIP_UPDATE" = "true" ] || run yum makecache -y || return 1
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-img libvirt libvirt-daemon-kvm virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates libvirt-client libvirt-daemon-driver-lxc)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run yum install -y "${packages[@]}"
            ;;
        zypper)
            [ "$SKIP_UPDATE" = "true" ] || run zypper --non-interactive refresh || return 1
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-tools libvirt libvirt-client virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxcfs libvirt libvirt-client)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run zypper --non-interactive install -y "${packages[@]}"
            ;;
        pacman)
            [ "$SKIP_UPDATE" = "true" ] || run pacman -Sy --noconfirm || return 1
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-base qemu-img libvirt virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxcfs libvirt)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run pacman -S --needed --noconfirm "${packages[@]}"
            ;;
        apk)
            [ "$SKIP_UPDATE" = "true" ] || run apk update || return 1
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-system-x86_64 qemu-img libvirt libvirt-client bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates libvirt libvirt-client)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run apk add --no-cache "${packages[@]}"
            ;;
        brew)
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu libvirt virt-manager)
            [ "$INSTALL_LXC" = "true" ] && log "Homebrew does not provide a full Linux LXC host stack; skipping LXC packages."
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run brew install "${packages[@]}"
            ;;
        *)
            log "unsupported package manager: $pm"
            exit 1
            ;;
    esac
}

systemd_unit_exists() {
    local state
    state=$(systemctl show --property=LoadState --value "$1" 2>/dev/null) || return 1
    [ -n "$state" ] && [ "$state" != "not-found" ]
}

enable_systemd_daemon() {
    local daemon="$1"
    if systemd_unit_exists "$daemon.socket"; then
        run systemctl enable --now "$daemon.socket"
    elif systemd_unit_exists "$daemon.service"; then
        run systemctl enable --now "$daemon.service"
    else
        log "no unit found for $daemon"
        return 1
    fi
}

enable_services() {
    [ "$START_SERVICES" = "true" ] || return 0
    [ "$INSTALL_QEMU" = "true" ] || [ "$INSTALL_LXC" = "true" ] || return 0
    if [ "$DRY_RUN" = "true" ]; then
        log "dry run: service discovery will run after packages are installed"
        return 0
    fi
    local svc modular=true
    local drivers=()
    [ "$INSTALL_QEMU" = "true" ] && drivers+=(virtqemud)
    [ "$INSTALL_LXC" = "true" ] && drivers+=(virtlxcd)
    if have_cmd systemctl; then
        # Monolithic and modular libvirt daemons must not be started together.
        # Keep an active monolithic installation, otherwise prefer the selected
        # drivers' socket units when the distribution provides them all.
        if systemctl is-active --quiet libvirtd.service || systemctl is-active --quiet libvirtd.socket; then
            enable_systemd_daemon libvirtd || return 1
            return 0
        fi
        for svc in "${drivers[@]}"; do
            if ! systemd_unit_exists "$svc.socket" && ! systemd_unit_exists "$svc.service"; then
                modular=false
            fi
        done
        if ! $modular; then
            for svc in virtqemud virtlxcd; do
                if systemctl is-active --quiet "$svc.service" || systemctl is-active --quiet "$svc.socket"; then
                    log "modular libvirt is active but a selected driver is missing; install that driver before continuing"
                    return 1
                fi
            done
            enable_systemd_daemon libvirtd || return 1
            return 0
        fi
        for svc in virtlogd virtlockd virtnetworkd virtstoraged virtnodedevd virtnwfilterd virtsecretd; do
            if systemd_unit_exists "$svc.socket" || systemd_unit_exists "$svc.service"; then
                enable_systemd_daemon "$svc" || return 1
            fi
        done
        for svc in "${drivers[@]}"; do
            enable_systemd_daemon "$svc" || return 1
        done
    elif have_cmd rc-service; then
        if rc-service --exists libvirtd; then
            drivers=(libvirtd)
        fi
        for svc in "${drivers[@]}"; do
            rc-service --exists "$svc" || { log "no OpenRC service for $svc"; return 1; }
            run rc-update add "$svc" default || return 1
            run rc-service "$svc" start || return 1
        done
    else
        log "service manager not detected; cannot verify libvirt/LXC runtime"
        return 1
    fi
}

verify_runtime() {
    [ "$DRY_RUN" = "true" ] && return 0
    [ "$START_SERVICES" = "true" ] || return 0
    local output
    if [ "$INSTALL_QEMU" = "true" ]; then
        output="$(as_root virsh -c qemu:///system uri 2>&1)" || {
            log "libvirt QEMU runtime is unavailable: $output"
            return 1
        }
    fi
    if [ "$INSTALL_LXC" = "true" ]; then
        output="$(as_root virsh -c lxc:/// uri 2>&1)" || {
            log "libvirt LXC runtime is unavailable: $output"
            return 1
        }
    fi
}

main() {
    local pm
    if [ "$INSTALL_QEMU" != "true" ] && [ "$INSTALL_LXC" != "true" ]; then
        log "no runtimes selected"
        return 0
    fi
    if ! pm="$(detect_pm)"; then
        log "no supported package manager found"
        exit 1
    fi
    log "package manager: $pm"
    install_packages "$pm" || return 1
    if [ "$pm" = brew ]; then
        log "Homebrew dependencies installed; Linux host services are not configured on macOS"
        return 0
    fi
    enable_services || return 1
    verify_runtime || return 1
    if [ "$START_SERVICES" != "true" ]; then
        log "packages installed; service startup and runtime verification disabled by START_SERVICES"
        return 0
    fi
    log "installation step completed; run: bash scripts/local.sh"
}

main "$@"
