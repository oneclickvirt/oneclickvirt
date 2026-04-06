#!/bin/bash
# 单模块运行器 - 用于Actions中选择运行特定模块
# 用法: bash run_module.sh <module_number|module_range> [server_url]
# 示例: bash run_module.sh 01          # 运行模块01
#        bash run_module.sh 01-05       # 运行模块01到05
#        bash run_module.sh 01,03,05    # 运行模块01,03,05
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULES_DIR="${SCRIPT_DIR}/modules"
COMMON_DIR="${SCRIPT_DIR}/common"

source "${COMMON_DIR}/test_framework.sh"

MODULE_INPUT="${1:-}"
SERVER_URL="${2:-${SERVER_URL:-}}"

if [[ -z "$MODULE_INPUT" ]]; then
    echo "用法: $0 <模块编号> [server_url]"
    echo "支持格式: 01 | 01-05 | 01,03,05 | all"
    echo ""
    echo "可用模块:"
    for f in "${MODULES_DIR}"/*.sh; do
        local_name=$(basename "$f")
        head -2 "$f" | grep "^# 模块" || echo "  ${local_name}"
    done
    exit 1
fi

# 解析模块列表
parse_modules() {
    local input="$1"
    local modules=()
    if [[ "$input" == "all" ]]; then
        for f in "${MODULES_DIR}"/*.sh; do
            local num; num=$(basename "$f" | grep -oP '^\d+')
            modules+=("$num")
        done
    elif [[ "$input" == *-* ]]; then
        local start; start=$(echo "$input" | cut -d- -f1 | sed 's/^0*//')
        local end; end=$(echo "$input" | cut -d- -f2 | sed 's/^0*//')
        for ((i=start; i<=end; i++)); do
            modules+=($(printf "%02d" "$i"))
        done
    elif [[ "$input" == *,* ]]; then
        IFS=',' read -ra parts <<< "$input"
        for p in "${parts[@]}"; do
            modules+=($(printf "%02d" "$(echo "$p" | sed 's/^0*//')"))
        done
    else
        modules+=($(printf "%02d" "$(echo "$input" | sed 's/^0*//')"))
    fi
    echo "${modules[@]}"
}

MODULES=($(parse_modules "$MODULE_INPUT"))

if [[ -z "$SERVER_URL" ]]; then
    echo "错误: 请设置 SERVER_URL 环境变量或通过参数传入"
    exit 1
fi

log_section "开始模块测试: ${MODULES[*]}"
log_info "服务器: ${SERVER_URL}"
log_info "环境: ${ENV_TYPE}"

# 初始化报告
REPORT_DIR="${SCRIPT_DIR}/reports"
mkdir -p "$REPORT_DIR"
report_init "${REPORT_DIR}/module-${MODULE_INPUT}.md" "模块 ${MODULE_INPUT}"

# 先执行登录(大多数模块需要)
wait_server_ready "$SERVER_URL" 60 5 || { log_error "服务器不可达"; exit 1; }
ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS") || { log_error "管理员登录失败"; exit 1; }
USER_TOKEN=$(do_login "$SERVER_URL" "$TEST_USER" "$TEST_USER_PASS") || USER_TOKEN="$ADMIN_TOKEN"
NORMAL_ADMIN_TOKEN=$(do_login "$SERVER_URL" "$NORMAL_ADMIN_USER" "$NORMAL_ADMIN_PASS") || true

# 加载并运行模块
EXIT_CODE=0
for mod in "${MODULES[@]}"; do
    mod_file="${MODULES_DIR}/${mod}_*.sh"
    mod_files=(${mod_file})
    if [[ ! -f "${mod_files[0]}" ]]; then
        log_warning "模块 ${mod} 不存在，跳过"
        continue
    fi
    source "${mod_files[0]}"
    func_name="run_module_${mod}"
    if declare -f "$func_name" > /dev/null 2>&1; then
        log_section "运行模块 ${mod}"
        "$func_name" || EXIT_CODE=1
    else
        log_warning "模块 ${mod} 无入口函数 ${func_name}"
    fi
done

# 汇总
report_finalize
log_section "测试完成"
log_info "总计: ${TOTAL_TESTS} | 通过: ${PASSED_TESTS} | 失败: ${FAILED_TESTS} | 跳过: ${SKIPPED_TESTS}"
[[ $FAILED_TESTS -gt 0 ]] && EXIT_CODE=1
exit $EXIT_CODE
