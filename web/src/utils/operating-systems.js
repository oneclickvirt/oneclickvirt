// 操作系统数据；name 必须与后端 osType 规范化值保持一致。
export const operatingSystems = [
  // Linux 发行版
  { name: 'ubuntu', displayName: 'Ubuntu', category: 'Linux', color: '#E95420', abbr: 'U', icon: 'fl-ubuntu' },
  { name: 'debian', displayName: 'Debian', category: 'Linux', color: '#A80030', abbr: 'D', icon: 'fl-debian' },
  { name: 'alpine', displayName: 'Alpine Linux', category: 'Linux', color: '#0D597F', abbr: 'A', icon: 'fl-alpine' },
  { name: 'archlinux', displayName: 'Arch Linux', category: 'Linux', color: '#1793D1', abbr: 'Ar', icon: 'fl-archlinux' },
  { name: 'rockylinux', displayName: 'Rocky Linux', category: 'Linux', color: '#10B981', abbr: 'R', icon: 'fl-rocky-linux' },
  { name: 'almalinux', displayName: 'AlmaLinux', category: 'Linux', color: '#0F4266', abbr: 'AL', icon: 'fl-almalinux' },
  { name: 'openeuler', displayName: 'openEuler', category: 'Linux', color: '#4F46E5', abbr: 'OE', icon: 'fl-tux' },
  { name: 'openwrt', displayName: 'OpenWrt', category: 'Linux', color: '#00B5E2', abbr: 'OW', icon: 'fl-tux' },
  { name: 'opensuse', displayName: 'openSUSE', category: 'Linux', color: '#73BA25', abbr: 'oS', icon: 'fl-opensuse' },
  { name: 'oracle', displayName: 'Oracle Linux', category: 'Linux', color: '#F80000', abbr: 'O', icon: 'fl-tux' },
  { name: 'fedora', displayName: 'Fedora', category: 'Linux', color: '#51A2DA', abbr: 'F', icon: 'fl-fedora' },
  { name: 'centos', displayName: 'CentOS', category: 'Linux', color: '#932279', abbr: 'C', icon: 'fl-centos' },
  { name: 'gentoo', displayName: 'Gentoo', category: 'Linux', color: '#54487A', abbr: 'G', icon: 'fl-gentoo' },
  { name: 'kali', displayName: 'Kali Linux', category: 'Linux', color: '#557C94', abbr: 'K', icon: 'fl-kali-linux' },
  // BSD 系统
  { name: 'freebsd', displayName: 'FreeBSD', category: 'BSD', color: '#AB2B28', abbr: 'FB', icon: 'fl-freebsd' },
  { name: 'openbsd', displayName: 'OpenBSD', category: 'BSD', color: '#F2CA30', abbr: 'OB', icon: 'fl-openbsd' },
  { name: 'netbsd', displayName: 'NetBSD', category: 'BSD', color: '#FF6600', abbr: 'NB', icon: 'fl-tux' },
  // 商业/桌面系统
  { name: 'windows', displayName: 'Windows', category: 'Desktop/Server', color: '#0078D4', abbr: 'Win', icon: 'fl-windows' },
  { name: 'macos', displayName: 'macOS', category: 'Desktop/Server', color: '#6B7280', abbr: 'mac', icon: 'fl-apple' },
  // 其他系统
  { name: 'unknown', displayName: 'Unknown', category: 'Other', color: '#909399', abbr: '?', icon: 'fl-tux' },
  { name: 'other', displayName: 'Other', category: 'Other', color: '#909399', abbr: '?', icon: 'fl-tux' }
]

export const osAliases = {
  arch: 'archlinux',
  archlinux: 'archlinux',
  rocky: 'rockylinux',
  rockylinux: 'rockylinux',
  almalinux: 'almalinux',
  alma: 'almalinux',
  openeuler: 'openeuler',
  openwrt: 'openwrt',
  opensuse: 'opensuse',
  sles: 'opensuse',
  suse: 'opensuse',
  oraclelinux: 'oracle',
  oracle: 'oracle',
  win: 'windows',
  windows: 'windows',
  win10: 'windows',
  win11: 'windows',
  winserver: 'windows',
  mac: 'macos',
  macos: 'macos',
  osx: 'macos',
  darwin: 'macos'
}

export const normalizeOperatingSystemName = (name) => {
  if (!name) return ''
  const lower = String(name).trim().toLowerCase().replace(/[_.\s/\\:]+/g, '-').replace(/-+/g, '-')
  if (!lower) return ''
  if (osAliases[lower]) return osAliases[lower]
  const direct = operatingSystems.find(os => os.name === lower)
  if (direct) return direct.name
  const alias = Object.entries(osAliases).find(([key]) => lower === key || lower.startsWith(`${key}-`) || lower.includes(`-${key}-`) || lower.endsWith(`-${key}`))
  if (alias) return alias[1]
  const matched = operatingSystems.find(os => !['other', 'unknown'].includes(os.name) && (lower === os.name || lower.includes(os.name)))
  return matched?.name || lower
}

// 根据分类获取操作系统
export const getOperatingSystemsByCategory = () => {
  const grouped = {}
  operatingSystems.forEach(os => {
    if (!grouped[os.category]) grouped[os.category] = []
    grouped[os.category].push(os)
  })
  return grouped
}

// 根据名称获取操作系统信息
export const getOperatingSystemByName = (name) => {
  const normalized = normalizeOperatingSystemName(name)
  return operatingSystems.find(os => os.name === normalized)
}

// 获取所有操作系统名称列表
export const getAllOperatingSystemNames = () => operatingSystems.map(os => os.name)

// 获取显示名称
export const getDisplayName = (name) => {
  const os = getOperatingSystemByName(name)
  return os ? os.displayName : name
}

// 从镜像名称或OS类型智能匹配OS信息
export const matchOperatingSystem = (imageStr) => {
  if (!imageStr) return null
  const normalized = normalizeOperatingSystemName(imageStr)
  return getOperatingSystemByName(normalized) || operatingSystems.find(os => os.name === 'unknown')
}
