package utils

// IPv6KeepaliveInstallCommand keeps the optional IPv6 neighbor/path refresh in
// its own cron.d file. Never pipe a single job into crontab: that replaces every
// unrelated root job and races with concurrent instance creation.
func IPv6KeepaliveInstallCommand() string {
	return `set -eu
command -v curl >/dev/null
command -v flock >/dev/null
test -d /etc/cron.d
test ! -L /etc/cron.d
mkdir -p /run/lock
test ! -L /run/lock
exec 9>/run/lock/oneclickvirt-ipv6-keepalive.lock
flock -x -w 10 9
target=/etc/cron.d/oneclickvirt-ipv6-keepalive
temporary=$(mktemp /etc/cron.d/.oneclickvirt-ipv6-keepalive.XXXXXX)
trap 'rm -f -- "$temporary"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
printf '%s\n' \
  '# Managed by OneClickVirt: IPv6 path keepalive' \
  'SHELL=/bin/sh' \
  'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
  "*/1 * * * * root curl --noproxy '*' -6 -fsS --connect-timeout 6 --max-time 6 https://ipv6.ip.sb >/dev/null 2>&1 && curl --noproxy '*' -6 -fsS --connect-timeout 6 --max-time 6 https://ipv6.ip.sb >/dev/null 2>&1" >"$temporary"
if [ -e "$target" ] || [ -L "$target" ]; then
  if [ -L "$target" ] || [ ! -f "$target" ] || ! cmp -s "$temporary" "$target"; then
    printf 'Refusing to replace an existing custom IPv6 keepalive file\n' >&2
    exit 1
  fi
  exit 0
fi
chmod 0644 "$temporary"
mv -- "$temporary" "$target"
`
}
