// Match the server policy when loading, editing and submitting legacy PVE
// nodes. Watching the methods too prevents async form assignment restoring an
// unsupported device_proxy after the network-type watcher has already run.
export function normalizePortMappingMethods(type, networkType, ipv4 = '', ipv6 = '') {
  const provider = String(type || '').trim().toLowerCase()
  ipv4 = String(ipv4 || '').trim().toLowerCase()
  ipv6 = String(ipv6 || '').trim().toLowerCase()
  if (['docker', 'podman', 'containerd', 'orbstack'].includes(provider)) {
    return { ipv4PortMappingMethod: 'native', ipv6PortMappingMethod: 'native' }
  }
  if (['proxmox', 'proxmoxve'].includes(provider)) {
    if (['', 'nat_ipv4', 'nat_ipv4_ipv6'].includes(networkType || '')) ipv4 = 'iptables'
    else if (!['native', 'iptables'].includes(ipv4)) ipv4 = 'native'
    if (!['native', 'iptables'].includes(ipv6)) ipv6 = 'native'
  }
  return { ipv4PortMappingMethod: ipv4, ipv6PortMappingMethod: ipv6 }
}
