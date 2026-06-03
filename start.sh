#!/bin/bash

set -e

PROJECT_ROOT=$(pwd)
SERVER_DIR="$PROJECT_ROOT/server"
WEB_DIR="$PROJECT_ROOT/web"
SERVER_PID_FILE="$PROJECT_ROOT/.server.pid"
WEB_PID_FILE="$PROJECT_ROOT/.web.pid"
LOG_DIR="$PROJECT_ROOT/server/storage/logs"
SERVER_LOG="$LOG_DIR/server.log"
WEB_LOG="$LOG_DIR/web.log"

mkdir -p "$LOG_DIR"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

check_port() {
    lsof -Pi :$1 -sTCP:LISTEN -t >/dev/null 2>&1
}

check_dependencies() {
    log "检查依赖项..."
    
    if ! command -v go &> /dev/null; then
        log "错误: 未找到 Go"
        exit 1
    fi
    
    if ! command -v node &> /dev/null; then
        log "错误: 未找到 Node.js"
        exit 1
    fi
    
    if ! command -v npm &> /dev/null; then
        log "错误: 未找到 npm"
        exit 1
    fi
}

install_dependencies() {
    log "安装依赖项..."
    
    cd "$SERVER_DIR"
    go mod tidy
    go mod download
    
    cd "$WEB_DIR"
    if [ ! -d "node_modules" ]; then
        npm install
    fi
    
    cd "$PROJECT_ROOT"
}

clean_files() {
    log "清理旧文件..."
    
    if [ -f "$SERVER_DIR/oneclickvirt" ]; then
        rm -f "$SERVER_DIR/oneclickvirt"
        log "已删除旧二进制文件"
    fi
    
    if [ -d "$SERVER_DIR/data" ]; then
        rm -rf "$SERVER_DIR/data"
        log "已删除数据目录"
    fi
    if [ -d "$PROJECT_ROOT/data" ]; then
        rm -rf "$PROJECT_ROOT/data"
        log "已删除数据目录"
    fi
    
    if [ -d "$SERVER_DIR/certs" ]; then
        rm -rf "$SERVER_DIR/certs"
        log "已删除证书目录"
    fi
    
    # 删除初始化标志文件，以便重新初始化
    if [ -f "$SERVER_DIR/storage/.db_initialized" ]; then
        rm -f "$SERVER_DIR/storage/.db_initialized"
        log "已删除初始化标志文件"
    fi
    
    log "清理完成"
}

clean_logs() {
    log "准备清理所有日志..."
    
    local has_logs=false
    local log_locations=()
    
    if [ -d "$LOG_DIR" ] && [ -n "$(ls -A "$LOG_DIR" 2>/dev/null)" ]; then
        log_locations+=("$LOG_DIR")
        has_logs=true
    fi
    
    if [ -d "$SERVER_DIR/logs" ] && [ -n "$(ls -A "$SERVER_DIR/logs" 2>/dev/null)" ]; then
        log_locations+=("$SERVER_DIR/logs")
        has_logs=true
    fi
    
    if [ -d "$WEB_DIR/logs" ] && [ -n "$(ls -A "$WEB_DIR/logs" 2>/dev/null)" ]; then
        log_locations+=("$WEB_DIR/logs")
        has_logs=true
    fi
    
    find "$PROJECT_ROOT" -name "*.log" -type f 2>/dev/null | while read -r logfile; do
        if [ -f "$logfile" ]; then
            has_logs=true
            break
        fi
    done
    
    if [ "$has_logs" = false ]; then
        log "未找到日志文件，无需清理"
        return
    fi
    
    echo "警告: 此操作将删除所有日志文件，包括："
    for location in "${log_locations[@]}"; do
        echo "目录: $location"
        ls -la "$location" 2>/dev/null
        echo ""
    done
    
    echo "散落的日志文件:"
    find "$PROJECT_ROOT" -name "*.log" -type f 2>/dev/null | head -10
    echo ""
    
    echo -n "确认删除所有日志文件吗？(输入 'yes' 确认): "
    read confirm
    
    if [ "$confirm" = "yes" ]; then
        for location in "${log_locations[@]}"; do
            rm -rf "$location"/*
            log "已清理目录: $location"
        done
        
        find "$PROJECT_ROOT" -name "*.log" -type f -delete 2>/dev/null
        log "已清理所有 .log 文件"
        
        log "所有日志文件已清理完成"
    else
        log "操作已取消"
    fi
}

build_server() {
    log "构建服务器..."
    cd "$SERVER_DIR"
    
    go build -o oneclickvirt .
    log "服务器构建完成"
    
    cd "$PROJECT_ROOT"
}

start_server() {
    if [ -f "$SERVER_PID_FILE" ] && kill -0 $(cat "$SERVER_PID_FILE") 2>/dev/null; then
        log "服务器已在运行 (PID: $(cat $SERVER_PID_FILE))"
        return
    fi
    
    if check_port 8888; then
        log "端口 8888 已被占用"
        return 1
    fi
    
    log "启动服务器..."
    cd "$SERVER_DIR"
    
    nohup ./oneclickvirt > "$SERVER_LOG" 2>&1 &
    echo $! > "$SERVER_PID_FILE"
    
    sleep 3
    
    if kill -0 $(cat "$SERVER_PID_FILE") 2>/dev/null; then
        log "服务器已启动 (PID: $(cat $SERVER_PID_FILE))"
        log "服务器地址: http://localhost:8888"
    else
        log "服务器启动失败"
        rm -f "$SERVER_PID_FILE"
        return 1
    fi
    
    cd "$PROJECT_ROOT"
}

start_web() {
    if [ -f "$WEB_PID_FILE" ] && kill -0 $(cat "$WEB_PID_FILE") 2>/dev/null; then
        log "Web服务已在运行 (PID: $(cat $WEB_PID_FILE))"
        return
    fi
    
    if check_port 8080; then
        log "端口 8080 已被占用"
        return 1
    fi
    
    log "启动Web服务..."
    cd "$WEB_DIR"
    
    nohup npm run serve > "$WEB_LOG" 2>&1 &
    echo $! > "$WEB_PID_FILE"
    
    sleep 5
    
    if kill -0 $(cat "$WEB_PID_FILE") 2>/dev/null; then
        log "Web服务已启动 (PID: $(cat $WEB_PID_FILE))"
        log "Web地址: http://localhost:8080"
    else
        log "Web服务启动失败"
        rm -f "$WEB_PID_FILE"
        return 1
    fi
    
    cd "$PROJECT_ROOT"
}

stop_service() {
    local name=$1
    local pid_file=$2
    
    if [ -f "$pid_file" ]; then
        local pid=$(cat "$pid_file")
        if kill -0 "$pid" 2>/dev/null; then
            log "停止 $name (PID: $pid)..."
            kill "$pid"
            
            local count=0
            while kill -0 "$pid" 2>/dev/null && [ $count -lt 10 ]; do
                sleep 1
                count=$((count + 1))
            done
            
            if kill -0 "$pid" 2>/dev/null; then
                kill -9 "$pid"
            fi
            
            log "$name 已停止"
        fi
        rm -f "$pid_file"
    else
        log "$name 未在运行"
    fi
}

stop_all() {
    log "停止所有服务..."
    stop_service "服务器" "$SERVER_PID_FILE"
    stop_service "Web服务" "$WEB_PID_FILE"
}

stop_server_only() {
    log "停止服务器..."
    stop_service "服务器" "$SERVER_PID_FILE"
}

stop_web_only() {
    log "停止Web服务..."
    stop_service "Web服务" "$WEB_PID_FILE"
}

clean_stop() {
    log "停止所有服务并清理文件..."
    stop_service "服务器" "$SERVER_PID_FILE"
    stop_service "Web服务" "$WEB_PID_FILE"
    clean_files
}

status() {
    log "服务状态:"
    
    if [ -f "$SERVER_PID_FILE" ] && kill -0 $(cat "$SERVER_PID_FILE") 2>/dev/null; then
        echo "服务器: 运行中 (PID: $(cat $SERVER_PID_FILE))"
        if check_port 8888; then
            echo "  端口 8888: 监听中"
        else
            echo "  端口 8888: 未监听"
        fi
    else
        echo "服务器: 未运行"
        rm -f "$SERVER_PID_FILE"
    fi
    
    if [ -f "$WEB_PID_FILE" ] && kill -0 $(cat "$WEB_PID_FILE") 2>/dev/null; then
        echo "Web服务: 运行中 (PID: $(cat $WEB_PID_FILE))"
        if check_port 8080; then
            echo "  端口 8080: 监听中"
        else
            echo "  端口 8080: 未监听"
        fi
    else
        echo "Web服务: 未运行"
        rm -f "$WEB_PID_FILE"
    fi
}

logs() {
    local service=$1
    case $service in
        "server")
            if [ -f "$SERVER_LOG" ]; then
                echo "服务器日志 (最后50行):"
                tail -n 50 "$SERVER_LOG"
            else
                echo "服务器日志文件未找到"
            fi
            ;;
        "web")
            if [ -f "$WEB_LOG" ]; then
                echo "Web服务日志 (最后50行):"
                tail -n 50 "$WEB_LOG"
            else
                echo "Web服务日志文件未找到"
            fi
            ;;
        *)
            if [ -f "$SERVER_LOG" ]; then
                echo "=== 服务器日志 (最后20行) ==="
                tail -n 20 "$SERVER_LOG"
            fi
            if [ -f "$WEB_LOG" ]; then
                echo "=== Web服务日志 (最后20行) ==="
                tail -n 20 "$WEB_LOG"
            fi
            ;;
    esac
}

start_all() {
    log "启动 OneClickVirt..."
    
    check_dependencies
    install_dependencies
    build_server
    
    start_server
    if [ $? -eq 0 ]; then
        start_web
        if [ $? -eq 0 ]; then
            log "所有服务已启动"
            log "Web界面: http://localhost:8080"
            log "API接口: http://localhost:8888"
            log "默认登录: admin / Admin123!@#"
        fi
    fi
}

start_server_only() {
    log "仅启动服务器..."
    
    check_dependencies
    install_dependencies
    build_server
    
    start_server
    if [ $? -eq 0 ]; then
        log "服务器已启动"
        log "API接口: http://localhost:8888"
        log "默认登录: admin / Admin123!@#"
    fi
}

start_web_only() {
    log "仅启动Web服务..."
    
    check_dependencies
    install_dependencies
    
    start_web
    if [ $? -eq 0 ]; then
        log "Web服务已启动"
        log "Web界面: http://localhost:8080"
    fi
}

restart_all() {
    log "重启所有服务..."
    clean_stop
    sleep 2
    start_all
}

restart_server_only() {
    log "重启服务器..."
    stop_server_only
    clean_files
    sleep 2
    start_server_only
}

restart_web_only() {
    log "重启Web服务..."
    stop_web_only
    sleep 2
    start_web_only
}

clean_restart() {
    log "清理重启服务..."
    clean_stop
    sleep 2
    start_all
}

show_help() {
    echo "OneClickVirt 启动脚本"
    echo ""
    echo "用法: $0 [命令]"
    echo ""
    echo "启动命令:"
    echo "  start       启动所有服务 (默认)"
    echo "  start-server 仅启动后端服务器"
    echo "  start-web   仅启动前端Web服务"
    echo ""
    echo "停止命令:"
    echo "  stop        停止所有服务"
    echo "  stop-server 仅停止后端服务器"
    echo "  stop-web    仅停止前端Web服务"
    echo ""
    echo "重启命令:"
    echo "  restart     重启所有服务"
    echo "  restart-server 仅重启后端服务器"
    echo "  restart-web 仅重启前端Web服务"
    echo ""
    echo "其他命令:"
    echo "  clean       停止服务并清理文件"
    echo "  cleanlogs   清理所有日志文件"
    echo "  status      显示服务状态"
    echo "  logs        显示服务日志"
    echo "  logs server 显示服务器日志"
    echo "  logs web    显示Web服务日志"
    echo "  build       仅构建服务器"
    echo "  install     仅安装依赖"
    echo "  help        显示此帮助"
}

case "${1:-start}" in
    "start")
        start_all
        ;;
    "start-server")
        start_server_only
        ;;
    "start-web")
        start_web_only
        ;;
    "stop")
        stop_all
        ;;
    "stop-server")
        stop_server_only
        ;;
    "stop-web")
        stop_web_only
        ;;
    "restart")
        restart_all
        ;;
    "restart-server")
        restart_server_only
        ;;
    "restart-web")
        restart_web_only
        ;;
    "clean")
        clean_stop
        ;;
    "cleanlogs")
        clean_logs
        ;;
    "status")
        status
        ;;
    "logs")
        logs "$2"
        ;;
    "build")
        check_dependencies
        build_server
        ;;
    "install")
        check_dependencies
        install_dependencies
        ;;
    "help"|"-h"|"--help")
        show_help
        ;;
    *)
        echo "未知命令: $1"
        show_help
        exit 1
        ;;
esac
