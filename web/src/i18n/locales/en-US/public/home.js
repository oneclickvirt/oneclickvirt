export default {
  title: 'OneClickVirt',
  nav: {
    login: 'Login',
    register: 'Register'
  },
  hero: {
    title: 'Open Source Virtualization Management Platform',
    description: 'OneClickVirt provides an easy-to-use open-source virtual machine and container management platform, supporting multiple virtualization technologies.',
    loginButton: 'Account Login',
    registerButton: 'Register Now'
  },
  features: {
    vm: {
      title: 'Virtual Machine Management',
      description: 'Quickly create and manage virtual machine instances'
    },
    container: {
      title: 'Container Management',
      description: 'Quickly create and manage container instances'
    },
    monitoring: {
      title: 'Monitoring Dashboard',
      description: 'Real-time resource usage monitoring'
    }
  },
  platformOverview: {
    title: 'Platform Overview',
    description: 'Real-time platform operational statistics'
  },
  platforms: {
    title: 'Supported Virtualization Platforms',
    description: 'One-click integration with multiple mainstream virtualization technologies. Click a card to view the open-source project'
  },
  stats: {
    users: 'Users',
    nodes: 'Nodes',
    containers: 'Containers',
    vms: 'Virtual Machines'
  },
  announcements: {
    title: 'System Announcements',
    typeHomepage: 'Homepage Announcement',
    typeTopbar: 'Top Bar Announcement'
  },
  supporters: {
    title: 'Sponsors',
    description: 'Thanks to these groups and individuals for sponsoring OneClickVirt'
  },
  footer: {
    coreProjects: 'Core Projects',
    relatedProjects: 'Related Projects',
    moreProjects: 'More Projects',
    supportAndDocs: 'Support',
    supporters: 'Sponsors',
    documentation: 'Documentation',
    feedback: 'Issue Feedback',
    communityGroup: 'Community Group',
    allRightsReserved: 'All rights reserved.',
    openSourceProject: 'Open Source Projects by OneClickVirt',
    serverVersion: 'Server',
    latestVersion: 'Latest',
    versionFetchFailed: 'Version unavailable, backend may be disconnected',
    manageUpdates: 'Manage updates',
    updateDialogTitle: 'Controller version management',
    currentVersion: 'Current version',
    deploymentMode: 'Deployment mode',
    deploymentFlavor: 'Release flavor',
    updateTab: 'Update',
    rollbackTab: 'Rollback',
    commandsTab: 'Commands',
    selectVersion: 'Select target version',
    selectRollbackVersion: 'Select a rollback release or local backup',
    assetUnavailable: 'Assets unavailable',
    localBackup: 'Local backup',
    updateNow: 'Update now',
    rollbackNow: 'Rollback now',
    restartNow: 'Restart controller',
    noCommands: 'No manual commands are available for this deployment',
    destructiveCommand: 'Changes runtime files',
    copyCommand: 'Copy command',
    updateLoadFailed: 'Failed to load update information',
    operationRunning: 'Operation in progress',
    reconnecting: 'Controller is restarting; waiting for the connection',
    operationSubmitted: 'Operation submitted',
    operationFailed: 'Operation failed',
    updateConfirm: 'Update to {version}? The current controller and Web files will be backed up and the service restarted.',
    rollbackConfirm: 'Rollback to {version}? Database migrations are not automatically reversed.',
    restartConfirm: 'Restart the controller service? This page will disconnect briefly.',
    rollbackWarning: 'Rollback replaces controller and Web assets only. Database migrations are normally forward-compatible; verify compatibility first.',
    rollbackDatabaseNote: 'Database migrations are not automatically reversed. Keep a database backup before production operations.'
  },
  errors: {
    fetchAnnouncementsFailed: 'Failed to fetch announcements:',
    checkInitFailed: 'Failed to check system initialization status:'
  },
  debug: {
    checkingInit: 'Checking initialization status on homepage:',
    needInitRedirect: 'System needs initialization, redirecting to init page',
    serverConnectionFailed: 'Server connection failed, may need initialization, redirecting to init page'
  }
}
