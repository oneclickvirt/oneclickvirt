#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

source <(sed '/^main "\$@"$/d' "$ROOT_DIR/scripts/install.sh")
FAKE_BIN="$TMP_DIR/bin"
mkdir -p "$FAKE_BIN"

cat > "$FAKE_BIN/curl" <<'SCRIPT'
#!/usr/bin/env bash
out=""
prev=""
for arg in "$@"; do
    [[ "$prev" == "-o" ]] && out="$arg"
    prev="$arg"
done
if [[ -n "$out" ]]; then
    printf 'partial-but-invalid' > "$out"
    exit 7
fi
exit 0
SCRIPT
cat > "$FAKE_BIN/wget" <<'SCRIPT'
#!/usr/bin/env bash
out=""
prev=""
for arg in "$@"; do
    [[ "$prev" == "-O" ]] && out="$arg"
    prev="$arg"
done
[[ -n "$out" ]] && printf 'partial-but-invalid' > "$out"
exit 8
SCRIPT
chmod +x "$FAKE_BIN/curl" "$FAKE_BIN/wget"
export PATH="$FAKE_BIN:$PATH"
hash -r 2>/dev/null || true

target="$TMP_DIR/result"
if download_file https://example.invalid "$target" >/dev/null 2>&1; then
    echo 'failed download unexpectedly succeeded' >&2
    exit 1
fi
[[ ! -e "$target" ]] || { echo 'failed download left a published output' >&2; exit 1; }
if compgen -G "$target.part.*" >/dev/null; then
    echo 'failed download left a temporary file' >&2
    exit 1
fi

printf 'previous-valid-content' > "$target"
if download_file https://example.invalid "$target" >/dev/null 2>&1; then
    echo 'failed replacement download unexpectedly succeeded' >&2
    exit 1
fi
[[ "$(cat "$target")" == previous-valid-content ]] || {
    echo 'failed replacement download destroyed the previous output' >&2
    exit 1
}

echo 'atomic download failure tests passed'
