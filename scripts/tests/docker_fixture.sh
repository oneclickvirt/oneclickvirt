#!/usr/bin/env bash

create_owned_docker_volume() {
    local name="$1" owner="$2" actual_owner
    [[ -n "$name" && -n "$owner" ]] || return 2
    docker volume create --label "ocv.test.run=$owner" "$name" >/dev/null || return 1
    actual_owner=$(docker volume inspect --format '{{index .Labels "ocv.test.run"}}' "$name") || return 1
    if [[ "$actual_owner" != "$owner" ]]; then
        printf 'Refusing to use an unowned Docker volume: %s\n' "$name" >&2
        return 1
    fi
}

# Cleanup only fixtures whose label and identity still belong to this run.
remove_owned_docker_fixture() {
    local kind="$1" name="$2" owner="$3" format state actual_owner identity
    case "$kind" in
        container) format='{{index .Config.Labels "ocv.test.run"}} {{.Id}}' ;;
        network) format='{{index .Labels "ocv.test.run"}} {{.Id}}' ;;
        volume) format='{{index .Labels "ocv.test.run"}} {{.Name}}' ;;
        *) return 2 ;;
    esac
    [[ -n "$name" && -n "$owner" ]] || return 2
    state=$(docker "$kind" inspect --format "$format" "$name" 2>/dev/null) || return 0
    read -r actual_owner identity <<< "$state"
    if [[ "$actual_owner" != "$owner" || -z "$identity" || "$identity" == *[[:space:]]* ]]; then
        printf 'Preserving unowned or unresolved Docker %s: %s\n' "$kind" "$name" >&2
        return 0
    fi
    case "$kind" in
        container) docker container rm -f -v "$identity" >/dev/null ;;
        network) docker network rm "$identity" >/dev/null ;;
        volume) docker volume rm "$identity" >/dev/null ;;
    esac
}
