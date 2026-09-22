package utils

import "fmt"

// BuildRemoteDownloadScript returns a self-contained POSIX shell script that downloads
// a URL to a temporary path, validates the result, then atomically moves it to
// the destination path. It forces curl to HTTP/1.1 first because some CDN paths
// intermittently fail with curl exit 92 over HTTP/2.
func BuildRemoteDownloadScript(url, tmpPath, dstPath string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true
export PATH=%s${PATH:+:$PATH}

	url=%s
	tmp_base=%s
	dst=%s

log() {
  printf '[download] %%s\n' "$*" >&2
}

	mkdir -p "$(dirname "$dst")"
# Every invocation gets its own remote temporary file.  A fixed dst.tmp
# allows concurrent image/script downloads to remove or publish one another's
# partial content before the final atomic rename.
tmp="$(mktemp "${tmp_base}.XXXXXX")"
finish() {
  rc=$?
  if [ "$rc" -ne 0 ]; then
    printf 'TEMP_SCRIPT_FAILED\n' > "${MARKER_FILE:-$0.marker}" 2>/dev/null || true
  fi
  if [ -n "${tmp:-}" ]; then
    rm -f -- "$tmp" 2>/dev/null || true
  fi
  trap - 0
  exit "$rc"
}
trap finish 0
	rm -f -- "$tmp"
last_rc=1

try_curl() {
  mode="$1"
  command -v curl >/dev/null 2>&1 || return 127
  rm -f "$tmp"
  if [ "$mode" = "ipv4" ]; then
    curl -4 -fL --http1.1 --connect-timeout 30 --max-time 900 --retry 5 --retry-delay 10 --retry-connrefused --speed-time 120 --speed-limit 1024 -o "$tmp" "$url"
  else
    curl -fL --http1.1 --connect-timeout 30 --max-time 900 --retry 5 --retry-delay 10 --retry-connrefused --speed-time 120 --speed-limit 1024 -o "$tmp" "$url"
  fi
}

try_wget() {
  mode="$1"
  command -v wget >/dev/null 2>&1 || return 127
  rm -f "$tmp"
  if [ "$mode" = "ipv4" ]; then
    wget -4 -O "$tmp" --timeout=30 --tries=5 --waitretry=10 "$url"
  else
    wget -O "$tmp" --timeout=30 --tries=5 --waitretry=10 "$url"
  fi
}

for method in curl-ipv4 wget-ipv4 curl-any wget-any; do
  log "trying $method"
  method_rc=0
  case "$method" in
    curl-ipv4) try_curl ipv4 || method_rc=$? ;;
    wget-ipv4) try_wget ipv4 || method_rc=$? ;;
    curl-any) try_curl any || method_rc=$? ;;
    wget-any) try_wget any || method_rc=$? ;;
  esac
  if [ "$method_rc" -eq 0 ] && [ -s "$tmp" ]; then
    log "$method succeeded"
    last_rc=0
    break
  elif [ "$method_rc" -eq 0 ]; then
    log "$method produced an empty file"
    last_rc=1
  else
    last_rc=$method_rc
    log "$method failed with exit code $last_rc"
  fi
done

if [ ! -s "$tmp" ]; then
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
