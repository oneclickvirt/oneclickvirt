export default {
  title: 'OneClickVirt',
  nav: {
    login: '登录',
    register: '注册'
  },
  hero: {
    title: '开源虚拟化管理平台',
    description: 'OneClickVirt 提供开源简单易用的虚拟机和容器管理，支持多种虚拟化开设。',
    loginButton: '帐户登录',
    registerButton: '注册使用'
  },
  features: {
    vm: {
      title: '虚拟机管理',
      description: '快速创建和管理虚拟机实例'
    },
    container: {
      title: '容器管理',
      description: '快速创建和管理容器实例'
    },
    monitoring: {
      title: '监控面板',
      description: '实时监控资源使用情况'
    }
  },
  platformOverview: {
    title: '平台概览',
    description: '当前平台运行状态'
  },
  platforms: {
    title: '支持的虚拟化平台',
    description: '一键对接多种主流虚拟化技术，点击卡片查看对应开源项目'
  },
  stats: {
    users: '用户数量',
    nodes: '节点数量',
    containers: '容器数量',
    vms: '虚拟机数量'
  },
  announcements: {
    title: '系统公告',
    typeHomepage: '首页公告',
    typeTopbar: '顶部栏公告'
  },
  supporters: {
    title: '赞助方',
    description: '感谢以下团体或个人赞助 OneClickVirt 项目'
  },
  footer: {
    coreProjects: '核心项目',
    relatedProjects: '相关项目',
    moreProjects: '更多项目',
    supportAndDocs: '支持',
    supporters: '赞助方',
    documentation: '使用文档',
    feedback: '问题反馈',
    communityGroup: '交流群组',
    allRightsReserved: 'All rights reserved.',
    openSourceProject: '一键虚拟化旗下开源项目',
    serverVersion: '主控版本',
    latestVersion: '最新版本',
    versionFetchFailed: '版本获取失败，后端可能已断连',
    manageUpdates: '升级管理',
    updateDialogTitle: '主控版本管理',
    currentVersion: '当前版本',
    deploymentMode: '部署模式',
    deploymentFlavor: '发布类型',
    updateTab: '升级',
    rollbackTab: '回退',
    commandsTab: '命令',
    selectVersion: '选择目标版本',
    selectRollbackVersion: '选择回退版本或本地备份',
    assetUnavailable: '资产不可用',
    localBackup: '本地备份',
    updateNow: '立即升级',
    rollbackNow: '立即回退',
    restartNow: '重启主控',
    noCommands: '当前部署没有可展示的命令',
    destructiveCommand: '会修改运行环境',
    copyCommand: '复制命令',
    updateLoadFailed: '获取升级信息失败',
    operationRunning: '操作进行中',
    reconnecting: '主控正在重启，等待连接恢复',
    operationSubmitted: '操作已提交',
    operationFailed: '操作失败',
    updateConfirm: '确定升级到 {version} 吗？升级会备份当前主控和 Web 文件，并重启服务。',
    rollbackConfirm: '确定回退到 {version} 吗？数据库迁移不会自动逆向回退。',
    restartConfirm: '确定重启主控服务吗？当前页面会短暂断开。',
    rollbackWarning: '回退只替换主控和 Web 资产；数据库迁移通常保持向前兼容，请先确认版本兼容性。',
    rollbackDatabaseNote: '数据库迁移通常不会自动逆向回退，请在生产操作前保留数据库备份。'
  },
  errors: {
    fetchAnnouncementsFailed: '获取公告失败:',
    checkInitFailed: '检查系统初始化状态失败:'
  },
  debug: {
    checkingInit: '首页检查初始化状态:',
    needInitRedirect: '系统需要初始化，跳转到初始化页面',
    serverConnectionFailed: '服务器连接失败，可能需要初始化，跳转到初始化页面'
  }
}
