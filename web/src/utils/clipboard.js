import { ElMessage } from 'element-plus'
import i18n from '@/i18n'

const t = (...args) => i18n.global.t(...args)

/**
 * 复制文本到剪贴板
 * 支持现代浏览器的 Clipboard API 和降级到传统的 execCommand
 * 
 * @param {string} text - 要复制的文本
 * @param {string} successMessage - 成功提示信息（可选）
 * @param {string} errorMessage - 失败提示信息（可选）
 * @returns {Promise<boolean>} - 返回是否复制成功
 */
export async function copyToClipboard(text, successMessage, errorMessage) {
  if (successMessage === undefined) successMessage = t('common.saveSuccess')
  if (errorMessage === undefined) errorMessage = t('common.copyFailed')
  // 检查是否有内容可复制
  if (!text || text.trim() === '') {
    ElMessage.warning(t('common.nothingToCopy'))
    return false
  }

  try {
    // 优先使用现代的 Clipboard API
    // 此API要求安全上下文（HTTPS或localhost）
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      if (successMessage) {
        ElMessage.success(successMessage)
      }
      return true
    }

    // 降级方案：使用传统的 document.execCommand
    // execCommand 已废弃，但作为 Clipboard API 不可用时的兼容方案
    // 在非安全上下文（非HTTPS）环境中仍然需要
    const textArea = document.createElement('textarea')
    textArea.value = text
    
    // 设置样式使其不可见且不影响页面布局
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    textArea.style.opacity = '0'
    textArea.style.pointerEvents = 'none'
    
    // 到DOM
    document.body.appendChild(textArea)
    
    try {
      // 聚焦并选中文本
      textArea.focus()
      textArea.select()
      
      // 尝试执行复制命令
      // @ts-ignore - execCommand 已废弃但作为降级方案仍需使用
      const successful = document.execCommand('copy')
      
      if (successful) {
        if (successMessage) {
          ElMessage.success(successMessage)
        }
        return true
      } else {
        throw new Error('execCommand returned false')
      }
    } finally {
      // 无论成功与否，都要移除临时元素
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    if (errorMessage) {
      ElMessage.error(errorMessage)
    }
    return false
  }
}

/**
 * Read text from the system clipboard.
 *
 * Clipboard reads are only available to a focused page in a secure context;
 * unlike writes, there is no reliable execCommand fallback. Returning null
 * keeps callers from accidentally sending an incomplete paste to a session.
 *
 * @param {string} errorMessage - Message shown when the read fails
 * @param {string} unavailableMessage - Message shown when the API cannot be used
 * @returns {Promise<string|null>} - Clipboard text, or null on failure
 */
export async function readFromClipboard(errorMessage, unavailableMessage) {
  if (errorMessage === undefined) errorMessage = t('common.pasteFailed')
  if (unavailableMessage === undefined) unavailableMessage = t('common.clipboardUnavailable')

  if (typeof navigator === 'undefined'
    || !navigator.clipboard
    || typeof navigator.clipboard.readText !== 'function'
    || typeof window === 'undefined'
    || !window.isSecureContext) {
    ElMessage.error(unavailableMessage)
    return null
  }

  try {
    return await navigator.clipboard.readText()
  } catch (error) {
    console.error('读取剪贴板失败:', error)
    if (errorMessage) {
      ElMessage.error(errorMessage)
    }
    return null
  }
}

/**
 * 复制对象为JSON字符串
 * 
 * @param {Object} obj - 要复制的对象
 * @param {boolean} pretty - 是否格式化JSON（默认true）
 * @param {string} successMessage - 成功提示信息（可选）
 * @param {string} errorMessage - 失败提示信息（可选）
 * @returns {Promise<boolean>} - 返回是否复制成功
 */
export async function copyObjectAsJSON(obj, pretty = true, successMessage, errorMessage) {
  if (successMessage === undefined) successMessage = t('common.saveSuccess')
  if (errorMessage === undefined) errorMessage = t('common.copyFailed')
  try {
    const jsonString = pretty ? JSON.stringify(obj, null, 2) : JSON.stringify(obj)
    return await copyToClipboard(jsonString, successMessage, errorMessage)
  } catch (error) {
    console.error('JSON序列化失败:', error)
    ElMessage.error(t('common.cannotSerialize'))
    return false
  }
}

/**
 * 检查剪贴板API是否可用
 * 
 * @returns {boolean} - 返回剪贴板API是否可用
 */
export function isClipboardAvailable() {
  return !!(navigator.clipboard && window.isSecureContext)
}

export default {
  copyToClipboard,
  readFromClipboard,
  copyObjectAsJSON,
  isClipboardAvailable
}
