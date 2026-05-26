<template>
  <div class="sftp-panel">
    <div class="sftp-toolbar">
      <el-button
        size="small"
        @click="goParent"
      >
        Up
      </el-button>
      <el-input
        v-model="currentPath"
        size="small"
        class="path-input"
        @keyup.enter="refresh"
      />
      <el-button
        size="small"
        :loading="loading"
        @click="refresh"
      >
        Refresh
      </el-button>
      <input
        ref="fileInputRef"
        type="file"
        class="hidden-file-input"
        @change="handleFileChange"
      >
      <el-button
        size="small"
        type="primary"
        :loading="uploading"
        @click="openFilePicker"
      >
        Upload
      </el-button>
    </div>

    <el-progress
      v-if="uploading"
      :percentage="uploadProgress"
      :stroke-width="10"
      status="success"
    />

    <el-table
      v-loading="loading"
      :data="entries"
      size="small"
      height="420"
      @row-dblclick="onRowDblClick"
    >
      <el-table-column
        prop="name"
        label="Name"
        min-width="280"
      >
        <template #default="scope">
          <span
            class="entry-name"
            :class="{ dir: scope.row.isDir }"
          >
            {{ scope.row.isDir ? `📁 ${scope.row.name}` : `📄 ${scope.row.name}` }}
          </span>
        </template>
      </el-table-column>
      <el-table-column
        prop="size"
        label="Size"
        width="120"
      >
        <template #default="scope">
          <span>{{ scope.row.isDir ? '-' : formatSize(scope.row.size) }}</span>
        </template>
      </el-table-column>
      <el-table-column
        prop="modTime"
        label="Modified"
        width="180"
      >
        <template #default="scope">
          <span>{{ formatTime(scope.row.modTime) }}</span>
        </template>
      </el-table-column>
      <el-table-column
        label="Action"
        width="140"
      >
        <template #default="scope">
          <el-button
            v-if="!scope.row.isDir"
            link
            type="primary"
            @click="downloadEntry(scope.row)"
          >
            Download
          </el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup>
import { ref, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  listUserInstanceSFTP,
  downloadUserInstanceSFTP,
  uploadUserInstanceSFTP,
  getUserInstanceSFTPUploadStatus,
  abortUserInstanceSFTPUpload,
  listAdminInstanceSFTP,
  downloadAdminInstanceSFTP,
  uploadAdminInstanceSFTP,
  getAdminInstanceSFTPUploadStatus,
  abortAdminInstanceSFTPUpload,
  listAdminProviderSFTP,
  downloadAdminProviderSFTP,
  uploadAdminProviderSFTP,
  getAdminProviderSFTPUploadStatus,
  abortAdminProviderSFTPUpload
} from '@/api/sftp'

const props = defineProps({
  entityType: {
    type: String,
    required: true,
    validator: (v) => ['user-instance', 'admin-instance', 'admin-provider'].includes(v)
  },
  entityId: {
    type: [Number, String],
    required: true
  }
})

const currentPath = ref('/')
const entries = ref([])
const loading = ref(false)
const uploading = ref(false)
const uploadProgress = ref(0)
const fileInputRef = ref(null)

const CHUNK_SIZE = 8 * 1024 * 1024 // 8MB

const getApi = () => {
  if (props.entityType === 'user-instance') {
    return {
      list: listUserInstanceSFTP,
      download: downloadUserInstanceSFTP,
      upload: uploadUserInstanceSFTP,
      uploadStatus: getUserInstanceSFTPUploadStatus,
      uploadAbort: abortUserInstanceSFTPUpload
    }
  }
  if (props.entityType === 'admin-instance') {
    return {
      list: listAdminInstanceSFTP,
      download: downloadAdminInstanceSFTP,
      upload: uploadAdminInstanceSFTP,
      uploadStatus: getAdminInstanceSFTPUploadStatus,
      uploadAbort: abortAdminInstanceSFTPUpload
    }
  }
  return {
    list: listAdminProviderSFTP,
    download: downloadAdminProviderSFTP,
    upload: uploadAdminProviderSFTP,
    uploadStatus: getAdminProviderSFTPUploadStatus,
    uploadAbort: abortAdminProviderSFTPUpload
  }
}

const createStableUploadId = (file, targetDir) => {
  const raw = [
    props.entityType,
    String(props.entityId),
    targetDir,
    file.name,
    String(file.size),
    String(file.lastModified || 0)
  ].join('|')
  let hash = 2166136261
  for (let i = 0; i < raw.length; i++) {
    hash ^= raw.charCodeAt(i)
    hash += (hash << 1) + (hash << 4) + (hash << 7) + (hash << 8) + (hash << 24)
  }
  return `u${(hash >>> 0).toString(16)}`
}

const uploadChunkWithRetry = async (api, entityId, formData, maxRetries = 3) => {
  let lastError = null
  for (let attempt = 1; attempt <= maxRetries; attempt++) {
    try {
      return await api.upload(entityId, formData, { timeout: 0 })
    } catch (error) {
      lastError = error
      if (attempt >= maxRetries) {
        break
      }
      await new Promise((resolve) => setTimeout(resolve, 300 * attempt))
    }
  }
  throw lastError
}

const refresh = async () => {
  loading.value = true
  try {
    const api = getApi()
    const response = await api.list(props.entityId, currentPath.value || '/')
    const data = response?.data || {}
    currentPath.value = data.path || '/'
    entries.value = data.entries || []
  } catch (error) {
    console.error('SFTP list failed:', error)
    ElMessage.error(error?.message || 'List directory failed')
  } finally {
    loading.value = false
  }
}

const goParent = async () => {
  if (!currentPath.value || currentPath.value === '/') {
    currentPath.value = '/'
    await refresh()
    return
  }

  const segments = currentPath.value.split('/').filter(Boolean)
  if (segments.length === 0) {
    currentPath.value = '/'
  } else {
    segments.pop()
    currentPath.value = '/' + segments.join('/')
    if (currentPath.value === '') {
      currentPath.value = '/'
    }
  }

  await refresh()
}

const onRowDblClick = async (row) => {
  if (!row?.isDir) {
    return
  }
  currentPath.value = row.path || '/'
  await refresh()
}

const downloadEntry = async (entry) => {
  try {
    const api = getApi()
    const response = await api.download(props.entityId, entry.path)
    const blob = response?.data || response
    const objectUrl = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = objectUrl
    anchor.download = entry.name || 'download.bin'
    document.body.appendChild(anchor)
    anchor.click()
    document.body.removeChild(anchor)
    URL.revokeObjectURL(objectUrl)
  } catch (error) {
    console.error('SFTP download failed:', error)
    ElMessage.error(error?.message || 'Download failed')
  }
}

const openFilePicker = () => {
  if (fileInputRef.value) {
    fileInputRef.value.value = ''
    fileInputRef.value.click()
  }
}

const handleFileChange = async (event) => {
  const file = event?.target?.files?.[0]
  if (!file) {
    return
  }

  uploading.value = true
  uploadProgress.value = 0
  try {
    const api = getApi()
    const targetDir = currentPath.value || '/'
    const targetPath = `${targetDir.replace(/\/+$/, '')}/${file.name}`
    const uploadId = createStableUploadId(file, targetDir)

    let uploadedBytes = 0
    try {
      const statusResp = await api.uploadStatus(props.entityId, {
        uploadId,
        targetDir,
        filename: file.name,
        targetPath
      })
      uploadedBytes = Number(statusResp?.data?.uploadedBytes || 0)
      const completed = !!statusResp?.data?.completed
      if (completed || uploadedBytes >= file.size) {
        uploadProgress.value = 100
        ElMessage.success('Upload already completed')
        await refresh()
        return
      }

      if (uploadedBytes > 0 && uploadedBytes < file.size) {
        try {
          await ElMessageBox.confirm(
            'Detected incomplete upload data. Continue from breakpoint?',
            'Resume Upload',
            {
              confirmButtonText: 'Resume',
              cancelButtonText: 'Restart',
              distinguishCancelAndClose: true,
              type: 'warning'
            }
          )
        } catch (choice) {
          if (choice === 'cancel') {
            const abortData = new FormData()
            abortData.append('uploadId', uploadId)
            abortData.append('targetDir', targetDir)
            abortData.append('filename', file.name)
            abortData.append('targetPath', targetPath)
            await api.uploadAbort(props.entityId, abortData)
            uploadedBytes = 0
          } else {
            throw new Error('Upload canceled by user')
          }
        }
      }
    } catch {
      uploadedBytes = 0
    }

    const totalChunks = Math.max(1, Math.ceil(file.size / CHUNK_SIZE))
    let startChunkIndex = Math.floor(uploadedBytes / CHUNK_SIZE)
    if (startChunkIndex < 0) startChunkIndex = 0
    if (startChunkIndex > totalChunks - 1) startChunkIndex = totalChunks - 1

    uploadProgress.value = Math.round((uploadedBytes / file.size) * 100)

    for (let chunkIndex = startChunkIndex; chunkIndex < totalChunks; chunkIndex++) {
      const start = chunkIndex * CHUNK_SIZE
      const end = Math.min(start + CHUNK_SIZE, file.size)
      const chunk = file.slice(start, end)
      const formData = new FormData()
      formData.append('file', chunk, file.name)
      formData.append('filename', file.name)
      formData.append('targetDir', targetDir)
      formData.append('targetPath', targetPath)
      formData.append('uploadId', uploadId)
      formData.append('chunkIndex', String(chunkIndex))
      formData.append('totalChunks', String(totalChunks))
      formData.append('offset', String(start))
      formData.append('isLastChunk', String(chunkIndex === totalChunks - 1))

      await uploadChunkWithRetry(api, props.entityId, formData, 3)
      uploadProgress.value = Math.round(((chunkIndex + 1) / totalChunks) * 100)
    }

    ElMessage.success('Upload succeeded')
    await refresh()
  } catch (error) {
    console.error('SFTP upload failed:', error)
    ElMessage.error(error?.message || 'Upload failed')
  } finally {
    uploading.value = false
    uploadProgress.value = 0
  }
}

const formatSize = (size) => {
  const value = Number(size || 0)
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MB`
  return `${(value / (1024 * 1024 * 1024)).toFixed(1)} GB`
}

const formatTime = (unixTs) => {
  if (!unixTs) return '-'
  return new Date(unixTs * 1000).toLocaleString()
}

watch(() => props.entityId, () => {
  currentPath.value = '/'
  entries.value = []
  uploading.value = false
  uploadProgress.value = 0
  refresh()
})

watch(() => props.entityType, () => {
  currentPath.value = '/'
  entries.value = []
  uploading.value = false
  uploadProgress.value = 0
  refresh()
})

onMounted(() => {
  refresh()
})
</script>

<style scoped>
.sftp-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  height: 100%;
}

.sftp-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
}

.path-input {
  flex: 1;
}

.hidden-file-input {
  display: none;
}

.entry-name.dir {
  font-weight: 600;
}
</style>
