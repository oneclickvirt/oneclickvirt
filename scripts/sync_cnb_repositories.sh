#!/usr/bin/env bash
# Synchronize only the explicitly listed public GitHub repositories.
set -euo pipefail

CNB_SYNC_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cnb_git() {
    # shellcheck disable=SC2016 # Git invokes this helper only when credentials are requested.
    git -c credential.helper= \
        -c credential.helper='!f() { test "$1" = get || exit 0; printf "%s\n" "username=${CNB_PUSH_USERNAME}" "password=${CNB_PUSH_TOKEN}"; }; f' \
        "$@"
}

is_snapshot_tree() {
    local commit=$1 tree=$2
    [[ "$(git rev-parse "${commit}^{tree}")" == "$tree" ]] &&
        [[ -z "$(git cat-file -p "$commit" | sed -n '/^parent /p')" ]]
}

sync_cnb_snapshot() (
    local source_dir=$1 destination=$2
    cd "$source_dir"
    local snapshot tree remote_refs branch old candidate
    local old_main='' old_master=''
    local refspecs=()
    snapshot=$(bash "$CNB_SYNC_SCRIPT_DIR/build_cnb_snapshot.sh" HEAD) || return
    tree=$(git rev-parse "${snapshot}^{tree}") || return
    remote_refs=$(cnb_git ls-remote --heads "$destination" refs/heads/main refs/heads/master) || return
    for branch in main master; do
        old=$(printf '%s\n' "$remote_refs" | awk -v ref="refs/heads/$branch" '$2 == ref { print $1 }')
        case "$branch" in
            main) old_main=$old ;;
            master) old_master=$old ;;
        esac
        if [[ -n "$old" ]]; then
            refspecs+=("+refs/heads/$branch:refs/remotes/cnb/$branch")
        fi
    done
    if [[ ${#refspecs[@]} -gt 0 ]]; then
        cnb_git fetch --quiet --no-tags --depth=1 "$destination" "${refspecs[@]}" || return
        # Use the exact objects read before fetching. A concurrent writer causes
        # the explicit leases below to fail instead of overwriting their update.
        for old in "$old_main" "$old_master"; do
            if [[ -n "$old" ]]; then
                git cat-file -e "${old}^{commit}" || return
            fi
        done
    fi
    candidate=$snapshot
    for old in "$old_main" "$old_master"; do
        if [[ -n "$old" ]] && is_snapshot_tree "$old" "$tree"; then
            candidate=$old
            break
        fi
    done
    if [[ "$old_main" == "$candidate" && "$old_master" == "$candidate" ]]; then
        printf 'Unchanged: %s\n' "$destination"
        return 0
    fi
    cnb_git push --atomic \
        "--force-with-lease=refs/heads/main:$old_main" \
        "--force-with-lease=refs/heads/master:$old_master" \
        "$destination" \
        "$candidate:refs/heads/main" \
        "$candidate:refs/heads/master" || return
    printf 'Synchronized: %s (%s)\n' "$destination" "$(git rev-parse HEAD)"
)

main() (
    : "${CNB_PUSH_USERNAME:?CNB_PUSH_USERNAME is not configured}"
    : "${CNB_PUSH_TOKEN:?CNB_PUSH_TOKEN is not configured}"
    export GIT_TERMINAL_PROMPT=0
    local sync_tmp repo source_branch entry failures=0
    sync_tmp=$(mktemp -d)
    trap 'rm -rf "$sync_tmp"' EXIT
    # Never infer a source from the current checkout: the maintainer also has a
    # private counterpart, which must never be published to these destinations.
    # ECS has its own source/artifact synchronization and default branch; it
    # must never be included in this main/master snapshot replacement.
    local repositories=()
    while IFS= read -r entry; do
        repositories+=("$entry")
    done < <(python3 - "$CNB_SYNC_SCRIPT_DIR/../integrations/catalog.json" <<'PY'
import json, pathlib, re, sys
catalog = pathlib.Path(sys.argv[1])
entries = {'oneclickvirt': 'main', 'oneclickvirt.github.io': 'main'}
if catalog.exists():
    for profile in json.loads(catalog.read_text()):
        owner, name = profile['repository'].split('/')
        assert owner == 'oneclickvirt' and re.fullmatch(r'[A-Za-z0-9_.-]+', name)
        assert re.fullmatch(r'[A-Za-z0-9_.-]+', profile['branch'])
        if name != 'ecs':
            entries[name] = profile['branch']
else:
    entries.update({name: 'main' for name in 'pve incus docker lxd containerd podman qemu kubevirt 6in4 ndpresponder'.split()})
for name, branch in sorted(entries.items()):
    print(name + ':' + branch)
PY
    )
    [[ ${#repositories[@]} -gt 1 ]] || return 1
    for entry in "${repositories[@]}"; do
        repo=${entry%%:*}
        source_branch=${entry#*:}
        printf 'Synchronizing oneclickvirt/%s\n' "$repo"
        if ! git -c credential.helper= clone --quiet --depth=1 --single-branch --branch "$source_branch" \
            "https://github.com/oneclickvirt/$repo.git" "$sync_tmp/$repo"; then
            printf 'Failed to read public source: %s\n' "$repo" >&2
            failures=$((failures + 1))
            continue
        fi
        if ! sync_cnb_snapshot "$sync_tmp/$repo" "https://cnb.cool/oneclickvirt/$repo"; then
            printf 'Failed to synchronize: %s\n' "$repo" >&2
            failures=$((failures + 1))
        fi
    done
    [[ "$failures" == 0 ]]
)

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
