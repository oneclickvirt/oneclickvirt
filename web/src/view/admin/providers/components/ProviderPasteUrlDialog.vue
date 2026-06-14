<template>
  <el-dialog
    :model-value="visible"
    :title="$t('admin.providers.setHardwareReport')"
    width="500px"
    @update:model-value="$emit('update:visible', $event)"
  >
    <el-form @submit.prevent="handleSubmit">
      <el-form-item
        :label="$t('admin.providers.pasteUrl')"
        required
        :error="input && !isValidPasteUrl ? $t('admin.providers.pasteUrlInvalid') : ''"
      >
        <el-input
          :model-value="input"
          :placeholder="$t('admin.providers.pasteUrlPlaceholder')"
          clearable
          @update:model-value="$emit('update:input', $event)"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="$emit('update:visible', false)">
        {{ $t('common.cancel') }}
      </el-button>
      <el-button
        type="primary"
        :loading="saving"
        :disabled="!isValidPasteUrl"
        @click="handleSubmit"
      >
        {{ $t('common.confirm') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  visible: { type: Boolean, default: false },
  input: { type: String, default: '' },
  saving: { type: Boolean, default: false }
})

const emit = defineEmits(['update:visible', 'update:input', 'submit'])

const isValidPasteUrl = computed(() => {
  const value = props.input.trim()
  if (!value) return false
  try {
    const url = new URL(value)
    const searchArea = `${url.pathname} ${url.search} ${url.hash}`
    return url.protocol === 'https:' &&
      url.hostname === 'paste.spiritlhl.net' &&
      /[a-zA-Z0-9_-]+\.txt/.test(searchArea)
  } catch {
    return false
  }
})

const handleSubmit = () => {
  if (props.saving || !isValidPasteUrl.value) return
  emit('submit')
}
</script>
