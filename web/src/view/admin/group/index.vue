<template>
  <div class="admin-group-page">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.group.title') }}</span>
        </div>
      </template>

      <el-form
        v-loading="loading"
        :model="form"
        label-width="120px"
        style="max-width: 800px"
      >
        <el-form-item :label="t('admin.group.groupName')">
          <el-input
            v-model="form.groupName"
            :placeholder="t('admin.group.groupNamePlaceholder')"
            maxlength="64"
            show-word-limit
          />
        </el-form-item>

        <el-form-item :label="t('admin.group.groupDescription')">
          <div class="editor-wrapper">
            <div ref="editorRef" class="rich-editor" contenteditable="true"
              @input="onEditorInput"
              @paste="onEditorPaste"
            />
          </div>
          <div class="form-item-hint">
            {{ t('admin.group.groupDescriptionHint') }}
          </div>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" @click="handleSave" :loading="saving">
            {{ t('common.save') }}
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import service from '@/utils/request'

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const editorRef = ref(null)

const form = ref({
  groupName: '',
  groupDescription: ''
})

const fetchGroupInfo = async () => {
  loading.value = true
  try {
    const res = await service({ url: '/v1/admin/group-info', method: 'get' })
    if ((res.code === 0 || res.code === 200) && res.data) {
      form.value.groupName = res.data.groupName || ''
      form.value.groupDescription = res.data.groupDescription || ''
      await nextTick()
      if (editorRef.value) {
        editorRef.value.innerHTML = form.value.groupDescription
      }
    }
  } catch (e) {
    console.error('Failed to fetch group info:', e)
  } finally {
    loading.value = false
  }
}

const onEditorInput = () => {
  if (editorRef.value) {
    form.value.groupDescription = editorRef.value.innerHTML
  }
}

const onEditorPaste = (e) => {
  // Allow HTML paste but sanitize
  e.preventDefault()
  const html = e.clipboardData.getData('text/html')
  const text = e.clipboardData.getData('text/plain')
  document.execCommand('insertHTML', false, html || text)
}

const handleSave = async () => {
  saving.value = true
  try {
    const res = await service({
      url: '/v1/admin/group-info',
      method: 'put',
      data: {
        groupName: form.value.groupName,
        groupDescription: form.value.groupDescription
      }
    })
    if (res.code === 0 || res.code === 200) {
      ElMessage.success(t('common.saveSuccess'))
    } else {
      ElMessage.error(res.msg || res.message || t('common.saveFailed'))
    }
  } catch (e) {
    ElMessage.error(t('common.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  fetchGroupInfo()
})
</script>

<style scoped>
.admin-group-page {
  padding: 20px;
}
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
}
.editor-wrapper {
  width: 100%;
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  overflow: hidden;
}
.rich-editor {
  min-height: 200px;
  padding: 12px;
  outline: none;
  font-size: 14px;
  line-height: 1.6;
}
.rich-editor:focus {
  border-color: var(--el-color-primary);
}
.form-item-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-top: 4px;
}
</style>
