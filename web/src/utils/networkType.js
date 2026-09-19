// Resolve the effective network type for legacy/imported instances whose
// snapshot may not contain the provider setting yet.
export function resolveInstanceNetworkType(instance, providers = []) {
  const instanceType = String(instance?.networkType || '').trim().toLowerCase()
  if (instanceType) return instanceType
  const providerId = instance?.providerId
  const provider = providers.find(item => String(item?.id) === String(providerId))
  return String(provider?.networkType || '').trim().toLowerCase()
}

// Incus/LXD dual-stack NAT keeps each guest on the managed bridge and exposes
// it through the node's public IPv6. A controller pool is therefore not an
// input for that one combination; other IPv6 modes still use routed/static
// allocations where the provider supports them.
export function usesControllerIPv6Pool(providerType, networkType) {
  const provider = String(providerType || '').trim().toLowerCase()
  const network = String(networkType || '').trim().toLowerCase()
  if (!['nat_ipv4_ipv6', 'dedicated_ipv4_ipv6', 'ipv6_only'].includes(network)) return false
  return network !== 'nat_ipv4_ipv6' || !['incus', 'lxd'].includes(provider)
}

export function usesManagedIPv6NAT(providerType, networkType) {
  const provider = String(providerType || '').trim().toLowerCase()
  const network = String(networkType || '').trim().toLowerCase()
  return ['incus', 'lxd'].includes(provider) && network === 'nat_ipv4_ipv6'
}
