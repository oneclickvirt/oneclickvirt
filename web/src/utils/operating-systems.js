// 操作系统数据
export const operatingSystems = [
  // Linux 发行版
  { name: 'ubuntu', displayName: 'Ubuntu', category: 'Linux', color: '#E95420', abbr: 'U' },
  { name: 'debian', displayName: 'Debian', category: 'Linux', color: '#A80030', abbr: 'D' },
  { name: 'centos', displayName: 'CentOS', category: 'Linux', color: '#932279', abbr: 'C' },
  { name: 'rhel', displayName: 'Red Hat Enterprise Linux', category: 'Linux', color: '#EE0000', abbr: 'RH' },
  { name: 'fedora', displayName: 'Fedora', category: 'Linux', color: '#51A2DA', abbr: 'F' },
  { name: 'opensuse', displayName: 'openSUSE', category: 'Linux', color: '#73BA25', abbr: 'oS' },
  { name: 'alpine', displayName: 'Alpine Linux', category: 'Linux', color: '#0D597F', abbr: 'A' },
  { name: 'arch', displayName: 'Arch Linux', category: 'Linux', color: '#1793D1', abbr: 'Ar' },
  { name: 'mint', displayName: 'Linux Mint', category: 'Linux', color: '#87CF3E', abbr: 'M' },
  { name: 'kali', displayName: 'Kali Linux', category: 'Linux', color: '#557C94', abbr: 'K' },
  { name: 'rocky', displayName: 'Rocky Linux', category: 'Linux', color: '#10B981', abbr: 'R' },
  { name: 'almalinux', displayName: 'AlmaLinux', category: 'Linux', color: '#0F4266', abbr: 'AL' },
  { name: 'oracle', displayName: 'Oracle Linux', category: 'Linux', color: '#F80000', abbr: 'O' },
  { name: 'amazonlinux', displayName: 'Amazon Linux', category: 'Linux', color: '#FF9900', abbr: 'Am' },
  { name: 'sles', displayName: 'SUSE Linux Enterprise Server', category: 'Linux', color: '#0C322C', abbr: 'SL' },
  { name: 'gentoo', displayName: 'Gentoo', category: 'Linux', color: '#54487A', abbr: 'G' },
  { name: 'void', displayName: 'Void Linux', category: 'Linux', color: '#478061', abbr: 'V' },
  { name: 'nixos', displayName: 'NixOS', category: 'Linux', color: '#7EBAE4', abbr: 'Nx' },
  // BSD 系统
  { name: 'freebsd', displayName: 'FreeBSD', category: 'BSD', color: '#AB2B28', abbr: 'FB' },
  { name: 'openbsd', displayName: 'OpenBSD', category: 'BSD', color: '#F2CA30', abbr: 'OB' },
  { name: 'netbsd', displayName: 'NetBSD', category: 'BSD', color: '#FF6600', abbr: 'NB' },
  // 其他系统
  { name: 'other', displayName: '其他', category: 'Other', color: '#909399', abbr: '?' }
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

// 从镜像名称或OS类型智能匹配OS信息
export const matchOperatingSystem = (imageStr) => {
  if (!imageStr) return null
  const lower = imageStr.toLowerCase()
  // First try exact match
  const exact = getOperatingSystemByName(lower)
  if (exact) return exact
  // Then try substring match
  return operatingSystems.find(os => lower.includes(os.name)) || null
}
