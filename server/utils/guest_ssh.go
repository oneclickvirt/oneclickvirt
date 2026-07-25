package utils

// BuildGuestSSHRecoveryScript returns a POSIX shell script that repairs an
// already-installed SSH service inside a guest. The provider-specific caller
// is responsible for executing it through lxc/incus exec. Package installation
// remains the job of the normal image setup script; this fallback covers
// OpenSSH configuration drift and OpenWrt/Dropbear images.
func BuildGuestSSHRecoveryScript() string {
	return `set -u

if [ -x /etc/init.d/dropbear ] || command -v dropbear >/dev/null 2>&1; then
    if command -v uci >/dev/null 2>&1; then
        uci -q set dropbear.@dropbear[0].PasswordAuth='on' || true
        uci -q set dropbear.@dropbear[0].RootPasswordAuth='on' || true
        uci -q commit dropbear || true
    fi
    if [ -x /etc/init.d/dropbear ]; then
        /etc/init.d/dropbear enable >/dev/null 2>&1 || true
        if /etc/init.d/dropbear restart >/dev/null 2>&1 || /etc/init.d/dropbear start >/dev/null 2>&1; then
            echo ONECLICKVIRT_SSH_READY
            exit 0
        fi
    fi
    if command -v dropbear >/dev/null 2>&1; then
        dropbear -R >/dev/null 2>&1 || true
        if command -v pgrep >/dev/null 2>&1 && pgrep -x dropbear >/dev/null 2>&1; then
            echo ONECLICKVIRT_SSH_READY
            exit 0
        fi
    fi
fi

if ! command -v sshd >/dev/null 2>&1; then
    echo "no installed OpenSSH or Dropbear server found" >&2
    exit 127
fi

sshd_config=/etc/ssh/sshd_config
if [ ! -f "$sshd_config" ]; then
    echo "OpenSSH server exists but $sshd_config is missing" >&2
    exit 1
fi

chattr -i "$sshd_config" >/dev/null 2>&1 || true
set_sshd_directive() {
    key="$1"
    value="$2"
    file="$3"
    if grep -Eq "^[[:space:]#]*${key}[[:space:]]" "$file" 2>/dev/null; then
        sed -i "s|^[[:space:]#]*${key}[[:space:]].*|${key} ${value}|g" "$file"
    else
        printf '%s %s\n' "$key" "$value" >> "$file"
    fi
}

set_sshd_directive PermitRootLogin yes "$sshd_config"
set_sshd_directive PasswordAuthentication yes "$sshd_config"
if [ -d /etc/ssh/sshd_config.d ]; then
    for file in /etc/ssh/sshd_config.d/*; do
        [ -f "$file" ] || continue
        chattr -i "$file" >/dev/null 2>&1 || true
        sed -i 's|^[[:space:]]*PasswordAuthentication[[:space:]]\+no.*|PasswordAuthentication yes|g' "$file"
        sed -i 's|^[[:space:]]*PermitRootLogin[[:space:]]\+\(no\|prohibit-password\|without-password\).*|PermitRootLogin yes|g' "$file"
    done
fi

command -v ssh-keygen >/dev/null 2>&1 && ssh-keygen -A >/dev/null 2>&1 || true
sshd -t

started=0
if command -v systemctl >/dev/null 2>&1; then
    systemctl restart sshd >/dev/null 2>&1 || systemctl restart ssh >/dev/null 2>&1 || true
    systemctl is-active --quiet sshd >/dev/null 2>&1 && started=1
    systemctl is-active --quiet ssh >/dev/null 2>&1 && started=1
fi
if [ "$started" -eq 0 ] && command -v rc-service >/dev/null 2>&1; then
    rc-service sshd restart >/dev/null 2>&1 || rc-service ssh restart >/dev/null 2>&1 || true
    rc-service sshd status >/dev/null 2>&1 && started=1
    rc-service ssh status >/dev/null 2>&1 && started=1
fi
if [ "$started" -eq 0 ] && command -v service >/dev/null 2>&1; then
    service sshd restart >/dev/null 2>&1 || service ssh restart >/dev/null 2>&1 || true
    service sshd status >/dev/null 2>&1 && started=1
    service ssh status >/dev/null 2>&1 && started=1
fi
if [ "$started" -eq 0 ]; then
    /usr/sbin/sshd >/dev/null 2>&1 || true
    if command -v pgrep >/dev/null 2>&1 && pgrep -x sshd >/dev/null 2>&1; then
        started=1
    fi
fi

if [ "$started" -ne 1 ]; then
    echo "OpenSSH configuration is valid but the service did not start" >&2
    exit 1
fi

echo ONECLICKVIRT_SSH_READY
`
}
