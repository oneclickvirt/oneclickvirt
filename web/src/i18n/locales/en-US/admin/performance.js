export default {
  title: "Performance Monitoring",
  subtitle: "Real-time monitoring of system performance metrics to ensure stable service operation",
  autoRefresh: "Auto Refresh",
  
  // Metric Cards
  goroutineCount: "Goroutine Count",
  memoryUsage: "Memory Usage",
  gcCount: "GC Count",
  databaseConnections: "Database Connections",
  
  // Status
  status: {
    normal: "Normal",
    warning: "Warning",
    critical: "Critical"
  },
  
  // Metric Details
  averagePause: "Average Pause",
  utilization: "Utilization",
  unit: "Unit",
  
  // Memory Details
  memoryDetails: "Memory Details",
  currentAlloc: "Current Allocation",
  totalAlloc: "Total Allocation",
  systemMemory: "System Memory",
  heapMemory: "Heap Memory",
  heapSystem: "Heap System",
  stackUsage: "Stack Usage",
  heapAlloc: "Heap Alloc",
  stackInuse: "Stack In Use",
  other: "Other",
  
  // GC Details
  gcDetails: "GC Garbage Collection Details",
  totalPauseTime: "Total Pause Time",
  lastPause: "Last Pause",
  nextGC: "Next GC",
  cpuCores: "CPU Cores",
  
  // Database Pool
  databasePool: "Database Connection Pool",
  maxConnections: "Max Connections",
  currentConnections: "Current Connections",
  inUse: "In Use",
  idle: "Idle",
  waitCount: "Wait Count",
  waitDuration: "Wait Duration",
  maxIdleClosed: "Max Idle Closed",
  maxLifetimeClosed: "Max Lifetime Closed",
  noData: "No database connection pool information available",
  
  // Database Manager Status
  dbManagerStatus: "Database Manager Status",
  connected: "Connected",
  reconnecting: "Reconnecting",
  heartbeatActive: "Heartbeat Active",
  maxReconnectRetry: "Max Reconnect Retry",
  reconnectInterval: "Reconnect Interval",
  
  // SSH Pool
  sshPool: "SSH Connection Pool",
  totalConnections: "Total Connections",
  healthyConnections: "Healthy Connections",
  unhealthyConnections: "Unhealthy Connections",
  activeConnections: "Active Connections",
  idleConnections: "Idle Connections",
  avgConnectionAge: "Avg Connection Age",
  oldestConnectionAge: "Oldest Connection Age",
  maxIdleTime: "Max Idle Time",
  noSSHData: "No SSH connection pool information available",
  
  // Goroutine Trend
  goroutineTrend: "Goroutine Count Trend",
  timeRange: {
    "5m": "5 Minutes",
    "15m": "15 Minutes",
    "1h": "1 Hour",
    "6h": "6 Hours"
  },
  
  // Chart Labels
  totalPauseLabel: "Total Pause",
  averagePauseLabel: "Average Pause",
  lastPauseLabel: "Last Pause",
  nanoseconds: "Nanoseconds",
  count: "Count",
  goroutineLabel: "Goroutine",
  
  // Error Messages
  fetchMetricsError: "Failed to fetch performance metrics",
  fetchHistoryError: "Failed to fetch historical data"
}
