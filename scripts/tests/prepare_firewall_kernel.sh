#!/usr/bin/env bash
set -euo pipefail

# Run on the disposable Linux CI runner hosting Docker, never inside the Debian
# test container. Installing iptables there does not provide host kernel modules.
# Loading modules does not flush host rules or require a public IPv6 route.
if [[ "$(uname -s)" != Linux ]]; then
    echo "Firewall kernel preparation must run on the Linux Docker daemon host" >&2
    exit 1
fi
command -v modprobe >/dev/null || { echo "Install kmod on the Docker daemon host" >&2; exit 1; }
for module in nf_tables iptable_filter iptable_nat ip6table_filter ip6table_nat xt_comment; do
    if ! modprobe "$module"; then
        echo "Unable to load ${module}; install the modules matching the running host kernel ($(uname -r)) before firewall tests" >&2
        exit 1
    fi
done
