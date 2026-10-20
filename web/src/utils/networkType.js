export const NETWORK_TYPE_KEYS = {
  nat_ipv4: 'natIpv4',
  nat_ipv4_ipv6: 'natIpv4Ipv6',
  dedicated_ipv4: 'dedicatedIpv4',
  dedicated_ipv4_ipv6: 'dedicatedIpv4Ipv6',
  ipv6_only: 'ipv6Only',
  no_port_mapping: 'noPortMapping'
}

// Missing policy on an older server means the default only. Never infer that
// every mode is available merely because the frontend recognizes its name.
export function selectableNetworkTypes(provider = {}) {
  const defaultType = provider.networkType || 'nat_ipv4'
  const configured = Array.isArray(provider.allowedNetworkTypes) ? provider.allowedNetworkTypes : []
  return [...new Set([defaultType, ...configured])].filter(value => Object.hasOwn(NETWORK_TYPE_KEYS, value))
}

export function selectProviderNetworkType(provider, currentType, previousProviderId) {
  if (!provider?.id) return ''
  const choices = selectableNetworkTypes(provider)
  if (String(provider.id) !== String(previousProviderId) || !choices.includes(currentType)) {
    return choices[0] || ''
  }
  return currentType
}

// Resolve the effective network type for legacy/imported instances whose
// snapshot may not contain the provider setting yet.
export function resolveInstanceNetworkType(instance, providers = []) {
  const instanceType = String(instance?.networkType || '').trim().toLowerCase()
  if (instanceType) return instanceType
  const providerId = instance?.providerId
  const provider = providers.find(item => String(item?.id) === String(providerId))
  return String(provider?.networkType || '').trim().toLowerCase()
}

export function hasAgentMappedNetworking(formData = {}) {
  if (String(formData.connectionType || '').trim().toLowerCase() !== 'agent') return false
  const networkType = String(formData.networkType || '').trim().toLowerCase()
  return networkType !== '' && networkType !== 'no_port_mapping'
}

// Incus/LXD retain two explicit dual-stack modes. device_proxy/iptables keep
// the guest on the managed bridge and publish the node's IPv6, while native
// consumes a controller allocation and attaches it directly to the guest.
export function usesControllerIPv6Pool(providerType, networkType, ipv6PortMappingMethod = 'device_proxy') {
  const provider = String(providerType || '').trim().toLowerCase()
  const network = String(networkType || '').trim().toLowerCase()
  const method = String(ipv6PortMappingMethod || 'device_proxy').trim().toLowerCase()
  if (!['nat_ipv4_ipv6', 'dedicated_ipv4_ipv6', 'ipv6_only'].includes(network)) return false
  return network !== 'nat_ipv4_ipv6' || !['incus', 'lxd'].includes(provider) || method === 'native'
}

export function usesManagedIPv6NAT(providerType, networkType, ipv6PortMappingMethod = 'device_proxy') {
  const provider = String(providerType || '').trim().toLowerCase()
  const network = String(networkType || '').trim().toLowerCase()
  const method = String(ipv6PortMappingMethod || 'device_proxy').trim().toLowerCase()
  return ['incus', 'lxd'].includes(provider) && network === 'nat_ipv4_ipv6' && method !== 'native'
}
