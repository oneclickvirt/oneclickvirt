#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
fail() { echo "CNB snapshot test failed: $*" >&2; exit 1; }

git init -q "$fixture"
cd "$fixture"
git config user.name fixture
git config user.email fixture@example.invalid
git config commit.gpgsign false
mkdir -p 'nested dir' 'nested dir/deeper'
printf 'never publish this history\n' > history-only.txt
git add .
git commit -qm old
git rm -q history-only.txt
printf 'public source\n' > main.txt
printf 'private helper\n' > copy_project.sh
printf 'private helper\n' > 'nested dir/copyproject.sh'
ln -s ../../main.txt 'nested dir/deeper/copy_project.sh'
printf '# Private mirror/synchronization tooling must never enter the public repository.\ncopy_project.sh\nprivate/**/copy_project.sh\n**/copyproject.sh\n*.log\n' > .gitignore
printf '/copyproject.sh\nnested/path/copyproject.sh\n!important.txt\ncache/\n' > .dockerignore
printf 'copy_project.sh\nkeep/\n' > 'nested dir/.gitignore'
git add -f .
git commit -qm current
source_commit=$(git rev-parse HEAD)
# Export must not include unrelated staged/unstaged/untracked user work.
printf 'staged work\n' >> main.txt
git add main.txt
printf 'unstaged work\n' >> main.txt
printf 'untracked work\n' > untracked.txt
before_index=$(git write-tree)
before_status=$(git status --porcelain)
before_file=$(git hash-object main.txt)

snapshot=$(bash "$ROOT_DIR/scripts/build_cnb_snapshot.sh" HEAD)
[[ "$(git rev-list --count "$snapshot")" == 1 ]] || fail "snapshot includes source history"
[[ "$(git rev-parse HEAD)" == "$source_commit" ]] || fail "HEAD changed"
[[ "$(git write-tree)" == "$before_index" ]] || fail "source index changed"
[[ "$(git status --porcelain)" == "$before_status" ]] || fail "worktree status changed"
[[ "$(git hash-object main.txt)" == "$before_file" ]] || fail "worktree content changed"
[[ "$(git show "$snapshot:main.txt")" == 'public source' ]] || fail "snapshot includes uncommitted work"
[[ "$(git show "$snapshot:.gitignore")" == '*.log' ]] || fail "sanitized .gitignore was not committed"
[[ "$(git show "$snapshot:.dockerignore")" == $'!important.txt\ncache/' ]] || fail "sanitized .dockerignore was not committed"
[[ "$(git show "$snapshot:nested dir/.gitignore")" == 'keep/' ]] || fail "nested ignore rules leaked"
if git ls-tree -r --name-only "$snapshot" | grep -E '(^|/)(copy_project|copyproject)\.sh$|history-only|untracked'; then
    fail "snapshot contains private paths"
fi
# Model the workflow push against a local bare destination. Both public branch
# names must point at the root snapshot, without making any old source history
# reachable from either ref.
git init --bare -q "$fixture/destination.git"
git push -q "$fixture/destination.git" "$source_commit:refs/heads/main" "$source_commit:refs/heads/master"
git push -q --force "$fixture/destination.git" "$snapshot:refs/heads/main" "$snapshot:refs/heads/master"
[[ "$(git --git-dir="$fixture/destination.git" rev-list --all --count)" == 1 ]] || fail "public branches retain source history"
for branch in main master; do
    [[ "$(git --git-dir="$fixture/destination.git" rev-parse "refs/heads/$branch")" == "$snapshot" ]] || fail "$branch was not replaced"
done
echo "CNB snapshot tests passed"
