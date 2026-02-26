// 实例详情页 - 操作与剪贴板工具
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { performInstanceAction, resetInstancePassword } from '@/api/user'
import { useSSHStore } from '@/pinia/modules/ssh'

export function useInstanceActions(instance, monitoring, loadInstanceDetail) {
  const router = useRouter()
  const { t } = useI18n()
  const sshStore = useSSHStore()

  const actionLoading = ref(false)
  const showPassword = ref(false)
  const showTrafficDetail = ref(false)

  const viewTaskDetail = (taskId) => {
    router.push({ path: '/user/tasks', query: { taskId } })
  }

  const performAction = async (action) => {
    if (actionLoading.value) {
      ElMessage.warning(t('user.instanceDetail.operationInProgress'))
      return
    }

    const actionText = {
      'start': t('user.instanceDetail.actionStart'),
      'stop': t('user.instanceDetail.actionStop'),
      'restart': t('user.instanceDetail.actionRestart'),
      'reset': t('user.instanceDetail.actionReset'),
      'delete': t('user.instanceDetail.actionDelete')
    }[action]

    const confirmText = action === 'delete'
      ? `${t('user.instanceDetail.confirm')}${t('user.instanceDetail.delete')}${t('user.instances.title')} "${instance.value.name}" ${t('common.questionMark')}${t('user.profile.deleteConfirmNote')}`
      : `${t('user.instanceDetail.confirm')}${actionText}${t('user.instances.title')} "${instance.value.name}" ${t('common.questionMark')}`

    if (action === 'start' && monitoring.trafficData?.isLimited) {
      const trafficLimitConfirm = await ElMessageBox.confirm(
        `${t('user.instances.title')} "${instance.value.name}" ${t('user.instanceDetail.trafficLimitWarning')}${t('common.comma')}${t('user.instances.title')}${t('user.instanceDetail.actionStart')}${t('common.period')}`,
        t('user.instanceDetail.trafficLimitNotice'),
        {
          confirmButtonText: t('user.instanceDetail.gotIt'),
          showCancelButton: false,
          type: 'warning'
        }
      ).catch(() => false)

      if (!trafficLimitConfirm) return
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

      actionLoading.value = true

      const response = await performInstanceAction({
        instanceId: instance.value.id,
        action
      })

      if (response.code === 0 || response.code === 200) {
        ElMessage.success(`${actionText}${t('user.tasks.request')}${t('user.tasks.submitted')}${t('common.comma')}${t('user.tasks.processing')}${t('common.ellipsis')}`)

        if (action === 'delete' || action === 'reset') {
          if (action === 'reset') {
            ElMessage.info(t('user.instanceDetail.resetSystemNotice'))
          }
          router.push('/user/instances')
        } else {
          setTimeout(async () => {
            await loadInstanceDetail()
            actionLoading.value = false
          }, 3000)
        }
      } else {
        actionLoading.value = false
      }
    } catch (error) {
      if (error !== 'cancel') {
        console.error(`${actionText}实例失败:`, error)
        ElMessage.error(`${actionText}${t('user.instances.title')}${t('common.failed')}`)
      }
      actionLoading.value = false
    }
  }

  const openSSHTerminal = () => {
    if (!instance.value.id) {
      ElMessage.error(t('user.instanceDetail.instanceNotFound'))
      return
    }
    if (instance.value.status !== 'running') {
      ElMessage.warning(t('user.instanceDetail.instanceNotRunning'))
      return
    }
    if (!instance.value.password) {
      ElMessage.warning(t('user.instanceDetail.noPassword'))
      return
    }
    if (!sshStore.hasConnection(instance.value.id)) {
      sshStore.createConnection(instance.value.id, instance.value.name)
    } else {
      sshStore.showConnection(instance.value.id)
    }
  }

  const showResetPasswordDialog = async () => {
    if (actionLoading.value) {
      ElMessage.warning(t('user.instanceDetail.operationInProgress'))
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

      actionLoading.value = true

      try {
        const response = await resetInstancePassword(instance.value.id)
        if (response.code === 0 || response.code === 200) {
          const taskId = response.data.taskId
          ElMessage.success(`${t('user.instanceDetail.resetPassword')}${t('user.tasks.taskCreated')}${t('common.leftParen')}${t('user.tasks.taskID')}: ${taskId}${t('common.rightParen')}${t('common.comma')}${t('user.tasks.checkProgress')}${t('user.tasks.taskList')}${t('common.inLocation')}`)
          setTimeout(() => { actionLoading.value = false }, 3000)
        } else {
          ElMessage.error(response.message || t('user.instanceDetail.resetPasswordFailed'))
          actionLoading.value = false
        }
      } catch (error) {
        console.error('创建密码重置任务失败:', error)
        ElMessage.error(t('user.instanceDetail.resetPasswordFailed'))
        actionLoading.value = false
      }
    } catch {
      // 用户取消
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
    const fullCommand = `ssh ${username || 'root'}@${ip} -p ${port}`
    if (fullCommand.length <= 40) return fullCommand
    const truncatedIP = truncateIP(ip, 20)
    return `ssh ${username || 'root'}@${truncatedIP} -p ${port}`
  }

  const formatIPPort = (ip, port) => {
    const fullAddress = `${ip}:${port}`
    if (fullAddress.length <= 30) return fullAddress
    const truncatedIP = truncateIP(ip, 20)
    return `${truncatedIP}:${port}`
  }

  const copyToClipboard = async (text) => {
    if (!text) {
      ElMessage.warning(t('user.instanceDetail.nothingToCopy'))
      return
    }
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text)
        ElMessage.success(t('user.instanceDetail.copiedToClipboard'))
        return
      }
      const textArea = document.createElement('textarea')
      textArea.value = text
      textArea.style.position = 'fixed'
      textArea.style.left = '-999999px'
      textArea.style.top = '-999999px'
      document.body.appendChild(textArea)
      textArea.focus()
      textArea.select()
      try {
        // eslint-disable-next-line no-unused-expressions
        document.execCommand('copy')
          ? ElMessage.success(t('user.instanceDetail.copiedToClipboard'))
          : (() => { throw new Error('execCommand failed') })()
      } finally {
        document.body.removeChild(textArea)
      }
    } catch (error) {
      console.error('复制失败:', error)
      ElMessage.error(t('user.profile.copyFailed'))
    }
  }

  return {
    actionLoading,
    showPassword,
    showTrafficDetail,
    viewTaskDetail,
    performAction,
    openSSHTerminal,
    showResetPasswordDialog,
    togglePassword,
    truncateIP,
    formatSSHCommand,
    formatIPPort,
    copyToClipboard
  }
}
