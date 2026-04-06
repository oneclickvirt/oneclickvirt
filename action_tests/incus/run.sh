#!/bin/bash
# Incus 虚拟化环境集成测试入口
# 使用 AliceInit 创建远程节点，安装 Incus 环境，部署主控并运行完整 API 测试

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export ENV_TYPE="incus"
export REPORT_DIR="${SCRIPT_DIR}"

source "${SCRIPT_DIR}/../common/run_env_test.sh"

main
exit $?
