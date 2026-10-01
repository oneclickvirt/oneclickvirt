#!/usr/bin/env bash
set -euo pipefail

image="${1:?image name is required}"
metadata="${DOCKER_METADATA_OUTPUT_JSON:?Docker metadata is required}"

digest_files=()
for path in ./*; do
    [[ -f "$path" ]] && digest_files+=("${path#./}")
done
if [[ ${#digest_files[@]} -ne 2 ]]; then
    printf 'Expected 2 platform digests (amd64 + arm64), found %s.\n' "${#digest_files[@]}" >&2
    exit 1
fi

tag_json=$(jq -er '.tags | if type == "array" and length > 0 then . else error("missing image tags") end' <<< "$metadata")
mapfile -t tags < <(jq -r '.[]' <<< "$tag_json")
tag_args=()
for tag in "${tags[@]}"; do
    tag_args+=(-t "$tag")
done

image_refs=()
for digest in "${digest_files[@]}"; do
    if [[ ! "$digest" =~ ^[[:xdigit:]]{64}$ ]]; then
        printf 'Invalid digest artifact filename: %s\n' "$digest" >&2
        exit 1
    fi
    image_refs+=("${image}@sha256:${digest}")
done

docker buildx imagetools create "${tag_args[@]}" "${image_refs[@]}"
