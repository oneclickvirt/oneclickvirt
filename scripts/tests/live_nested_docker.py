"""Actual nested Docker acceptance in a disposable Debian/Ubuntu guest."""
import secrets
import shlex


def nested_docker_command(marker):
    # Install the official current engine/runc, not an old distribution package
    # that might predate the AppArmor/sysctl regression being investigated.
    return r'''set -eu
. /etc/os-release
case "$ID" in debian|ubuntu) ;; *) echo 'Nested Docker acceptance requires Debian/Ubuntu' >&2; exit 1;; esac
test -n "${VERSION_CODENAME:-}"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg
install -d -m 0755 /etc/apt/keyrings
curl -fsSL --connect-timeout 15 --max-time 90 "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/ocv-docker.asc
gpg --batch --show-keys --with-colons /etc/apt/keyrings/ocv-docker.asc | awk -F: '$1 == "fpr" && $10 == "9DC858229FC7DD38854AE2D88D81803C0EBFCD88" {found=1} END {exit !found}'
chmod 0644 /etc/apt/keyrings/ocv-docker.asc
printf 'deb [arch=%s signed-by=/etc/apt/keyrings/ocv-docker.asc] https://download.docker.com/linux/%s %s stable\n' "$(dpkg --print-architecture)" "$ID" "$VERSION_CODENAME" > /etc/apt/sources.list.d/ocv-docker.list
apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io
systemctl enable --now docker
docker version --format '{{json .Server}}'
docker info --format 'StorageDriver={{.Driver}} CgroupVersion={{.CgroupVersion}}'
docker run --pull=always --rm --sysctl net.ipv4.ip_unprivileged_port_start=0 hello-world
printf '%s\n' ''' + shlex.quote(marker)


def verify_nested_docker(guest, collect_output):
    marker = "OCV_NESTED_DOCKER_OK_" + secrets.token_hex(16)
    stdin, stdout, _ = guest.exec_command(nested_docker_command(marker), timeout=900)
    stdin.channel.shutdown_write()
    try:
        output, error, status = collect_output(stdout.channel, 900)
    finally:
        stdout.channel.close()
    if status or marker not in output.splitlines() or "Hello from Docker!" not in output:
        raise RuntimeError(f"nested Docker failed ({status}): " + (output + error)[-8000:])
    return "\n".join(line for line in output.splitlines()
                     if line.startswith(("{", "StorageDriver=", "Hello from Docker!")))
