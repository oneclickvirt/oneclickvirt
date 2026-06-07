import { ElMessageBox } from 'element-plus'

export function normalizeShareURL(url) {
  if (!url) return ''
  if (/^https?:\/\//i.test(url)) return url
  const prefix = url.startsWith('/') ? '' : '/'
  return `${window.location.origin}${prefix}${url}`
}

/**
 * 显示分享链接对话框，支持用户手动选择和复制URL。
 * 不再自动复制到剪贴板，避免在穿透环境下传输失败。
 * @param {string} url - 分享链接URL
 * @param {object} options - 可选配置
 * @param {string} options.title - 对话框标题
 * @param {Function} options.t - i18n翻译函数
 */
export async function showShareLinkDialog(url, { title = '', t = (k) => k } = {}) {
  const fullUrl = normalizeShareURL(url)
  const dialogTitle = title || t('user.instances.createShareLink') || '创建临时访问链接'
  const tipText = t('user.instances.shareLinkTip') || '点击上方链接区域可全选，然后使用 Ctrl+C 复制'
  const createdText = t('user.instances.shareLinkCreated') || '分享链接已创建'
  const confirmText = t('common.confirm') || '确定'

  try {
    await ElMessageBox.alert(
      `<div style="margin-bottom:12px;word-break:break-all">${createdText}</div>` +
      `<div style="background:#f5f7fa;border:1px solid #dcdfe6;border-radius:4px;padding:10px 12px;font-family:monospace;font-size:13px;word-break:break-all;user-select:all;cursor:text;max-height:200px;overflow:auto">${fullUrl}</div>` +
      `<div style="margin-top:12px;color:#909399;font-size:12px">${tipText}</div>`,
      dialogTitle,
      {
        dangerouslyUseHTMLString: true,
        confirmButtonText: confirmText,
        customClass: 'share-link-dialog'
      }
    )
  } catch {
    // 用户关闭对话框
  }
}
