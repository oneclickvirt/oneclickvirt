export default {
  title: "性能监控",
  subtitle: "实时监控系统性能指标，确保服务稳定运行",
  autoRefresh: "自动刷新",
  
  // 指标卡片
  goroutineCount: "Goroutine 数量",
  memoryUsage: "内存使用",
  gcCount: "GC 次数",
  databaseConnections: "数据库连接",
  
  // 状态
  status: {
    normal: "正常",
    warning: "警告",
    critical: "危险"
  },
  
  // 指标详情
  averagePause: "平均暂停",
  utilization: "使用率",
  unit: "单位",
  
  // 内存详情
  memoryDetails: "内存详情",
  currentAlloc: "当前分配",
  totalAlloc: "累计分配",
  systemMemory: "系统内存",
  heapMemory: "堆内存",
  heapSystem: "堆系统",
  stackUsage: "栈使用",
  heapAlloc: "堆内存",
  stackInuse: "栈内存",
  other: "其他",
  
  // GC 详情
  gcDetails: "GC 垃圾回收详情",
  totalPauseTime: "总暂停时间",
  lastPause: "上次暂停",
  nextGC: "下次 GC",
  cpuCores: "CPU 核心数",
  
  // 数据库连接池
  databasePool: "数据库连接池",
  maxConnections: "最大连接数",
  currentConnections: "当前连接数",
  inUse: "使用中",
  idle: "空闲",
  waitCount: "等待次数",
  waitDuration: "等待时长",
  maxIdleClosed: "空闲关闭",
  maxLifetimeClosed: "生命周期关闭",
  noData: "暂无数据库连接池信息",
  
  // 数据库管理器状态
  dbManagerStatus: "数据库管理器状态",
  connected: "已连接",
  reconnecting: "重连中",
  heartbeatActive: "心跳监控",
  maxReconnectRetry: "最大重连次数",
  reconnectInterval: "重连间隔",
  
  // SSH 连接池
  sshPool: "SSH 连接池",
  totalConnections: "总连接数",
  healthyConnections: "健康连接数",
  unhealthyConnections: "不健康连接数",
  activeConnections: "活跃连接数",
  idleConnections: "空闲连接数",
  avgConnectionAge: "平均连接年龄",
  oldestConnectionAge: "最老连接年龄",
  maxIdleTime: "最大空闲时间",
  noSSHData: "暂无SSH连接池信息",
  
  // Goroutine 趋势
  goroutineTrend: "Goroutine 数量趋势",
  timeRange: {
    "5m": "5分钟",
    "15m": "15分钟",
    "1h": "1小时",
    "6h": "6小时"
  },
  
  // 图表标签
  totalPauseLabel: "总暂停",
  averagePauseLabel: "平均暂停",
  lastPauseLabel: "上次暂停",
  nanoseconds: "纳秒",
  count: "数量",
  goroutineLabel: "Goroutine",
  
  // 错误消息
  fetchMetricsError: "获取性能指标失败",
  fetchHistoryError: "获取历史数据失败"
}
