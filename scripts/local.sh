#!/bin/bash
# Read-only local libvirt/QEMU/LXC capability check for OneClickVirt local mode.
# Reference for local-mode CI/CD environments: run this before enabling the local provider.

set -u

ok=0
warn=0

print_check() {
    local label="$1"
    local status="$2"
    local detail="${3:-}"
    if [ "$status" = "ok" ]; then
        printf "[OK]   %s" "$label"
        ok=$((ok + 1))
    else
        printf "[WARN] %s" "$label"
        warn=$((warn + 1))
    fi
    if [ -n "$detail" ]; then
        printf " - %s" "$detail"
    fi
    printf "\n"
}

check_command() {
    local name="$1"
    if command -v "$name" >/dev/null 2>&1; then
        print_check "$name" ok "$(command -v "$name")"
    else
        print_check "$name" warn "not found"
    fi
}

check_virsh_uri() {
    local label="$1"
    local uri="$2"
    if ! command -v virsh >/dev/null 2>&1; then
        print_check "$label" warn "virsh not found"
        return
    fi
    if timeout 5 virsh -c "$uri" uri >/tmp/ocv-local-check.out 2>&1; then
        print_check "$label" ok "$(tr '\n' ' ' </tmp/ocv-local-check.out | sed 's/[[:space:]]*$//')"
    else
        print_check "$label" warn "$(tr '\n' ' ' </tmp/ocv-local-check.out | sed 's/[[:space:]]*$//')"
    fi
}

echo "OneClickVirt local provider environment check"
echo "This script only detects local virtualization dependencies and does not create resources."
echo

check_command virsh
check_command qemu-img
check_command qemu-system-x86_64
check_command virt-install
check_command lxc-start

if [ -e /dev/kvm ] || [ -e /sys/module/kvm ]; then
    print_check "KVM" ok "device or module detected"
else
    print_check "KVM" warn "not detected; QEMU may use software emulation"
fi

check_virsh_uri "libvirt QEMU" "qemu:///system"
check_virsh_uri "libvirt LXC" "lxc:///"

if [ -f /.dockerenv ] || [ -f /run/.containerenv ]; then
    print_check "container runtime" warn "controller appears to run inside a container; mount host libvirt sockets/devices"
fi

echo
echo "Summary: ${ok} ok, ${warn} warning(s)"
if [ "$warn" -gt 0 ]; then
    exit 1
fi
