#!/usr/bin/env bash

# Normalize user input before arithmetic, filenames or provisioning. Each
# module is selected once, even when ranges and individual IDs overlap.
parse_test_modules() {
    local input="$1" module_dir="$2" part first last number file
    local -a parts=() selected=()
    local -A seen=()
    if [[ "$input" == all ]]; then
        for file in "$module_dir"/[0-9][0-9]_*.sh; do
            [[ -f "$file" ]] || continue
            part="${file##*/}"
            number="${part:0:2}"
            [[ "$number" != 00 ]] || continue
            if [[ -z "${seen[$number]:-}" ]]; then
                selected+=("$number")
                seen[$number]=1
            fi
        done
    else
        if [[ ${#input} -gt 512 || ! "$input" =~ ^[0-9]{1,2}(-[0-9]{1,2})?(,[0-9]{1,2}(-[0-9]{1,2})?)*$ ]]; then
            printf 'Invalid module selection: use 01, 01-05, 01,03,05 or all\n' >&2
            return 2
        fi
        IFS=',' read -r -a parts <<< "$input"
        for part in "${parts[@]}"; do
            first="${part%%-*}"
            last="${part##*-}"
            first=$((10#$first))
            last=$((10#$last))
            if (( first < 1 || last < first )); then
                printf 'Invalid module range: %s\n' "$part" >&2
                return 2
            fi
            for ((number=first; number<=last; number++)); do
                printf -v file '%02d' "$number"
                if [[ -z "${seen[$file]:-}" ]]; then
                    selected+=("$file")
                    seen[$file]=1
                fi
            done
        done
    fi
    [[ ${#selected[@]} -gt 0 ]] || { printf 'No test modules selected\n' >&2; return 2; }
    printf '%s\n' "${selected[*]}"
}

validate_test_modules() {
    local module_dir="$1" number file found
    shift
    for number in "$@"; do
        found=false
        for file in "$module_dir/${number}_"*.sh; do
            [[ -f "$file" ]] && found=true
        done
        if [[ "$found" != true ]]; then
            printf 'Requested module %s not found\n' "$number" >&2
            return 2
        fi
    done
}
