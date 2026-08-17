import test from 'node:test'
import assert from 'node:assert/strict'

import { ipv6TunnelErrorMessage } from '../src/view/admin/providers/components/formTabs/composables/ipv6TunnelErrorCore.js'

test('prefers detailed node diagnostics over a generic 502 message', () => {
  const message = ipv6TunnelErrorMessage({
    status: 502,
    userMessage: 'External API call failed',
    response: {
      status: 502,
      data: {
        code: 502,
        message: 'IPv6 tunnel node operation failed',
        details: 'route output has no src field'
      }
    }
  }, 'operation failed', 'proxy diagnostic')

  assert.equal(message, 'route output has no src field')
})

test('identifies an opaque reverse-proxy 5xx response without dumping HTML', () => {
  const message = ipv6TunnelErrorMessage({
    response: {
      status: 502,
      data: '<html><body>Bad Gateway</body></html>'
    }
  }, 'operation failed', 'HTTP 502: proxy did not provide diagnostics')

  assert.equal(message, 'HTTP 502: proxy did not provide diagnostics')
})
