<template>
  <el-dialog
    :model-value="visible"
    :title="$t('admin.providers.setHardwareReport')"
    width="500px"
    @update:model-value="$emit('update:visible', $event)"
  >
    <el-form @submit.prevent="$emit('submit')">
      <el-form-item :label="$t('admin.providers.pasteUrl')">
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
        @click="$emit('submit')"
      >
        {{ $t('common.confirm') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
defineProps({
  visible: { type: Boolean, default: false },
  input: { type: String, default: '' },
  saving: { type: Boolean, default: false }
})

defineEmits(['update:visible', 'update:input', 'submit'])
</script>
