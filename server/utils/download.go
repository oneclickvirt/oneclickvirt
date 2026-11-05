package utils

import (
	"fmt"
	"strings"
)

// BuildRemoteDownloadScript returns a self-contained POSIX shell script that downloads
// a URL to a temporary path, validates the result, then atomically moves it to the
// destination path. It forces curl to HTTP/1.1 first because some CDN paths
// intermittently fail with curl exit 92 over HTTP/2.
func BuildRemoteDownloadScript(url, tmpPath, dstPath string) string {
	return BuildRemoteDownloadScriptWithFallback([]string{url}, tmpPath, dstPath)
}

// BuildRemoteDownloadScriptWithFallback tries each URL in order. This keeps the
// node mirror as the first candidate while preserving the caller's existing CDN
// and upstream fallbacks when a mirror is unavailable.
func BuildRemoteDownloadScriptWithFallback(urls []string, tmpPath, dstPath string) string {
	candidates := make([]string, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	for _, value := range urls {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}
	if len(candidates) == 0 {
		candidates = []string{""}
	}
	var candidateShell strings.Builder
	for index, candidate := range candidates {
		if index > 0 {
			candidateShell.WriteByte(' ')
		}
		candidateShell.WriteString(ShellSingleQuote(candidate))
	}

	return fmt.Sprintf(`#!/bin/sh
set -eu
export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true
export PATH=%s${PATH:+:$PATH}

	 tmp_base=%s
	 dst=%s
mirror_prefix=%s

log() {
  printf '[download] %%s\n' "$*" >&2
}

mkdir -p "$(dirname "$dst")"
# Every invocation gets its own remote temporary file. A fixed dst.tmp
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
    curl -4 -fL --http1.1 --connect-timeout "$connect_timeout" --max-time "$max_time" --retry "$retries" --retry-delay 10 --retry-connrefused --speed-time "$idle_timeout" --speed-limit 1024 -o "$tmp" "$url"
  else
    curl -fL --http1.1 --connect-timeout "$connect_timeout" --max-time "$max_time" --retry "$retries" --retry-delay 10 --retry-connrefused --speed-time "$idle_timeout" --speed-limit 1024 -o "$tmp" "$url"
  fi
}

try_wget() {
  mode="$1"
  command -v wget >/dev/null 2>&1 || return 127
  rm -f "$tmp"
  if [ "$mode" = "ipv4" ]; then
    wget -4 -O "$tmp" --timeout="$connect_timeout" --tries="$tries" --waitretry=10 "$url"
  else
    wget -O "$tmp" --timeout="$connect_timeout" --tries="$tries" --waitretry=10 "$url"
  fi
}

for url in %s; do
  [ -n "$url" ] || continue
  log "trying URL $url"
  connect_timeout=30 retries=5 tries=5 idle_timeout=120 max_time=900
  methods='curl-ipv4 wget-ipv4 curl-any wget-any'
  case "$url" in
    "$mirror_prefix"*)
      connect_timeout=${DUFS_MIRROR_CONNECT_TIMEOUT:-5}
      retries=0 tries=1 idle_timeout=${DUFS_MIRROR_IDLE_TIMEOUT:-30} max_time=86400
      methods='curl-any wget-any'
      ;;
  esac
  url_ok=1
  for method in $methods; do
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
      url_ok=0
      break
    elif [ "$method_rc" -eq 0 ]; then
      log "$method produced an empty file"
    else
      last_rc=$method_rc
      log "$method failed with exit code $last_rc"
    fi
  done
  if [ "$url_ok" -eq 0 ]; then
    if head -c 512 "$tmp" | grep -Eiq '<html|<!doctype'; then
      log "downloaded file appears to be an HTML/error page"
      rm -f "$tmp"
      last_rc=22
      continue
    fi
    case "$dst" in
      *.tar.gz|*.tgz) gzip -t "$tmp" || { last_rc=$?; rm -f "$tmp"; continue; } ;;
      *.tar) tar -tf "$tmp" >/dev/null || { last_rc=$?; rm -f "$tmp"; continue; } ;;
      *.zip) if command -v unzip >/dev/null 2>&1; then unzip -t "$tmp" >/dev/null || { last_rc=$?; rm -f "$tmp"; continue; }; fi ;;
    esac
    mv -f "$tmp" "$dst"
    echo TEMP_SCRIPT_OK > "${MARKER_FILE:-$0.marker}"
    log "saved to $dst"
    exit 0
  fi
done

log "all download URLs and methods failed"
exit "$last_rc"
`, shellQuote(StandardExtendedPath), ShellSingleQuote(tmpPath), ShellSingleQuote(dstPath), ShellSingleQuote(imageMirrorBaseURL()+"/"), candidateShell.String())
}
