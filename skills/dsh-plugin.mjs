import { OneClickVirtService, VirtError } from './dsh-service.mjs'

const id = { type: 'integer', minimum: 1 }
const page = { page: id, pageSize: { type: 'integer', minimum: 1, maximum: 100 } }
const instance = { instance_id: id }
const spec = (name, description, properties = {}, required = [], safe = true) => ({ name, description, parameters: { type: 'object', properties, required, additionalProperties: false }, safe })
export const operations = [
  spec('list_instances', 'List instances accessible to the signed-in account. Search uses the original host name, not the display alias.', { ...page, name: { type: 'string' }, status: { type: 'string' } }),
  spec('create_instance', 'Create a VM or container only when requested by the user. Read list_providers and catalog first. Administrators use image/cpu/memory/disk; users use catalog image_id and the four specification IDs. This queues work: inspect tasks before claiming success or retrying.', { provider_id: id, instance_type: { type: 'string', enum: ['container', 'vm'] }, image: { type: 'string' }, cpu: id, memory: id, disk: id, bandwidth: id, user_id: id, name: { type: 'string' }, description: { type: 'string' }, network_type: { type: 'string', enum: ['nat_ipv4', 'nat_ipv4_ipv6', 'dedicated_ipv4', 'dedicated_ipv4_ipv6', 'ipv6_only', 'no_port_mapping'] }, image_id: id, cpu_id: { type: 'string' }, memory_id: { type: 'string' }, disk_id: { type: 'string' }, bandwidth_id: { type: 'string' } }, ['provider_id', 'instance_type'], false),
  ...['start', 'stop', 'restart'].map(action => spec(action + '_instance', action + ' the explicitly selected instance. The API queues work; inspect tasks and reread instance state.', instance, ['instance_id'], false)),
  spec('delete_instance', 'Permanently delete the exact user-requested instance. Confirm the identity and user intent before setting confirm=true. Never delete an instance merely to troubleshoot it.', { ...instance, confirm: { type: 'boolean', const: true } }, ['instance_id', 'confirm'], false),
  spec('get_instance_detail', 'Read instance details. Passwords, private keys and platform credentials are redacted.', instance, ['instance_id']),
  spec('list_providers', 'List provider nodes visible to this account. Ordinary users receive only their available providers.', page),
  spec('health_check', 'Run an administrator-authorized health check on a provider node.', { provider_id: id }, ['provider_id'], false),
  spec('get_instance_logs', 'Read diagnostic instance state and task errors. The current HTTP API does not expose guest logs; available=false explicitly reports that limit.', { ...instance, lines: id }, ['instance_id']),
  spec('get_system_status', 'Read dashboard status and resource totals for the current account.'),
  spec('get_metrics', 'Read system, provider or instance monitoring. Ordinary users can read their own instances; provider metrics require an administrator.', { ...instance, provider_id: id, hours: { type: 'integer', minimum: 1, maximum: 168 } }),
  spec('instance_alias', 'Update the selected instance display alias; empty clears it. The original host name and search behavior stay unchanged.', { ...instance, alias: { type: 'string', maxLength: 128 } }, ['instance_id', 'alias'], false),
  spec('catalog', 'Read provider images and instance specification IDs before creating a VM or container.', { provider_id: id, instance_type: { type: 'string', enum: ['container', 'vm'] } }, ['provider_id']),
  spec('tasks', 'Read accepted task status, progress and errors. An accepted task is not proof that VM creation or a power action completed.', { ...page, ...instance }),
  spec('resource', 'Read a resource from the OneClickVirt Skill. Configuration is administrator-only and credentials are redacted.', { uri: { type: 'string', enum: ['oneclickvirt://instances/list', 'oneclickvirt://providers/list', 'oneclickvirt://system/status', 'oneclickvirt://health/status', 'oneclickvirt://config/system'] } }, ['uri']),
]

export const guide = () => ({
  login: 'Sign in from the OneClickVirt sidebar page. Passwords and tokens never belong in chat or tool arguments. CLI/CI can use ONE_CLICK_VIRT_API_URL and ONE_CLICK_VIRT_API_TOKEN.',
  steps: ['Read list_providers, then catalog for the selected provider and instance type.', 'Create with explicit resource choices from that catalog. Administrators use numeric resources; ordinary users use specification IDs.', 'Read tasks and instance details after accepted mutations. Never repeat an interrupted create/delete without checking server state.', 'Use the same explicit instance ID for power actions, metrics and display aliases. Only delete when the user has asked to remove that exact instance.'],
  templates: {
    create_debian_container: { provider_id: '<from list_providers>', instance_type: 'container', image: '<Debian image from catalog>', cpu: 2, memory: 1024, disk: 20 },
    create_ubuntu_vm: { provider_id: '<from list_providers>', instance_type: 'vm', image: '<Ubuntu image from catalog>', cpu: 2, memory: 2048, disk: 20 },
    troubleshoot_instance: ['get_instance_detail', 'get_metrics', 'tasks', 'health_check (administrator)'],
    quick_status_check: ['get_system_status', 'list_instances', 'list_providers', 'resource: oneclickvirt://health/status'],
  },
})

export function validateArguments(schema, args) {
  if (!args || typeof args !== 'object' || Array.isArray(args) || Object.keys(args).some(key => !Object.hasOwn(schema.properties, key))) throw new VirtError('INVALID_ARGUMENTS', 'Invalid tool arguments')
  for (const key of schema.required) if (!Object.hasOwn(args, key)) throw new VirtError('INVALID_ARGUMENTS', 'Missing ' + key)
  for (const [key, value] of Object.entries(args)) {
    const item = schema.properties[key]
    if ((item.type === 'integer' && (!Number.isSafeInteger(value) || value < (item.minimum || 0) || value > (item.maximum || Number.MAX_SAFE_INTEGER))) || (item.type === 'string' && (typeof value !== 'string' || (item.maxLength && Array.from(value).length > item.maxLength))) || (item.type === 'boolean' && typeof value !== 'boolean') || (item.enum && !item.enum.includes(value)) || (Object.hasOwn(item, 'const') && item.const !== value)) throw new VirtError('INVALID_ARGUMENTS', 'Invalid ' + key)
  }
}

export function apply(ctx, config = {}) {
  if (typeof ctx?.inject !== 'function' || typeof ctx?.effect !== 'function') throw new VirtError('DSH_INCOMPATIBLE', 'Use a DSH version with the native plugin lifecycle API')
  const service = new OneClickVirtService(config)
  ctx.effect(() => () => service.dispose())
  ctx.inject(['credentials'], scope => {
    if (typeof scope.credentials?.readRecord === 'function' && typeof scope.credentials?.modifyRecord === 'function') service.store.attach(scope.credentials).catch(() => {})
  })
  ctx.inject(['connection'], scope => {
    if (typeof scope.connection?.fetch?.register !== 'function') return
    service.host.connection = true
    scope.connection.fetch.register({ path: '/api/oneclickvirt.plugin', methods: ['POST'], requestBody: 'buffered', async fetch(request) {
      try {
        const text = await request.text()
        if (text.length > 65536) throw new VirtError('REQUEST_LIMIT', 'Request exceeds 64 KiB')
        const input = JSON.parse(text)
        let data
        if (input.action === 'status') data = await service.status(request.signal)
        else if (input.action === 'login') data = await service.login(input, request.signal)
        else if (input.action === 'logout') data = await service.logout(request.signal)
        else if (input.action === 'captcha') data = await service.captcha(input, request.signal)
        else if (input.action === 'call') {
          const operation = operations.find(item => item.name === input.name)
          if (!operation) throw new VirtError('UNKNOWN_OPERATION', 'Unknown operation')
          validateArguments(operation.parameters, input.args || {})
          data = await service.call(input.name, input.args || {}, request.signal)
        } else throw new VirtError('UNKNOWN_OPERATION', 'Unknown plugin operation')
        return Response.json({ data }, { headers: { 'Cache-Control': 'no-store' } })
      } catch (error) {
        return Response.json({ error: { code: error instanceof VirtError ? error.code : 'PLUGIN_ERROR', message: error instanceof VirtError ? error.message : 'The operation failed; check deployment connectivity and plugin credential storage' } }, { status: error instanceof VirtError ? error.status : 502, headers: { 'Cache-Control': 'no-store' } })
      }
    } })
  })
  ctx.inject(['tools'], scope => {
    if (typeof scope.tools?.register !== 'function') return
    service.host.tools = true
    for (const operation of [...operations, spec('guide', 'Read the OneClickVirt workflow, account scope and creation examples before managing instances.')]) scope.tools.register({
      name: 'oneclickvirt_' + operation.name, description: operation.description,
      parameters: operation.parameters,
      output: { schema: {}, render: (_args, value) => [{ type: 'text', text: JSON.stringify(value) }] },
      isConcurrencySafe: () => operation.safe,
      execute: async (args, exec = {}) => {
        validateArguments(operation.parameters, args)
        if (exec.signal?.aborted) throw exec.signal.reason
        return operation.name === 'guide' ? guide() : service.call(operation.name, args, exec.signal)
      },
    })
  })
}
