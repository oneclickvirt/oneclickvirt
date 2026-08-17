import { ElMessageBox } from 'element-plus'
import { isUnmatchedApiRoute } from '@/utils/apiCompatibilityCore'
import { ipv6TunnelErrorMessage, ipv6TunnelErrorStatus } from './ipv6TunnelErrorCore'

export { ipv6TunnelErrorMessage } from './ipv6TunnelErrorCore'

export async function showIPv6TunnelError(error, t, operation) {
  const data = error?.response?.data && typeof error.response.data === 'object' ? error.response.data : {}
  const status = ipv6TunnelErrorStatus(error)
  let message

  if (status === 404 && isUnmatchedApiRoute(error)) {
    const contract = data.api_contract ? ` (${data.api_contract})` : ''
    message = t('admin.providers.ipv6Pool.tunnelApiMismatchDetail', {
      operation,
      contract
    })
  } else {
    const details = ipv6TunnelErrorMessage(
      error,
      t('common.operationFailed'),
      t('admin.providers.ipv6Pool.tunnelProxyError', { status })
    )
    message = t('admin.providers.ipv6Pool.tunnelOperationErrorDetail', {
      operation,
      details
    })
  }

  try {
    await ElMessageBox.alert(message, t('admin.providers.ipv6Pool.tunnelOperationErrorTitle'), {
      type: 'error',
      confirmButtonText: t('common.confirm'),
      closeOnClickModal: false,
      customClass: 'ipv6-tunnel-error-dialog'
    })
  } catch {
    // Closing a diagnostic dialog is not an operation failure.
  }
}
