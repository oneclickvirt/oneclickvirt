#!/usr/bin/env bash
# This script runs only on a disposable GitHub-hosted runner.
set -euo pipefail
[[ "${GITHUB_ACTIONS:-}" == true && "${RUNNER_ENVIRONMENT:-}" == github-hosted ]] || {
    echo 'Use a disposable GitHub-hosted runner for runtime provisioning.' >&2
    exit 1
}
name=ocv-mcp-integration
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
wrapper="$work/runtime"
readonly_mode=()
case "$INTEGRATION_RUNTIME" in
  docker)
    docker create --name "$name" --label oneclickvirt.integration-test=true alpine:3.23 sh -c 'trap "exit 0" TERM; while true; do sleep 1; done'
    printf '#!/bin/sh\nexec docker "$@"\n' > "$wrapper"
    ;;
  podman)
    sudo apt-get update -qq
    sudo apt-get install -y podman
    sudo podman create --name "$name" docker.io/library/alpine:3.23 sh -c 'trap "exit 0" TERM; while true; do sleep 1; done'
    printf '#!/bin/sh\nexec sudo podman "$@"\n' > "$wrapper"
    ;;
  containerd)
    curl -fLsS --retry 3 https://github.com/containerd/nerdctl/releases/download/v2.4.1/nerdctl-2.4.1-linux-amd64.tar.gz -o "$work/nerdctl.tar.gz"
    echo "f6bbf02fd8a299cbcbc71a0bad113c1b63b3f33689bbb7d51b0fa98c6d361d5e  $work/nerdctl.tar.gz" | sha256sum -c -
    tar -xzf "$work/nerdctl.tar.gz" -C "$work" nerdctl
    sudo systemctl start containerd
    sudo "$work/nerdctl" --namespace ocv-mcp-integration create --net none --name "$name" docker.io/library/alpine:3.23 sh -c 'trap "exit 0" TERM; while true; do sleep 1; done'
    printf '#!/bin/sh\nexec sudo "%s/nerdctl" --namespace ocv-mcp-integration "$@"\n' "$work" > "$wrapper"
    ;;
  incus)
    sudo apt-get update -qq
    sudo apt-get install -y incus
    sudo incus admin init --auto
    sudo incus init images:alpine/3.23 "$name"
    printf '#!/bin/sh\nexec sudo incus "$@"\n' > "$wrapper"
    ;;
  lxd)
    sudo snap install lxd --channel=5.21/stable
    sudo /snap/bin/lxd init --auto
    sudo /snap/bin/lxc init ubuntu-minimal:24.04 "$name"
    printf '#!/bin/sh\nexec sudo /snap/bin/lxc "$@"\n' > "$wrapper"
    ;;
  qemu)
    sudo apt-get update -qq
    sudo apt-get install -y qemu-system-x86 libvirt-daemon-system libvirt-clients
    sudo systemctl start libvirtd
    cat > "$work/domain.xml" <<EOF
<domain type='qemu'><name>$name</name><memory unit='MiB'>64</memory><vcpu>1</vcpu><os><type arch='x86_64'>hvm</type></os><devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices></domain>
EOF
    sudo virsh define "$work/domain.xml"
    printf '#!/bin/sh\nexec sudo virsh "$@"\n' > "$wrapper"
    readonly_mode=(--readonly)
    ;;
  pve)
    docker run -d --name "$name" --hostname pve-integration --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw spiritlhl/pve:9
    for attempt in $(seq 1 90); do
      if docker exec "$name" pvesh get /cluster/resources --type vm --output-format json > /dev/null 2>&1; then break; fi
      sleep 2
    done
    docker exec "$name" qm create 901 --name ocv-mcp-test --memory 64 --cores 1
    printf '#!/bin/sh\nexec docker exec %s pvesh "$@"\n' "$name" > "$wrapper"
    name=901
    readonly_mode=(--readonly)
    ;;
  kubevirt)
    curl -fLsS --retry 3 https://github.com/kubernetes-sigs/kind/releases/download/v0.33.0/kind-linux-amd64 -o "$work/kind"
    echo "aee6151561422756b764a4ae28e7f44cda5af5a9eead3cc9985112b1de8d8e0d  $work/kind" | sha256sum -c -
    chmod +x "$work/kind"
    "$work/kind" create cluster --name ocv-mcp-integration --wait 180s
    kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.9.1/kubevirt-operator.yaml
    kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.9.1/kubevirt-cr.yaml
    kubectl -n kubevirt wait kv kubevirt --for=condition=Available --timeout=420s
    cat <<EOF | kubectl apply -f -
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: $name
spec:
  runStrategy: Halted
  template:
    metadata:
      labels:
        kubevirt.io/domain: $name
    spec:
      domain:
        resources:
          requests:
            memory: 64Mi
EOF
    printf '#!/bin/sh\nexec kubectl "$@"\n' > "$wrapper"
    readonly_mode=(--readonly)
    ;;
  *) exit 1 ;;
esac
chmod +x "$wrapper"
python integrations/runtime_smoke.py --runtime "$INTEGRATION_RUNTIME" --id "$name" --binary "$wrapper" "${readonly_mode[@]}"
