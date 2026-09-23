#!/usr/bin/env bash
# Emit a history-free public commit without changing the checkout or its index.
# No network access or ref updates occur here; the caller decides where to push.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
source_commit=$(git rev-parse --verify "${1:-HEAD}^{commit}")
snapshot_tmp=$(mktemp -d)
trap 'rm -f "$snapshot_tmp/index" "$snapshot_tmp/index.lock" "$snapshot_tmp/paths" "$snapshot_tmp/metadata"; rmdir "$snapshot_tmp"' EXIT
export GIT_INDEX_FILE="$snapshot_tmp/index"
git read-tree "$source_commit"

# Use the index, not find -prune/-delete (which conflict on GNU find). NUL
# delimiters also cover nested files, symlinks and names containing whitespace.
git ls-files -z > "$snapshot_tmp/paths"
while IFS= read -r -d '' path; do
    case "${path##*/}" in
        copy_project.sh|copyproject.sh)
            git update-index --force-remove -- "$path"
            ;;
        .gitignore|.dockerignore)
            mode=$(git ls-files -s -- "$path")
            mode=${mode%% *}
            # Do not follow tracked symlinks or mutate the source worktree.
            [[ "$mode" == "100644" || "$mode" == "100755" ]] || continue
            git show "${source_commit}:${path}" | awk '
                $0 == "# Private mirror/synchronization tooling must never enter the public repository." { next }
                $0 ~ /^[[:space:]]*!?([*][*]\/|\/)?(copy_project|copyproject)\.sh[[:space:]]*$/ { next }
                { print }
            ' > "$snapshot_tmp/metadata"
            blob=$(git hash-object -w "$snapshot_tmp/metadata")
            git update-index --add --cacheinfo "$mode" "$blob" "$path"
            ;;
    esac
done < "$snapshot_tmp/paths"

git ls-files -z > "$snapshot_tmp/paths"
while IFS= read -r -d '' path; do
    case "${path##*/}" in
        copy_project.sh|copyproject.sh)
            printf 'Private mirror tooling remains in snapshot: %s\n' "$path" >&2
            exit 1
            ;;
    esac
done < "$snapshot_tmp/paths"

tree=$(git write-tree)
# Deliberately no -p: source history must not be reachable from this commit.
git -c user.name=oneclickvirt-sync \
    -c user.email=oneclickvirt-sync@users.noreply.github.com \
    -c commit.gpgsign=false commit-tree "$tree" \
    -m "sync: public source snapshot ${source_commit}"
