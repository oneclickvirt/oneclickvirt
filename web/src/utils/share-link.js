import { ElMessage, ElMessageBox } from 'element-plus'

export function normalizeShareURL(url) {
  if (!url) return ''
  if (/^https?:\/\//i.test(url)) return url
  const prefix = url.startsWith('/') ? '' : '/'
  return `${window.location.origin}${prefix}${url}`
}

function escapeHtml(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('\"', '&quot;')
    .replaceAll("'", '&#39;')
}

async function copyTextToClipboard(text) {
  if (navigator.clipboard?.writeText && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  document.body.appendChild(textarea)
  textarea.select()
  try {
    const copied = document.execCommand('copy')
    if (!copied) throw new Error('copy command failed')
  } finally {
    document.body.removeChild(textarea)
  }
}

/**
 * 显示分享链接对话框。主按钮是“复制”，取消按钮用于关闭；复制失败时仍可手动选择链接。
 * @param {string} url - 分享链接URL
 * @param {object} options - 可选配置
 * @param {string} options.title - 对话框标题
 * @param {Function} options.t - i18n翻译函数
 */
export async function showShareLinkDialog(url, { title = '', t = (k) => k } = {}) {
  const fullUrl = normalizeShareURL(url)
  const dialogTitle = title || t('user.instances.createShareLink') || '创建临时访问链接'
  const tipText = t('user.instances.shareLinkTip') || '点击链接区域可全选；也可以点击复制按钮。'
  const createdText = t('user.instances.shareLinkCreated') || '分享链接已创建'
  const copyText = t('common.copy') || '复制'
  const closeText = t('common.close') || t('common.cancel') || '关闭'
  const copiedText = t('user.instances.shareLinkCopied') || '分享链接已复制'
  const safeCreatedText = escapeHtml(createdText)
  const safeFullUrl = escapeHtml(fullUrl)
  const safeTipText = escapeHtml(tipText)

  try {
    await ElMessageBox.confirm(
      `<div style="margin-bottom:12px;word-break:break-all">${safeCreatedText}</div>` +
      `<div class="share-link-value" style="background:#f5f7fa;border:1px solid #dcdfe6;border-radius:4px;padding:10px 12px;font-family:monospace;font-size:13px;word-break:break-all;user-select:all;cursor:text;max-height:200px;overflow:auto">${safeFullUrl}</div>` +
      `<div style="margin-top:12px;color:#909399;font-size:12px">${safeTipText}</div>`,
      dialogTitle,
      {
        dangerouslyUseHTMLString: true,
        confirmButtonText: copyText,
        cancelButtonText: closeText,
        distinguishCancelAndClose: true,
        customClass: 'share-link-dialog',
        closeOnClickModal: false
      }
    )
    await copyTextToClipboard(fullUrl)
    ElMessage.success(copiedText)
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.warning(t('common.copyFailed') || tipText)
  }
}
