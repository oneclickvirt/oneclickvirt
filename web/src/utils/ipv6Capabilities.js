const STATIC_IPV6_PROVIDER_TYPES = new Set([
  'lxd',
  'incus',
  'proxmox',
  'proxmoxve',
  'docker',
  'podman',
  'containerd',
  'orbstack',
  'qemu',
  'kubevirt',
  'vmware',
  'virtualbox',
  'multipass',
  'vagrant'
])

const ROUTED_STATIC_IPV6_PROVIDER_TYPES = new Set([
  'qemu',
  'kubevirt',
  'vmware',
  'virtualbox',
  'multipass',
  'vagrant'
])

// Multipass and Vagrant need their IPv4 management interface during launch
// and provisioning. They still support NAT IPv4 plus an independent routed
// IPv6 NIC, but cannot honestly offer a strict IPv6-only guest.
const IPV6_ONLY_UNSUPPORTED_PROVIDER_TYPES = new Set(['multipass', 'vagrant'])

const normalizeProviderType = providerType => String(providerType || '').trim().toLowerCase()

// Keep this matrix aligned with server/service/ipv6pool.SupportsStaticIPv6.
export const supportsStaticIPv6Provider = providerType => STATIC_IPV6_PROVIDER_TYPES.has(normalizeProviderType(providerType))

// These VM backends require a tunnel-managed prefix, gateway, and bridge.
export const requiresRoutedStaticIPv6Provider = providerType => ROUTED_STATIC_IPV6_PROVIDER_TYPES.has(normalizeProviderType(providerType))

export const supportsIPv6OnlyProvider = providerType => !IPV6_ONLY_UNSUPPORTED_PROVIDER_TYPES.has(normalizeProviderType(providerType))
