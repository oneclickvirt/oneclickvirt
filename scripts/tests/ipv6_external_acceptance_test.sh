#!/usr/bin/env bash
# Strict end-to-end IPv6 acceptance test for an Incus/LXD node.
# Public IPv6, a default route, container reachability, and independent
# external SSH/HTTP probes are mandatory. Missing prerequisites are failures.
set -Eeuo pipefail

fail() { echo "IPv6 acceptance FAILED: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"; }
need ssh
need awk
need sed
need grep
need tr
need curl
need cut
need base64

: "${REMOTE_HOST:?set REMOTE_HOST to the node IPv4 or hostname}"
: "${REMOTE_USER:?set REMOTE_USER for node SSH}"
: "${EXTERNAL_PROBE_HOST:?set EXTERNAL_PROBE_HOST to an independent IPv4/IPv6 probe host}"
: "${EXTERNAL_PROBE_USER:?set EXTERNAL_PROBE_USER for probe SSH}"
REMOTE_PORT="${REMOTE_PORT:-22}"
REMOTE_KEY_FILE="${REMOTE_KEY_FILE:-}"
REMOTE_PASS="${REMOTE_PASS:-}"
REMOTE_KNOWN_HOSTS="${REMOTE_KNOWN_HOSTS:-${HOME}/.ssh/known_hosts}"
EXTERNAL_PROBE_PORT="${EXTERNAL_PROBE_PORT:-22}"
EXTERNAL_PROBE_KEY_FILE="${EXTERNAL_PROBE_KEY_FILE:-}"
EXTERNAL_PROBE_PASS="${EXTERNAL_PROBE_PASS:-}"
EXTERNAL_PROBE_KNOWN_HOSTS="${EXTERNAL_PROBE_KNOWN_HOSTS:-${HOME}/.ssh/known_hosts}"
CONTAINER_IMAGE="${CONTAINER_IMAGE:-images:debian/12}"
CONTAINER_NAME="${CONTAINER_NAME:-ocv-ipv6-acceptance-$(date +%s)-$$}"
TEST_RUN_ID="ocv-ipv6-$(date +%s)-$$-$RANDOM"
CONTAINER_CREATED=false
PROBE_KNOWN_HOSTS=""
TEST_PORT="${TEST_PORT:-18080}"
CONTAINER_SSH_PASSWORD="${CONTAINER_SSH_PASSWORD:-ocv-ipv6-test-$(date +%s)-$$}"

[[ "$REMOTE_PORT" =~ ^[0-9]+$ ]] || fail "invalid REMOTE_PORT"
[[ "$EXTERNAL_PROBE_PORT" =~ ^[0-9]+$ ]] || fail "invalid EXTERNAL_PROBE_PORT"
[[ "$TEST_PORT" =~ ^[0-9]+$ ]] || fail "invalid TEST_PORT"
[[ "$CONTAINER_SSH_PASSWORD" != *$'\n'* && "$CONTAINER_SSH_PASSWORD" != *$'\r'* ]] || fail "CONTAINER_SSH_PASSWORD contains a newline"

ssh_run() {
    local host="$1" user="$2" port="$3" key="$4" pass="$5" family="$6"
    shift 6
    local -a family_args=()
    [[ -n "$family" ]] && family_args+=("$family")
    local known_hosts="$REMOTE_KNOWN_HOSTS"
    if [[ "$host" == "$EXTERNAL_PROBE_HOST" && "$port" == "$EXTERNAL_PROBE_PORT" ]]; then
        known_hosts="$EXTERNAL_PROBE_KNOWN_HOSTS"
    fi
    [[ -f "$known_hosts" ]] || fail "known_hosts file does not exist: $known_hosts"
    local -a host_key_args=( -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$known_hosts" -o ConnectTimeout=15 )
    if [[ -n "$pass" && -z "$key" ]]; then
        need sshpass
        SSHPASS="$pass" sshpass -e ssh "${family_args[@]}" "${host_key_args[@]}" -p "$port" "$user@$host" "$@"
    elif [[ -n "$key" ]]; then
        ssh "${family_args[@]}" -o BatchMode=yes "${host_key_args[@]}" -i "$key" -p "$port" "$user@$host" "$@"
    else
        ssh "${family_args[@]}" -o BatchMode=yes "${host_key_args[@]}" -p "$port" "$user@$host" "$@"
    fi
}

node_ssh() {
    ssh_run "$REMOTE_HOST" "$REMOTE_USER" "$REMOTE_PORT" "$REMOTE_KEY_FILE" "$REMOTE_PASS" -4 "$@"
}

probe_ssh() {
    ssh_run "$EXTERNAL_PROBE_HOST" "$EXTERNAL_PROBE_USER" "$EXTERNAL_PROBE_PORT" "$EXTERNAL_PROBE_KEY_FILE" "$EXTERNAL_PROBE_PASS" "" "$@"
}

probe_exec() {
    local command="$1"
    local command_quoted
    # OpenSSH joins command arguments into a remote shell string. Preserve
    # the complete command as the single argument consumed by bash -lc.
    command_quoted="$(shell_quote "$command")"
    probe_ssh "bash -lc $command_quoted"
}

shell_quote() {
    local value="$1"
    value=${value//\'/\'\\\'\'}
    printf "'%s'" "$value"
}

cleanup() {
    if [[ -n "${PROBE_KNOWN_HOSTS:-}" ]]; then
        local probe_known_hosts_quoted
        probe_known_hosts_quoted="$(shell_quote "$PROBE_KNOWN_HOSTS")"
        probe_exec "rm -f -- $probe_known_hosts_quoted" >/dev/null 2>&1 || true
    fi
    [[ "$CONTAINER_CREATED" == true ]] || return 0
    node_ssh "sh -s" <<EOF
set -eu
runtime=incus
command -v "\$runtime" >/dev/null 2>&1 || runtime=lxc
names=\$("\$runtime" list --format csv -c n)
printf '%s\n' "\$names" | grep -Fx -- $(shell_quote "$CONTAINER_NAME") >/dev/null || exit 0
owner=\$("\$runtime" config get $(shell_quote "$CONTAINER_NAME") user.ocv.ipv6-test 2>/dev/null)
if [ "\$owner" != $(shell_quote "$TEST_RUN_ID") ]; then
    echo 'Refusing to remove IPv6 test container: ownership changed' >&2
    exit 1
fi
"\$runtime" delete -f $(shell_quote "$CONTAINER_NAME")
EOF
}
on_exit() {
    local status=$?
    trap - EXIT
    if ! cleanup; then
        echo "IPv6 acceptance FAILED: cleanup did not complete for $CONTAINER_NAME" >&2
        exit 1
    fi
    exit "$status"
}
trap on_exit EXIT

container_name_quoted="$(shell_quote "$CONTAINER_NAME")"
container_image_quoted="$(shell_quote "$CONTAINER_IMAGE")"
test_run_id_quoted="$(shell_quote "$TEST_RUN_ID")"
container_password_quoted="$(shell_quote "$CONTAINER_SSH_PASSWORD")"
test_port_quoted="$(shell_quote "$TEST_PORT")"

host_probe="$(node_ssh "sh -s" <<'EOF'
set -eu
addr=$(ip -6 -o addr show scope global | awk "{print \$4}" | cut -d/ -f1 | grep -Ev "^(fc|fd|fe80:)" | head -n1 || true)
route=$(ip -6 route show default | head -n1 || true)
printf "%s\n%s\n" "$addr" "$route"
EOF
)" || fail "cannot inspect node IPv6 state"
host_ipv6="$(printf '%s\n' "$host_probe" | sed -n '1p')"
host_route="$(printf '%s\n' "$host_probe" | sed -n '2p')"
[[ "$host_ipv6" =~ ^[0-9A-Fa-f:]+$ ]] || fail "node has no public global IPv6 address"
[[ -n "$host_route" ]] || fail "node has no IPv6 default route"

node_ssh "CONTAINER_IMAGE=$container_image_quoted CONTAINER_NAME=$container_name_quoted TEST_RUN_ID=$test_run_id_quoted sh -s" <<'EOF' || fail "failed to create acceptance container"
set -eu
runtime=incus
command -v "$runtime" >/dev/null 2>&1 || runtime=lxc
"$runtime" init "$CONTAINER_IMAGE" "$CONTAINER_NAME" -c "user.ocv.ipv6-test=$TEST_RUN_ID" >/dev/null
EOF
CONTAINER_CREATED=true
node_ssh "CONTAINER_NAME=$container_name_quoted sh -s" <<'EOF' || fail "failed to start acceptance container"
set -eu
runtime=incus
command -v "$runtime" >/dev/null 2>&1 || runtime=lxc
"$runtime" start "$CONTAINER_NAME" >/dev/null
EOF

container_ipv6_script="$(mktemp "${TMPDIR:-/tmp}/ocv-ipv6-probe.XXXXXX")"
cat > "$container_ipv6_script" <<'EOF'
set -eu
runtime=incus
command -v "$runtime" >/dev/null 2>&1 || runtime=lxc
for i in $(seq 1 60); do
    # `-c 6` selects the runtime's IPv6 address column. Reading `-c 4`
    # here would only inspect IPv4 and can never prove public IPv6 reachability.
    ip=$("$runtime" list "$CONTAINER_NAME" --format csv -c 6 | tr "," "\n" | awk "/:/{print \$1}" | grep -Ev "^(fc|fd|fe80:)" | head -n1 || true)
    [ -n "$ip" ] && printf "%s\n" "$ip" && exit 0
    sleep 1
done
exit 1
EOF
container_ipv6="$(node_ssh "CONTAINER_NAME=$container_name_quoted sh -s" < "$container_ipv6_script")" || {
    rm -f -- "$container_ipv6_script"
    fail "container did not receive a public IPv6 address"
}
rm -f -- "$container_ipv6_script"
[[ "$container_ipv6" =~ ^[0-9A-Fa-f:]+$ ]] || fail "invalid container IPv6 address: $container_ipv6"

node_ssh "CONTAINER_NAME=$container_name_quoted CONTAINER_SSH_PASSWORD=$container_password_quoted TEST_PORT=$test_port_quoted TEST_RUN_ID=$test_run_id_quoted sh -s" <<'EOF' || fail "failed to start container SSH/HTTP services"
set -eu
runtime=incus
command -v "$runtime" >/dev/null 2>&1 || runtime=lxc
"$runtime" exec "$CONTAINER_NAME" -- env CONTAINER_SSH_PASSWORD="$CONTAINER_SSH_PASSWORD" TEST_PORT="$TEST_PORT" TEST_RUN_ID="$TEST_RUN_ID" sh -s <<'CONTAINER_SCRIPT'
set -eu
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3 openssh-server >/dev/null
printf "root:%s\n" "$CONTAINER_SSH_PASSWORD" | chpasswd
mkdir -p /etc/ssh/sshd_config.d /tmp/ocv-ipv6-http
printf "%s\n" "PermitRootLogin yes" "PasswordAuthentication yes" > /etc/ssh/sshd_config.d/00-ocv-ipv6-test.conf
systemctl restart ssh
printf "%s\n" "$TEST_RUN_ID" > /tmp/ocv-ipv6-http/identity.txt
systemd-run --unit=ocv-ipv6-http --collect /usr/bin/python3 -m http.server "$TEST_PORT" --bind :: --directory /tmp/ocv-ipv6-http
ready=false
for attempt in $(seq 1 20); do
    if python3 -c "import sys,urllib.request; urllib.request.build_opener(urllib.request.ProxyHandler({})).open(\"http://[::1]:\" + sys.argv[1] + \"/identity.txt\", timeout=2).read()" "$TEST_PORT" >/dev/null; then
        ready=true
        break
    fi
    sleep 1
done
[ "$ready" = true ]
CONTAINER_SCRIPT
EOF

container_keys_command='for key in /etc/ssh/ssh_host_*_key.pub; do test -r "$key" && cat -- "$key"; done'
container_keys_command_quoted="$(shell_quote "$container_keys_command")"
container_host_keys="$(node_ssh "CONTAINER_NAME=$container_name_quoted sh -c $container_keys_command_quoted")" || fail "failed to read container SSH host keys"
[[ -n "$container_host_keys" ]] || fail "container exposed no SSH host public key"

url="http://[$container_ipv6]:$TEST_PORT/identity.txt"
url_quoted="$(shell_quote "$url")"
http_identity="$(probe_exec "curl -6 --noproxy '*' -fsS --connect-timeout 10 --max-time 20 $url_quoted")" || fail "external IPv6 HTTP probe could not reach $url"
[[ "$http_identity" == "$TEST_RUN_ID" ]] || fail "external HTTP reached the wrong container"
PROBE_KNOWN_HOSTS="/tmp/$TEST_RUN_ID-known_hosts"
container_ssh_host="[$container_ipv6]:22"
known_hosts_content=""
while IFS= read -r host_key; do
    [[ -n "$host_key" ]] || continue
    known_hosts_content+="$container_ssh_host $host_key"
    known_hosts_content+=$'\n'
done <<< "$container_host_keys"
host_key_payload="$(printf '%s' "$known_hosts_content" | base64 | tr -d '\n')"
host_key_payload_quoted="$(shell_quote "$host_key_payload")"
probe_known_hosts_quoted="$(shell_quote "$PROBE_KNOWN_HOSTS")"
install_probe_keys_command="printf '%s' $host_key_payload_quoted | base64 -d > $probe_known_hosts_quoted && chmod 600 $probe_known_hosts_quoted"
probe_exec "$install_probe_keys_command" || fail "failed to install pinned container host keys on external probe"
ssh_target="root@$container_ipv6"
ssh_password_quoted="$(shell_quote "$CONTAINER_SSH_PASSWORD")"
ssh_known_hosts_quoted="$(shell_quote "$PROBE_KNOWN_HOSTS")"
ssh_target_quoted="$(shell_quote "$ssh_target")"
ssh_probe_command="command -v sshpass >/dev/null 2>&1 && SSHPASS=$ssh_password_quoted sshpass -e ssh -6 -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$ssh_known_hosts_quoted -o ConnectTimeout=15 $ssh_target_quoted cat /tmp/ocv-ipv6-http/identity.txt"
ssh_identity="$(probe_exec "$ssh_probe_command")" || fail "external IPv6 SSH probe could not log in to [$container_ipv6]"
[[ "$ssh_identity" == "$TEST_RUN_ID" ]] || fail "external SSH reached the wrong container"

cleanup || fail "cleanup did not complete for $CONTAINER_NAME"
CONTAINER_CREATED=false
echo "IPv6 acceptance passed: host=$host_ipv6 container=$container_ipv6 ssh=ok http=ok probe=$EXTERNAL_PROBE_HOST"
