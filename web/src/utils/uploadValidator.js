/**
 * 文件上传验证工具
 * 头像上传功能已移除
 */

// 通用文件上传限制
const MAX_REQUEST_SIZE = 1024 * 1024 // 1MB (通用文件)

// 危险文件扩展名黑名单
const DANGEROUS_EXTS = [
  '.exe', '.bat', '.cmd', '.com', '.scr', '.pif', '.msi', '.dll',
  '.sh', '.bash', '.zsh', '.fish', '.ps1', '.vbs', '.js', '.jar',
  '.php', '.asp', '.jsp', '.py', '.rb', '.pl', '.cgi', '.htaccess'
]

/**
 * 验证文件大小
 * @param {File} file - 要验证的文件
 * @param {number} maxSize - 最大文件大小（字节），默认为1MB
 * @returns {boolean} 验证结果
 */
export function validateFileSize(file, maxSize = MAX_REQUEST_SIZE) {
  return file.size <= maxSize
}

/**
 * 检查文件扩展名是否安全
 * @param {string} filename - 文件名
 * @returns {boolean} 是否安全
 */
export function isFileExtensionSafe(filename) {
  const ext = filename.toLowerCase().split('.').pop()
  return !DANGEROUS_EXTS.includes('.' + ext)
}

/**
 * 验证文件名安全性
 * @param {string} filename - 文件名
 * @returns {Object} 验证结果
 */
export function validateFilename(filename) {
  const result = {
    valid: true,
    errors: []
  }

  // 检查文件名长度
  if (filename.length > 255) {
    result.valid = false
    result.errors.push('文件名过长（最大255字符）')
  }

  // 检查非法字符
  const illegalChars = /[<>:"/\\|?*\x00-\x1f]/
  if (illegalChars.test(filename)) {
    result.valid = false
    result.errors.push('文件名包含非法字符')
  }

  // 检查危险扩展名
  if (!isFileExtensionSafe(filename)) {
    result.valid = false
    result.errors.push('不允许上传的文件类型')
  }

  return result
}

/**
 * 验证图片文件
 * @param {File} file - 要验证的文件
 * @param {Object} options - 验证选项
 * @returns {Object} 验证结果
 */
export function validateImageFile(file, options = {}) {
  const {
    maxSize = MAX_AVATAR_SIZE,
    allowedTypes = ALLOWED_AVATAR_TYPES,
    allowedExts = ALLOWED_AVATAR_EXTS,
    showError = true
  } = options

  const result = {
    valid: true,
    errors: []
  }

  // 验证文件名安全性
  const filenameValidation = validateFilename(file.name)
  if (!filenameValidation.valid) {
    result.valid = false
    result.errors.push(...filenameValidation.errors)
  }

  // 验证文件大小
  if (file.size > maxSize) {
    result.valid = false
    const maxSizeMB = (maxSize / 1024 / 1024).toFixed(1)
    result.errors.push(`文件大小不能超过 ${maxSizeMB}MB`)
  }

  if (file.size === 0) {
    result.valid = false
    result.errors.push('文件大小为0')
  }

  // 验证文件类型
  if (!allowedTypes.includes(file.type)) {
    result.valid = false
    result.errors.push(`文件类型不支持，仅支持: ${allowedTypes.map(type => type.split('/')[1].toUpperCase()).join(', ')}`)
  }

  // 验证文件扩展名
  const ext = '.' + file.name.toLowerCase().split('.').pop()
  if (!allowedExts.includes(ext)) {
    result.valid = false
    result.errors.push(`文件扩展名不支持，仅支持: ${allowedExts.join(', ')}`)
  }

  // 显示错误消息
  if (showError && !result.valid) {
    import('element-plus').then(({ ElMessage }) => {
      result.errors.forEach(error => ElMessage.error(error))
    })
  }

  return result
}

/**
 * 验证JSON数据大小
 * @param {Object} data - 要验证的数据
 * @param {number} maxSize - 最大数据大小（字节）
 * @returns {boolean} 验证结果
 */
export function validateJsonSize(data, maxSize = MAX_REQUEST_SIZE) {
  const jsonString = JSON.stringify(data)
  const dataSize = new Blob([jsonString]).size
  return dataSize <= maxSize
}

/**
 * 获取数据大小（字节）
 * @param {*} data - 要计算大小的数据
 * @returns {number} 数据大小（字节）
 */
export function getDataSize(data) {
  if (data instanceof File || data instanceof Blob) {
    return data.size
  }
  
  if (typeof data === 'string') {
    return new Blob([data]).size
  }
  
  if (typeof data === 'object') {
    return new Blob([JSON.stringify(data)]).size
  }
  
  return 0
}

/**
 * 检查文件内容安全性（基础检查）
 * @param {File} file - 要检查的文件
 * @returns {Promise<Object>} 检查结果
 */
export async function checkFileContentSecurity(file) {
  return new Promise((resolve) => {
    const reader = new FileReader()
    
    reader.onload = function(e) {
      const content = e.target.result
      const result = {
        valid: true,
        errors: []
      }

      // 检查恶意脚本模式
      const maliciousPatterns = [
        /<script[^>]*>/i,
        /<iframe[^>]*>/i,
        /javascript:/i,
        /vbscript:/i,
        /on\w+\s*=/i,
        /<\?php/i,
        /<%[^>]*%>/i,
        /eval\s*\(/i,
        /exec\s*\(/i
      ]

      for (const pattern of maliciousPatterns) {
        if (pattern.test(content)) {
          result.valid = false
          result.errors.push('检测到潜在的恶意代码')
          break
        }
      }

      resolve(result)
    }

    reader.onerror = function() {
      resolve({
        valid: false,
        errors: ['文件读取失败']
      })
    }

    // 只读取文件前8KB进行检查
    const slice = file.slice(0, 8192)
    reader.readAsText(slice)
  })
}

/**
 * 增强的图片文件验证（包含安全检查）
 * @param {File} file - 要验证的文件
 * @param {Object} options - 验证选项
 * @returns {Promise<Object>} 验证结果
 */
export async function validateImageFileSecure(file, options = {}) {
  // 先执行基础验证
  const basicValidation = validateImageFile(file, { ...options, showError: false })
  if (!basicValidation.valid) {
    return basicValidation
  }

  // 执行内容安全检查
  const securityCheck = await checkFileContentSecurity(file)
  if (!securityCheck.valid) {
    const result = {
      valid: false,
      errors: [...basicValidation.errors, ...securityCheck.errors]
    }

    if (options.showError) {
      import('element-plus').then(({ ElMessage }) => {
        result.errors.forEach(error => ElMessage.error(error))
      })
    }

    return result
  }

  return basicValidation
}

/**
 * 格式化文件大小显示
 * @param {number} bytes - 字节数
 * @returns {string} 格式化后的大小字符串
 */
export function formatFileSize(bytes) {
  if (bytes === 0) return '0 B'
  
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

export default {
  MAX_REQUEST_SIZE,
  MAX_AVATAR_SIZE,
  ALLOWED_AVATAR_TYPES,
  ALLOWED_AVATAR_EXTS,
  DANGEROUS_EXTS,
  validateFileSize,
  validateImageFile,
  validateImageFileSecure,
  validateFilename,
  isFileExtensionSafe,
  checkFileContentSecurity,
  validateJsonSize,
  getDataSize,
  formatFileSize
}
