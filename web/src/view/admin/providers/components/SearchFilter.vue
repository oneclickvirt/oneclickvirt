<template>
  <div class="filter-container">
    <el-row :gutter="20">
      <el-col :span="6">
        <el-input
          v-model="localSearchForm.name"
          :placeholder="$t('admin.providers.searchByName')"
          clearable
          @clear="handleSearch"
          @keyup.enter="handleSearch"
        >
          <template #prefix>
            <el-icon><Search /></el-icon>
          </template>
        </el-input>
      </el-col>
      <el-col :span="4">
        <el-select
          v-model="localSearchForm.type"
          :placeholder="$t('admin.providers.selectType')"
          clearable
          @change="handleSearch"
        >
          <el-option
            :label="$t('admin.providers.proxmox')"
            value="proxmox"
          />
          <el-option
            :label="$t('admin.providers.lxd')"
            value="lxd"
          />
          <el-option
            :label="$t('admin.providers.incus')"
            value="incus"
          />
          <el-option
            :label="$t('admin.providers.docker')"
            value="docker"
          />
        </el-select>
      </el-col>
      <el-col :span="4">
        <el-select
          v-model="localSearchForm.status"
          :placeholder="$t('admin.providers.selectStatus')"
          clearable
          @change="handleSearch"
        >
          <el-option
            :label="$t('admin.providers.statusActive')"
            value="active"
          />
          <el-option
            :label="$t('admin.providers.statusOffline')"
            value="offline"
          />
          <el-option
            :label="$t('admin.providers.statusFrozen')"
            value="frozen"
          />
        </el-select>
      </el-col>
      <el-col :span="6">
        <el-button
          type="primary"
          @click="handleSearch"
        >
          {{ $t('admin.providers.search') }}
        </el-button>
        <el-button @click="handleReset">
          {{ $t('admin.providers.reset') }}
        </el-button>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { reactive, watch } from 'vue'
import { Search } from '@element-plus/icons-vue'

const props = defineProps({
  searchForm: {
    type: Object,
    required: true
  }
})

const emit = defineEmits(['search', 'reset'])

const localSearchForm = reactive({
  name: props.searchForm.name || '',
  type: props.searchForm.type || '',
  status: props.searchForm.status || ''
})

watch(() => props.searchForm, (newValue) => {
  localSearchForm.name = newValue.name || ''
  localSearchForm.type = newValue.type || ''
  localSearchForm.status = newValue.status || ''
}, { deep: true })

const handleSearch = () => {
  emit('search', { ...localSearchForm })
}

const handleReset = () => {
  localSearchForm.name = ''
  localSearchForm.type = ''
  localSearchForm.status = ''
  emit('reset')
}
</script>

<style scoped>
.filter-container {
  margin-bottom: 20px;
}
</style>
