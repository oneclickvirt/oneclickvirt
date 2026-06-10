export const PROVIDER_TYPE_ORDER = [
  'proxmox',
  'incus',
  'docker',
  'lxd',
  'podman',
  'containerd',
  'qemu',
  'kubevirt',
  'vmware',
  'orbstack',
  'virtualbox',
  'multipass',
  'vagrant'
]

export const CONTAINER_ONLY_PROVIDER_TYPES = ['docker', 'podman', 'containerd', 'orbstack']

export const VM_ONLY_PROVIDER_TYPES = ['vmware', 'virtualbox', 'multipass', 'vagrant']

export const STORAGE_POOL_PROVIDER_TYPES = ['proxmox', 'lxd', 'incus', 'qemu', 'kubevirt', ...VM_ONLY_PROVIDER_TYPES]

export const COPY_CAPABLE_PROVIDER_TYPES = ['lxd', 'incus', ...CONTAINER_ONLY_PROVIDER_TYPES]

export const PROVIDER_TYPE_OPTIONS = PROVIDER_TYPE_ORDER.map((value) => ({
  value,
  labelKey: `admin.providers.${value}`
}))

export const isContainerOnlyProvider = (type) => CONTAINER_ONLY_PROVIDER_TYPES.includes(type)

export const isVMOnlyProvider = (type) => VM_ONLY_PROVIDER_TYPES.includes(type)

export const isCopyCapableProvider = (type) => COPY_CAPABLE_PROVIDER_TYPES.includes(type)

export const isContainerGPUProvider = (type) => COPY_CAPABLE_PROVIDER_TYPES.includes(type)
