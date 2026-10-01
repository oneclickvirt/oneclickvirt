// 实例详情页 - 操作与剪贴板工具
import { onBeforeUnmount, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { copyToClipboard as copyToClipboardUtil } from '@/utils/clipboard'
import { formatEndpointHostPort, formatEndpointHostForUrl } from '@/utils/endpoint'
import { normalizeShareURL, showShareLinkDialog } from '@/utils/share-link'
import { canOpenInstanceDetail, getInstanceBusyMessage, isInstanceBusy } from '@/utils/instance-status'
import {
  performInstanceAction,
  resetInstancePassword,
  getInstanceNewPassword,
  getFilteredImages,
  createUserInstanceShare,
  performSharedInstanceAction,
  resetSharedInstancePassword,
  getSharedInstanceNewPassword,
  getSharedFilteredImages
} from '@/api/user'
import { useSSHStore } from '@/pinia/modules/ssh'
import { createActionLock, createKeyedActionLock } from '@/utils/actionLock'

export function useInstanceActions(instance, monitoring, loadInstanceDetail, shareToken = '') {
  const router = useRouter()
  const { t } = useI18n()
  const sshStore = useSSHStore()

  const getShareToken = () => {
    if (typeof shareToken === 'string') return shareToken
    return shareToken?.value || ''
  }

  const actionLoading = ref(false)
  const actionLock = createActionLock()
  const shareLinkLock = createActionLock()
  const passwordPollLock = createKeyedActionLock()
  const shareLinkLoading = ref(false)
  const passwordResetPending = ref(false)
  const pendingTimeouts = new Set()
  let disposed = false

  const scheduleTimeout = (callback, delay) => {
    let timeoutId
    timeoutId = setTimeout(() => {
      pendingTimeouts.delete(timeoutId)
      if (!disposed) callback()
    }, delay)
    pendingTimeouts.add(timeoutId)
    return timeoutId
  }

  onBeforeUnmount(() => {
    disposed = true
    pendingTimeouts.forEach(timeoutId => clearTimeout(timeoutId))
    pendingTimeouts.clear()
    passwordPollLock.clear()
    actionLock.release()
    shareLinkLock.release()
  })
  const showPassword = ref(false)
  const showTrafficDetail = ref(false)

  const getErrorMessage = (error, fallback) => {
    return error?.fullMessage || error?.userMessage || error?.message || fallback
  }

  const getTrafficOperationLockMessage = () => {
    return instance.value?.trafficOperationLockMessage ||
      monitoring.trafficData?.limitReason ||
      t('user.instanceDetail.trafficLimitStartBlocked')
  }

  const ensureTrafficOperationAllowed = () => {
    if (instance.value?.trafficOperationLocked || monitoring.trafficData?.isLimited) {
      ElMessage.error(getTrafficOperationLockMessage())
      return false
    }
    return true
  }

  const ensureInstanceOperationAllowed = () => {
    if (isInstanceBusy(instance.value)) {
      ElMessage.warning(getInstanceBusyMessage(instance.value))
      return false
    }
    if (!canOpenInstanceDetail(instance.value)) {
      ElMessage.warning(t('user.instances.cannotViewDetail', { status: instance.value?.status || 'unknown' }))
      return false
    }
    return true
  }

  // Reset image selection state
  const showResetImageDialog = ref(false)
  const resetTargetId = ref(null)
  const resetImages = ref([])
  const selectedResetImage = ref('')
  const loadingResetImages = ref(false)

  const viewTaskDetail = (taskId) => {
    if (getShareToken()) {
      ElMessage.info(`${t('user.tasks.taskID')}: ${taskId}`)
      return
    }
    router.push({ path: '/user/tasks', query: { taskId } })
  }

  const loadResetImages = async () => {
    const token = getShareToken()
    const providerId = instance.value?.provider_id || instance.value?.providerId
    if (!token && !providerId) return
    loadingResetImages.value = true
    try {
      const res = token
        ? await getSharedFilteredImages(token)
        : await getFilteredImages({
          provider_id: providerId,
          instance_type: instance.value.instance_type || instance.value.instanceType
        })
      resetImages.value = res.data?.data || res.data || []
      selectedResetImage.value = instance.value.image || ''
    } catch (err) {
      console.error('Failed to load images for reset:', err)
      resetImages.value = []
    } finally {
      loadingResetImages.value = false
    }
  }

  const confirmResetWithImage = async () => {
    if (actionLoading.value) return
    if (!selectedResetImage.value) return
    if (instance.value?.id !== resetTargetId.value) {
      showResetImageDialog.value = false
      return
    }
    showResetImageDialog.value = false
    await executeReset(selectedResetImage.value)
  }

  const executeReset = async (image) => {
    if (!ensureInstanceOperationAllowed()) return
    if (!ensureTrafficOperationAllowed()) return
    if (actionLoading.value || !actionLock.tryAcquire()) return
    actionLoading.value = true
    const targetId = instance.value.id
    const token = getShareToken()
    try {
      const payload = {
        instanceId: targetId,
        action: 'reset',
        image: image || undefined
      }
      const response = token
        ? await performSharedInstanceAction(token, payload)
        : await performInstanceAction(payload)
      if (response.code === 200) {
        const actionText = t('user.instanceDetail.actionReset')
        ElMessage.success(`${actionText}${t('user.tasks.request')}${t('user.tasks.submitted')}${t('common.comma')}${t('user.tasks.processing')}${t('common.ellipsis')}`)
        ElMessage.info(t('user.instanceDetail.resetSystemNotice'))
        if (token) {
          await loadInstanceDetail()
        } else {
          router.push('/user/instances')
        }
      }
    } catch (error) {
      if (error !== 'cancel') {
        console.error('重置实例失败:', error)
        ElMessage.error(getErrorMessage(error, `${t('user.instanceDetail.actionReset')}${t('user.instances.title')}${t('common.failed')}`))
      }
    } finally {
      actionLoading.value = false
      actionLock.release()
    }
  }

  const performAction = async (action) => {
    if (showResetImageDialog.value || actionLoading.value || !actionLock.tryAcquire()) {
      ElMessage.warning(t('user.instanceDetail.operationInProgress'))
      return
    }
    actionLoading.value = true
    if (!ensureInstanceOperationAllowed()) { actionLoading.value = false; actionLock.release(); return }
    const targetId = instance.value.id
    const targetName = instance.value.name

    const actionText = {
      'start': t('user.instanceDetail.actionStart'),
      'stop': t('user.instanceDetail.actionStop'),
      'restart': t('user.instanceDetail.actionRestart'),
      'reset': t('user.instanceDetail.actionReset'),
      'delete': t('user.instanceDetail.actionDelete')
    }[action]

    const confirmText = action === 'delete'
      ? `${t('user.instanceDetail.confirm')}${t('user.instanceDetail.delete')}${t('user.instances.title')} "${targetName}" ${t('common.questionMark')}${t('user.profile.deleteConfirmNote')}`
      : `${t('user.instanceDetail.confirm')}${actionText}${t('user.instances.title')} "${targetName}" ${t('common.questionMark')}`

    if (!ensureTrafficOperationAllowed()) { actionLoading.value = false; actionLock.release(); return }

    // For reset action, show image selection dialog
    if (action === 'reset') {
      try {
        await ElMessageBox.confirm(
          confirmText,
          t('user.instanceDetail.confirmOperation'),
          {
            confirmButtonText: t('user.instanceDetail.confirm'),
            cancelButtonText: t('user.instanceDetail.cancel'),
            type: 'warning'
          }
        )
        if (instance.value?.id === targetId) {
          await loadResetImages()
          if (instance.value?.id === targetId) {
            resetTargetId.value = targetId
            showResetImageDialog.value = true
          }
        }
      } catch (error) {
        if (error !== 'cancel') {
          console.error('重置操作出错:', error)
        }
      }
      actionLoading.value = false
      actionLock.release()
      return
    }

    try {
      await ElMessageBox.confirm(
        confirmText,
        t('user.instanceDetail.confirmOperation'),
        {
          confirmButtonText: t('user.instanceDetail.confirm'),
          cancelButtonText: t('user.instanceDetail.cancel'),
          type: action === 'delete' ? 'error' : 'warning'
        }
      )
      if (instance.value?.id !== targetId) {
        actionLoading.value = false
        actionLock.release()
        return
      }

      const token = getShareToken()
      const payload = {
        instanceId: targetId,
        action
      }
      const response = token
        ? await performSharedInstanceAction(token, payload)
        : await performInstanceAction(payload)

      if (response.code === 200) {
        ElMessage.success(`${actionText}${t('user.tasks.request')}${t('user.tasks.submitted')}${t('common.comma')}${t('user.tasks.processing')}${t('common.ellipsis')}`)

        if (action === 'delete' || action === 'reset') {
          if (action === 'reset') {
            ElMessage.info(t('user.instanceDetail.resetSystemNotice'))
          }
          if (token) {
            if (action === 'delete') {
              router.push('/home')
            } else {
              await loadInstanceDetail()
            }
          } else {
            router.push('/user/instances')
          }
        } else {
          scheduleTimeout(async () => {
            try {
              await loadInstanceDetail()
            } catch (error) {
              ElMessage.error(getErrorMessage(error, t('user.instances.loadFailed')))
            } finally {
              actionLoading.value = false
              actionLock.release()
            }
          }, 3000)
        }
      } else {
        actionLoading.value = false
        actionLock.release()
      }
      if (response.code === 200 && (action === 'delete' || action === 'reset')) {
        actionLoading.value = false
        actionLock.release()
      }
    } catch (error) {
      if (error !== 'cancel') {
        console.error(`${actionText}实例失败:`, error)
        ElMessage.error(getErrorMessage(error, `${actionText}${t('user.instances.title')}${t('common.failed')}`))
      }
      actionLoading.value = false
      actionLock.release()
    }
  }

  const openSSHTerminal = () => {
    if (!instance.value.id) {
      ElMessage.error(t('user.instanceDetail.instanceNotFound'))
      return
    }
    if (!ensureInstanceOperationAllowed()) return
    if (!ensureTrafficOperationAllowed()) return
    if (instance.value.status !== 'running') {
      ElMessage.warning(t('user.instanceDetail.instanceNotRunning'))
      return
    }
    // 只有在没有SSH映射且为no_port_mapping模式时才拒绝
    if (!instance.value.hasSshMapping && instance.value.networkType === 'no_port_mapping') {
      ElMessage.warning(t('user.instanceDetail.sshNoPortMapping'))
      return
    }
    const token = getShareToken()
    const connectionKey = token ? `share-${token}` : instance.value.id
    if (!sshStore.hasConnection(connectionKey)) {
      sshStore.createConnection(instance.value.id, instance.value.name, false, 'ssh', {
        connectionKey,
        shareToken: token
      })
    } else {
      sshStore.showConnection(connectionKey)
    }
  }

  const openExecTerminal = () => {
    if (!instance.value.id) {
      ElMessage.error(t('user.instanceDetail.instanceNotFound'))
      return
    }
    if (!ensureInstanceOperationAllowed()) return
    if (!ensureTrafficOperationAllowed()) return
    if (instance.value.status !== 'running') {
      ElMessage.warning(t('user.instanceDetail.instanceNotRunning'))
      return
    }
    const token = getShareToken()
    const execKey = token ? `exec-share-${token}` : `exec-${instance.value.id}`
    if (!sshStore.hasConnection(execKey)) {
      sshStore.createConnection(instance.value.id, instance.value.name, false, 'exec', {
        connectionKey: execKey,
        shareToken: token
      })
    } else {
      sshStore.showConnection(execKey)
    }
  }

  const pollForNewPassword = (instanceId, taskId) => {
    const pollKey = `${instanceId}:${taskId}`
    if (!passwordPollLock.tryAcquire(pollKey)) return
    const token = getShareToken()
    let attempts = 0
    const maxAttempts = 20 // up to ~60 seconds (3s intervals)

    const attempt = async () => {
      attempts++
      try {
        const res = token
          ? await getSharedInstanceNewPassword(token, taskId)
          : await getInstanceNewPassword(instanceId, taskId)
        if (disposed) {
          passwordPollLock.release(pollKey)
          passwordResetPending.value = false
          return
        }
        if (res.code === 200 && res.data?.newPassword) {
          const pwd = res.data.newPassword
          if (disposed) {
            passwordPollLock.release(pollKey)
            passwordResetPending.value = false
            return
          }
          await ElMessageBox.alert(
            `<div style="word-break:break-all">${t('user.instanceDetail.newPassword')}: <strong style="user-select:all;font-family:monospace">${pwd}</strong></div>`,
            t('user.instanceDetail.resetPasswordTitle'),
            { dangerouslyUseHTMLString: true, confirmButtonText: t('user.instanceDetail.confirm') }
          )
          if (!disposed) await loadInstanceDetail()
          passwordPollLock.release(pollKey)
          passwordResetPending.value = false
          return
        }
      } catch {
        // task not ready yet or error, continue polling
      }
      if (attempts < maxAttempts) {
        scheduleTimeout(attempt, 3000)
      } else {
        ElMessage.warning(`${t('user.tasks.taskID')}: ${taskId} — ${t('user.tasks.checkProgress') || ''}${t('user.tasks.taskList') || '任务列表'}`)
        passwordPollLock.release(pollKey)
        passwordResetPending.value = false
      }
    }

    scheduleTimeout(attempt, 3000)
  }

  const showResetPasswordDialog = async () => {
    if (showResetImageDialog.value || passwordResetPending.value || actionLoading.value || !actionLock.tryAcquire()) {
      ElMessage.warning(t('user.instanceDetail.operationInProgress'))
      return
    }
    actionLoading.value = true
    if (!ensureInstanceOperationAllowed() || !ensureTrafficOperationAllowed()) {
      actionLoading.value = false
      actionLock.release()
      return
    }

    try {
      await ElMessageBox.confirm(
        `${t('user.instanceDetail.confirm')}${t('user.instanceDetail.resetPassword')}${t('user.instances.title')} "${instance.value.name}" ${t('user.instanceDetail.password')}${t('common.questionMark')}\n${t('user.tasks.system')}${t('user.tasks.willCreateTask')}${t('user.instanceDetail.resetPassword')}${t('user.tasks.operation')}${t('common.period')}`,
        t('user.instanceDetail.resetPasswordTitle'),
        {
          confirmButtonText: t('user.instanceDetail.confirm'),
          cancelButtonText: t('user.instanceDetail.cancel'),
          type: 'warning'
        }
      )

      try {
        const token = getShareToken()
        const response = token
          ? await resetSharedInstancePassword(token)
          : await resetInstancePassword(instance.value.id)
        if (response.code === 200) {
          const taskId = response.data.taskId
          ElMessage.info(`${t('user.instanceDetail.resetPassword')}${t('user.tasks.taskCreated')}${t('common.leftParen')}${t('user.tasks.taskID')}: ${taskId}${t('common.rightParen')}${t('common.comma')}${t('user.tasks.processing')}${t('common.ellipsis')}`)
          actionLoading.value = false
          if (taskId) {
            passwordResetPending.value = true
            pollForNewPassword(instance.value.id, taskId)
          }
        } else {
          ElMessage.error(response.message || t('user.instanceDetail.resetPasswordFailed'))
          actionLoading.value = false
        }
      } catch (error) {
        console.error('创建密码重置任务失败:', error)
        ElMessage.error(getErrorMessage(error, t('user.instanceDetail.resetPasswordFailed')))
        actionLoading.value = false
      }
    } catch {
      // 用户取消
    } finally {
      actionLoading.value = false
      actionLock.release()
    }
  }

  const createShareLink = async () => {
    if (!instance.value?.id) {
      ElMessage.error(t('user.instances.instanceInvalid'))
      return
    }
    if (!ensureInstanceOperationAllowed()) return
    if (!ensureTrafficOperationAllowed()) return
    if (!shareLinkLock.tryAcquire()) return
    shareLinkLoading.value = true
    try {
      const { value } = await ElMessageBox.prompt(
        t('user.instances.shareExpiryPrompt'),
        t('user.instances.createShareLink'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          inputValue: '30',
          inputPattern: /^([1-9]\d{0,3}|100[0-7]\d|10080)$/,
          inputErrorMessage: t('user.instances.shareExpiryInvalid')
        }
      )
      const minutes = Number(value || 30)
      const response = await createUserInstanceShare(instance.value.id, { expiresInMinutes: minutes })
      const url = normalizeShareURL(response.data?.url)
      if (!url) {
        ElMessage.error(t('user.instances.shareLinkCreateFailed'))
        return
      }
      // 显示分享链接对话框，支持手动选择和复制
      await showShareLinkDialog(url, { title: t('user.instances.createShareLink'), t })
    } catch (error) {
      if (error !== 'cancel') {
        console.error('创建分享链接失败:', error)
        ElMessage.error(getErrorMessage(error, t('user.instances.shareLinkCreateFailed')))
      }
    } finally {
      shareLinkLoading.value = false
      shareLinkLock.release()
    }
  }

  const togglePassword = () => {
    showPassword.value = !showPassword.value
  }

  const truncateIP = (ip, maxLength = 25) => {
    if (!ip || ip.length <= maxLength) return ip
    return ip.substring(0, maxLength - 3) + '...'
  }

  const formatSSHCommand = (username, ip, port) => {
    const host = formatEndpointHostForUrl(ip)
    const fullCommand = `ssh ${username || 'root'}@${host} -p ${port}`
    // Do not truncate IPv6 literals: removing their closing bracket would
    // turn a valid copyable command into an invalid host expression.
    if (host.includes(':') || fullCommand.length <= 40) return fullCommand
    const truncatedIP = truncateIP(host, 20)
    return `ssh ${username || 'root'}@${truncatedIP} -p ${port}`
  }

  const formatIPPort = (ip, port) => {
    const host = formatEndpointHostForUrl(ip)
    const fullAddress = formatEndpointHostPort(ip, port)
    if (host.includes(':') || fullAddress.length <= 30) return fullAddress
    const truncatedIP = truncateIP(host, 20)
    return `${truncatedIP}:${port}`
  }

  const doCopyToClipboard = async (text) => {
    await copyToClipboardUtil(text, t('user.instanceDetail.copiedToClipboard'))
  }

  return {
    actionLoading,
    passwordResetPending,
    shareLinkLoading,
    showPassword,
    showTrafficDetail,
    showResetImageDialog,
    resetImages,
    selectedResetImage,
    loadingResetImages,
    confirmResetWithImage,
    viewTaskDetail,
    performAction,
    openSSHTerminal,
    openExecTerminal,
    createShareLink,
    showResetPasswordDialog,
    togglePassword,
    truncateIP,
    formatSSHCommand,
    formatIPPort,
    copyToClipboard: doCopyToClipboard
  }
}
