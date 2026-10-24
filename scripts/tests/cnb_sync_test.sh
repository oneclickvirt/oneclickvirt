#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../sync_cnb_repositories.sh
source "$ROOT_DIR/scripts/sync_cnb_repositories.sh"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
fail() { echo "CNB sync test failed: $*" >&2; exit 1; }

git init -q "$fixture/source"
git -C "$fixture/source" config user.name fixture
git -C "$fixture/source" config user.email fixture@example.invalid
git -C "$fixture/source" config commit.gpgsign false
printf 'public source\n' > "$fixture/source/source.txt"
git -C "$fixture/source" add .
git -C "$fixture/source" commit -qm initial
git init --bare -q "$fixture/destination.git"
git --git-dir="$fixture/destination.git" symbolic-ref HEAD refs/heads/main

# Empty destination, repeat run, and one missing branch.
sync_cnb_snapshot "$fixture/source" "$fixture/destination.git"
first=$(git --git-dir="$fixture/destination.git" rev-parse main)
sync_cnb_snapshot "$fixture/source" "$fixture/destination.git"
[[ "$(git --git-dir="$fixture/destination.git" rev-parse main)" == "$first" ]] || fail 'unchanged source was pushed again'
git --git-dir="$fixture/destination.git" update-ref -d refs/heads/master
sync_cnb_snapshot "$fixture/source" "$fixture/destination.git"
[[ "$(git --git-dir="$fixture/destination.git" rev-parse master)" == "$first" ]] || fail 'missing branch was not repaired from existing snapshot'

# An existing branch with identical content but full history must be replaced.
git -C "$fixture/source" commit --allow-empty -qm 'history must not be mirrored'
git -C "$fixture/source" push -q --force "$fixture/destination.git" HEAD:refs/heads/main HEAD:refs/heads/master HEAD:refs/heads/unrelated
unrelated=$(git --git-dir="$fixture/destination.git" rev-parse unrelated)
sync_cnb_snapshot "$fixture/source" "$fixture/destination.git"
for branch in main master; do
    [[ "$(git --git-dir="$fixture/destination.git" rev-list --count "$branch")" == 1 ]] || fail "$branch retained source history"
done
[[ "$(git --git-dir="$fixture/destination.git" rev-parse unrelated)" == "$unrelated" ]] || fail 'unrelated branch changed'
[[ "$(git --git-dir="$fixture/destination.git" symbolic-ref HEAD)" == refs/heads/main ]] || fail 'default branch changed'

printf 'updated public source\n' >> "$fixture/source/source.txt"
git -C "$fixture/source" commit -qam update
sync_cnb_snapshot "$fixture/source" "$fixture/destination.git"
[[ "$(git --git-dir="$fixture/destination.git" rev-parse main)" == "$(git --git-dir="$fixture/destination.git" rev-parse master)" ]] || fail 'snapshot branches diverged'
[[ "$(git --git-dir="$fixture/destination.git" show main:source.txt)" == "$(cat "$fixture/source/source.txt")" ]] || fail 'updated content was not synchronized'
echo 'CNB sync tests passed'
