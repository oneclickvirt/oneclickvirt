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
    as_root "$@"
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
            [ "$SKIP_UPDATE" = "true" ] || run apt-get update -y
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-utils libvirt-daemon-system libvirt-clients virtinst bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates uidmap)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run apt-get install -y "${packages[@]}"
            ;;
        dnf)
            [ "$SKIP_UPDATE" = "true" ] || run dnf makecache -y
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-img libvirt libvirt-daemon-kvm virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run dnf install -y "${packages[@]}"
            ;;
        yum)
            [ "$SKIP_UPDATE" = "true" ] || run yum makecache -y
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-img libvirt libvirt-daemon-kvm virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run yum install -y "${packages[@]}"
            ;;
        zypper)
            [ "$SKIP_UPDATE" = "true" ] || run zypper --non-interactive refresh
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-kvm qemu-tools libvirt libvirt-client virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxcfs)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run zypper --non-interactive install -y "${packages[@]}"
            ;;
        pacman)
            [ "$SKIP_UPDATE" = "true" ] || run pacman -Sy --noconfirm
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-base qemu-img libvirt virt-install bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxcfs)
            [ "${#packages[@]}" -gt 0 ] || { log "no packages selected"; return 0; }
            run pacman -S --needed --noconfirm "${packages[@]}"
            ;;
        apk)
            [ "$SKIP_UPDATE" = "true" ] || run apk update
            [ "$INSTALL_QEMU" = "true" ] && packages+=(qemu-system-x86_64 qemu-img libvirt libvirt-client bridge-utils dnsmasq)
            [ "$INSTALL_LXC" = "true" ] && packages+=(lxc lxc-templates)
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

enable_services() {
    [ "$START_SERVICES" = "true" ] || return 0
    if have_cmd systemctl; then
        for svc in libvirtd virtqemud virtlogd virtlockd lxc-net; do
            if systemctl list-unit-files "$svc.service" >/dev/null 2>&1; then
                run systemctl enable --now "$svc.service" || true
            fi
        done
    elif have_cmd rc-service; then
        for svc in libvirtd virtqemud lxc; do
            if rc-service "$svc" status >/dev/null 2>&1; then
                run rc-update add "$svc" default || true
                run rc-service "$svc" start || true
            fi
        done
    else
        log "service manager not detected; start libvirt/LXC services manually if needed"
    fi
}

main() {
    local pm
    if ! pm="$(detect_pm)"; then
        log "no supported package manager found"
        exit 1
    fi
    log "package manager: $pm"
    install_packages "$pm"
    enable_services
    log "installation step completed; run: bash scripts/local.sh"
}

main "$@"
