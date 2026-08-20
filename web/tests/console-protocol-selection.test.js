import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const component = readFileSync(
  resolve(process.cwd(), 'src/components/VNCDialog.vue'),
  'utf8'
)
const consoleApi = readFileSync(resolve(process.cwd(), 'src/api/console.js'), 'utf8')
const terminal = readFileSync(resolve(process.cwd(), 'src/components/ConsoleTerminal.vue'), 'utf8')
const capabilityNormalizer = readFileSync(resolve(process.cwd(), 'src/utils/consoleCapabilities.js'), 'utf8')
const adminInstances = readFileSync(resolve(process.cwd(), 'src/view/admin/instances/index.vue'), 'utf8')
const instanceDetail = readFileSync(resolve(process.cwd(), 'src/view/user/instances/detail.vue'), 'utf8')
const overviewCard = readFileSync(resolve(process.cwd(), 'src/view/user/instances/components/InstanceOverviewCard.vue'), 'utf8')

test('console capability loading waits for an explicit protocol choice', () => {
  const match = component.match(/async function loadConsoleInfo\(\) \{([\s\S]*?)\n}\n\nasync function connectVNC/)
  assert.ok(match, 'loadConsoleInfo body should be available')
  assert.doesNotMatch(match[1], /connectVNC\(/)
  assert.doesNotMatch(match[1], /repairConsole\(/)
  assert.match(component, /selectedProtocol\.value = capabilities\.value\.length === 1/)
  assert.match(component, /@click="selectProtocol\(capability\.protocol\)"/)
})

test('legacy protocol lists still expose every advertised console choice', () => {
  assert.match(component, /normalizeConsoleCapabilities/)
  assert.match(capabilityNormalizer, /const listedProtocols = Array\.isArray\(info\?\.protocols\)/)
  assert.match(capabilityNormalizer, /add\(info\?\.protocol, true\)/)
  assert.match(component, /selectedProtocol\.value = capabilities\.value\.length === 1/)
})

test('selecting VNC is the action that starts noVNC', () => {
  const match = component.match(/async function selectProtocol\(protocol\) \{([\s\S]*?)\n}\n\nasync function repairConsole/)
  assert.ok(match, 'selectProtocol body should be available')
  assert.match(match[1], /protocol === 'vnc' && capability\.available/)
  assert.match(match[1], /await connectVNC\(\)/)
  assert.doesNotMatch(match[1], /repairConsole\(/)
})

test('switching between terminal protocols creates a fresh console session', () => {
  assert.match(component, /<ConsoleTerminal\s+[^>]*:key="selectedProtocol"/)
})

test('serial terminals suppress xterm cursor-position replies before forwarding input', () => {
  assert.match(terminal, /createSerialConsoleInputFilter/)
  assert.match(terminal, /filterSerialInput\(data\)/)
  assert.match(terminal, /function sendTerminalInput\(data\)/)
  assert.match(terminal, /if \(text !== null\) sendTerminalInput\(text\)/)
  assert.match(terminal, /terminal\.onData\(data => \{\s*sendTerminalInput\(data\)/)
  assert.match(terminal, /terminal\.parser\.registerCsiHandler\(\{ final: 'n' \}/)
  assert.match(terminal, /status === 5 \|\| status === 6/)
  assert.match(terminal, /schedulePendingSerialInputFlush\(\)/)
})

test('admin console mounts with an already-open dialog and still loads capabilities', () => {
  assert.match(component, /watch\(\(\) => props\.modelValue, async visible =>/)
  assert.match(component, /\}, \{ immediate: true \}\)/)
})

test('selected VNC and SPICE failures keep the server diagnostic visible', () => {
  assert.match(component, /function reportConsoleError\(message, attempt = connectionAttempt\)/)
  assert.match(component, /event\.detail\?\.reason \|\| event\.detail\?\.message \|\| t\('user\.instanceDetail\.vncDisconnected'\)/)
  assert.match(component, /async function prepareSPICE\(\)/)
  assert.match(component, /fetch\(spiceAssetUrl\.value/)
  assert.match(component, /v-if="selectedCapability\?\.available && status === 'error' && statusMessage"/)
})

test('console clients select their endpoint scope without changing Web SSH', () => {
  assert.match(consoleApi, /\/v1\/admin\/instances\/\$\{encodeURIComponent\(instanceId\)\}\/console/)
  assert.match(consoleApi, /\/v1\/public\/instance-shares\/\$\{encodeURIComponent\(shareToken \|\| ''\)\}\/console/)
	assert.doesNotMatch(consoleApi, /sessionStorage\.getItem\('token'\)/)
	assert.doesNotMatch(consoleApi, /token=\$\{encodeURIComponent/)
  assert.match(component, /scope: \{ type: String, default: 'user' \}/)
  assert.match(terminal, /props\.scope !== 'share' && !token/)

  const sshSection = overviewCard.match(/<!-- Web SSH按钮 -->[\s\S]*?<\/el-button>/)
  assert.ok(sshSection, 'Web SSH section should remain present')
  assert.match(sshSection[0], /!instance\.hasSshMapping && instance\.networkType === 'no_port_mapping'/)
})

test('admin and shared views expose the same protocol chooser', () => {
  assert.match(adminInstances, /@click="openConsole\(scope\.row\)"/)
  assert.match(adminInstances, /scope="admin"/)
  assert.doesNotMatch(instanceDetail, /<VNCDialog\s+v-if="!isShareMode"/)
  assert.match(instanceDetail, /:scope="isShareMode \? 'share' : 'user'"/)
  assert.match(overviewCard, /v-if="instance\.status === 'running'"/)
})
