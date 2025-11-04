// 操作系统数据
export const operatingSystems = [
  // Linux 发行版
  { name: 'ubuntu', displayName: 'Ubuntu', category: 'Linux' },
  { name: 'debian', displayName: 'Debian', category: 'Linux' },
  { name: 'centos', displayName: 'CentOS', category: 'Linux' },
  { name: 'rhel', displayName: 'Red Hat Enterprise Linux', category: 'Linux' },
  { name: 'fedora', displayName: 'Fedora', category: 'Linux' },
  { name: 'opensuse', displayName: 'openSUSE', category: 'Linux' },
  { name: 'alpine', displayName: 'Alpine Linux', category: 'Linux' },
  { name: 'arch', displayName: 'Arch Linux', category: 'Linux' },
  { name: 'mint', displayName: 'Linux Mint', category: 'Linux' },
  { name: 'kali', displayName: 'Kali Linux', category: 'Linux' },
  { name: 'rocky', displayName: 'Rocky Linux', category: 'Linux' },
  { name: 'almalinux', displayName: 'AlmaLinux', category: 'Linux' },
  { name: 'oracle', displayName: 'Oracle Linux', category: 'Linux' },
  { name: 'amazonlinux', displayName: 'Amazon Linux', category: 'Linux' },
  { name: 'sles', displayName: 'SUSE Linux Enterprise Server', category: 'Linux' },
  { name: 'gentoo', displayName: 'Gentoo', category: 'Linux' },
  { name: 'void', displayName: 'Void Linux', category: 'Linux' },
  { name: 'nixos', displayName: 'NixOS', category: 'Linux' },
  // BSD 系统
  { name: 'freebsd', displayName: 'FreeBSD', category: 'BSD' },
  { name: 'openbsd', displayName: 'OpenBSD', category: 'BSD' },
  { name: 'netbsd', displayName: 'NetBSD', category: 'BSD' },
  // 其他系统
  { name: 'other', displayName: '其他', category: 'Other' }
]

// 根据分类获取操作系统
export const getOperatingSystemsByCategory = () => {
  const grouped = {}
  operatingSystems.forEach(os => {
    if (!grouped[os.category]) {
      grouped[os.category] = []
    }
    grouped[os.category].push(os)
  })
  return grouped
}

// 根据名称获取操作系统信息
export const getOperatingSystemByName = (name) => {
  return operatingSystems.find(os => os.name === name)
}

// 获取所有操作系统名称列表
export const getAllOperatingSystemNames = () => {
  return operatingSystems.map(os => os.name)
}

// 获取显示名称
export const getDisplayName = (name) => {
  const os = getOperatingSystemByName(name)
  return os ? os.displayName : name
}
