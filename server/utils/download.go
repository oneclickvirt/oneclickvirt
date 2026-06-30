package utils

import "fmt"

// BuildRemoteDownloadScript returns a self-contained bash script that downloads
// a URL to a temporary path, validates the result, then atomically moves it to
// the destination path. It forces curl to HTTP/1.1 first because some CDN paths
// intermittently fail with curl exit 92 over HTTP/2.
func BuildRemoteDownloadScript(url, tmpPath, dstPath string) string {
	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail
export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true
export PATH=%s${PATH:+:$PATH}

url=%s
tmp=%s
dst=%s

log() {
  printf '[download] %%s\n' "$*" >&2
}

mkdir -p "$(dirname "$dst")"
rm -f "$tmp"
last_rc=1

try_curl() {
  local mode="$1"
  rm -f "$tmp"
  local args=(-fL --http1.1 --connect-timeout 30 --max-time 900 --retry 5 --retry-delay 10 --retry-connrefused --speed-time 120 --speed-limit 1024 -o "$tmp")
  if [[ "$mode" == "ipv4" ]]; then
    args=(-4 "${args[@]}")
  fi
  curl "${args[@]}" "$url"
}

try_wget() {
  local mode="$1"
  command -v wget >/dev/null 2>&1 || return 127
  rm -f "$tmp"
  local args=(-O "$tmp" --timeout=30 --tries=5 --waitretry=10)
  if [[ "$mode" == "ipv4" ]]; then
    args=(-4 "${args[@]}")
  fi
  wget "${args[@]}" "$url"
}

for method in curl-ipv4 wget-ipv4 curl-any wget-any; do
  log "trying $method"
  case "$method" in
    curl-ipv4) try_curl ipv4 ;;
    wget-ipv4) try_wget ipv4 ;;
    curl-any) try_curl any ;;
    wget-any) try_wget any ;;
  esac && {
    if [[ -s "$tmp" ]]; then
      log "$method succeeded"
      last_rc=0
      break
    fi
    log "$method produced an empty file"
    last_rc=1
  } || {
    last_rc=$?
    log "$method failed with exit code $last_rc"
  }
done

if [[ ! -s "$tmp" ]]; then
  log "all download methods failed"
  exit "$last_rc"
fi

if head -c 512 "$tmp" | grep -Eiq '<html|<!doctype'; then
  log "downloaded file appears to be an HTML/error page"
  exit 22
fi

case "$dst" in
  *.tar.gz|*.tgz)
    gzip -t "$tmp"
    ;;
  *.tar)
    tar -tf "$tmp" >/dev/null
    ;;
  *.zip)
    if command -v unzip >/dev/null 2>&1; then
      unzip -t "$tmp" >/dev/null
    fi
    ;;
esac

mv -f "$tmp" "$dst"
echo TEMP_SCRIPT_OK > "${MARKER_FILE:-$0.marker}"
log "saved to $dst"
`, shellQuote(StandardExtendedPath), ShellSingleQuote(url), ShellSingleQuote(tmpPath), ShellSingleQuote(dstPath))
}
