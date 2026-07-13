#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# shellcheck source=../../action_tests/common/test_framework.sh
source "${ROOT_DIR}/action_tests/common/test_framework.sh"
# shellcheck source=../../action_tests/modules/29_provider_images.sh
source "${ROOT_DIR}/action_tests/modules/29_provider_images.sh"

fail() {
    echo "provider image selection test failed: $*" >&2
    exit 1
}

fixture='[
  {"name":"alpine-3.19-cloud","instanceType":"container","architecture":"amd64","osType":"alpine","osVersion":"3.19"},
  {"name":"alpine-3.21-cloud","instanceType":"container","architecture":"amd64","osType":"alpine","osVersion":"3.21"},
  {"name":"alpine-edge-cloud","instanceType":"container","architecture":"amd64","osType":"alpine","osVersion":"edge"},
  {"name":"debian-11-cloud","instanceType":"container","architecture":"amd64","osType":"debian","osVersion":"11"},
  {"name":"debian-13-cloud","instanceType":"container","architecture":"amd64","osType":"debian","osVersion":"13"},
  {"name":"alpine-3.19-kvm-cloud","instanceType":"vm","architecture":"amd64","osType":"alpine","osVersion":"3.19"},
  {"name":"alpine-edge-kvm-cloud","instanceType":"vm","architecture":"amd64","osType":"alpine","osVersion":"edge"},
  {"name":"debian-11-kvm-cloud","instanceType":"vm","architecture":"amd64","osType":"debian","osVersion":"11"},
  {"name":"debian-12-kvm-cloud","instanceType":"vm","architecture":"amd64","osType":"debian","osVersion":"12"},
  {"name":"debian-13-kvm-cloud","instanceType":"vm","architecture":"arm64","osType":"debian","osVersion":"13"}
]'

TEST_IMAGES="alpine,debian"
INSTANCE_TYPES="both"
PROVIDER_IMAGE_MAX_PER_FAMILY_TYPE=1
selected=$(_m29_select_candidates "$fixture" "amd64")
actual_names=$(echo "$selected" | jq -r '.[].name' | sort)
expected_names=$(printf '%s\n' \
    alpine-3.19-kvm-cloud \
    alpine-3.21-cloud \
    debian-12-kvm-cloud \
    debian-13-cloud | sort)
[[ "$actual_names" == "$expected_names" ]] || fail "unexpected representative set: ${actual_names//$'\n'/, }"

INSTANCE_TYPES="container"
selected=$(_m29_select_candidates "$fixture" "amd64")
[[ "$(echo "$selected" | jq 'length')" == "2" ]] || fail "container-only selection should contain two images"
[[ "$(echo "$selected" | jq -r 'all(.[]; .type == "container")')" == "true" ]] || fail "container-only selection included a VM"

TEST_IMAGES="all"
INSTANCE_TYPES="both"
PROVIDER_IMAGE_MAX_PER_FAMILY_TYPE=0
selected=$(_m29_select_candidates "$fixture" "amd64")
[[ "$(echo "$selected" | jq 'length')" == "9" ]] || fail "exhaustive selection should keep all nine amd64 images"

echo "provider image selection tests passed"
