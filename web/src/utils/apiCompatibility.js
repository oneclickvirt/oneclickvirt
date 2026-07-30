import { ElMessageBox } from 'element-plus'
import i18n from '@/i18n'
import { isApiContractMismatch, isUnmatchedApiRoute } from './apiCompatibilityCore'

export { expectedApiContract, isApiContractMismatch, isUnmatchedApiRoute } from './apiCompatibilityCore'

let compatibilityDialogVisible = false
let lastCompatibilityWarningAt = 0

const warningCooldownMs = 5 * 60 * 1000

function showCompatibilityWarning(backendVersion, backendCommit) {
  const now = Date.now()
  if (compatibilityDialogVisible || now - lastCompatibilityWarningAt < warningCooldownMs) return

  compatibilityDialogVisible = true
  lastCompatibilityWarningAt = now
  const backendDetails = backendVersion
    ? `\n${i18n.global.t('common.backendVersion')}: ${backendVersion}${backendCommit ? ` (${backendCommit})` : ''}`
    : ''

  ElMessageBox.alert(
    `${i18n.global.t('common.apiVersionMismatchMessage')}${backendDetails}`,
    i18n.global.t('common.apiVersionMismatchTitle'),
    {
      type: 'error',
      confirmButtonText: i18n.global.t('common.confirm'),
      closeOnClickModal: false
    }
  ).finally(() => {
    compatibilityDialogVisible = false
  })
}

export function notifyApiVersionMismatch(error) {
  if (!isUnmatchedApiRoute(error)) return
  const data = error?.response?.data
  showCompatibilityWarning(
    data && typeof data === 'object' ? data.server_version : '',
    data && typeof data === 'object' ? data.build_commit : ''
  )
}

export function notifyApiContractMismatch(buildInfo) {
  if (!isApiContractMismatch(buildInfo)) return
  const data = buildInfo?.data && typeof buildInfo.data === 'object' ? buildInfo.data : buildInfo
  showCompatibilityWarning(data?.version || '', data?.commit || '')
}
