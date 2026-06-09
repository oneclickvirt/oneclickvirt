<template>
  <div class="admin-group-page">
    <el-card>
      <template #header>
        <div class="card-header">
          <div>
            <div class="title">{{ t('admin.group.title') }}</div>
            <div class="subtitle">使用分组控制普通用户申请领取页的节点展示；描述支持 Markdown，并会安全渲染部分 HTML。</div>
          </div>
          <el-button type="primary" @click="openCreateDialog">新增分组</el-button>
        </div>
      </template>

      <el-table v-loading="loading" :data="groups" stripe>
        <el-table-column prop="groupName" label="分组名称" min-width="150" />
        <el-table-column label="分组描述" min-width="260">
          <template #default="{ row }">
            <div v-if="row.groupDescriptionHtml" class="description-preview" v-html="row.groupDescriptionHtml" />
            <el-text v-else type="info">未填写</el-text>
          </template>
        </el-table-column>
        <el-table-column label="节点" min-width="260">
          <template #default="{ row }">
            <el-space wrap>
              <el-tag v-for="provider in row.providers" :key="provider.id" size="small">
                {{ provider.name }}
              </el-tag>
              <el-text v-if="!row.providers?.length" type="info">暂无节点</el-text>
            </el-space>
          </template>
        </el-table-column>
        <el-table-column prop="providerCount" label="节点数量" width="100" />
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button text type="primary" @click="openEditDialog(row)">编辑</el-button>
            <el-button text type="danger" @click="deleteGroup(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-if="!loading && groups.length === 0" description="暂无分组，点击右上角新增分组后再勾选节点" />
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      :title="editingGroup?.id ? '编辑分组' : '新增分组'"
      width="860px"
      destroy-on-close
      @closed="resetDialog"
    >
      <el-form :model="form" label-width="110px">
        <el-form-item label="分组名称" required>
          <el-input v-model="form.groupName" maxlength="64" show-word-limit placeholder="例如：香港高性能 / 免费体验 / 美国节点" />
        </el-form-item>
        <el-form-item label="分组描述">
          <el-input
            v-model="form.groupDescription"
            type="textarea"
            :rows="6"
            maxlength="20000"
            show-word-limit
            placeholder="支持 Markdown，例如：**注意事项**、列表、链接；也可使用部分安全 HTML 标签"
          />
          <div class="form-item-hint">建议用 Markdown 编写说明，会像 GitHub 一样渲染常用标题、列表、粗体、链接与部分安全 HTML。</div>
        </el-form-item>
        <el-form-item label="预览" v-if="descriptionPreview">
          <div class="description-preview full" v-html="descriptionPreview" />
        </el-form-item>
        <el-form-item label="包含节点">
          <el-table
            ref="providerTableRef"
            :data="providers"
            height="320"
            row-key="id"
            @selection-change="onProviderSelectionChange"
          >
            <el-table-column type="selection" width="48" :selectable="canSelectProvider" />
            <el-table-column prop="name" label="节点名称" min-width="180" />
            <el-table-column prop="type" label="类型" width="100" />
            <el-table-column prop="status" label="状态" width="100" />
            <el-table-column label="当前分组" min-width="140">
              <template #default="{ row }">
                <el-tag v-if="row.groupId && row.groupId !== editingGroup?.id" type="warning" size="small">
                  {{ row.groupName || '其他分组' }}
                </el-tag>
                <el-tag v-else-if="row.groupId === editingGroup?.id" type="success" size="small">本分组</el-tag>
                <el-text v-else type="info">未分组</el-text>
              </template>
            </el-table-column>
          </el-table>
          <div class="form-item-hint">一个节点同一时间只属于一个分组；保存后会自动从原分组移出。</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveGroup">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import service from '@/utils/request'
import { renderMarkdown } from '@/utils/markdown'

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editingGroup = ref(null)
const providerTableRef = ref(null)
const groups = ref([])
const providers = ref([])
const selectedProviderIds = ref([])

const form = reactive({
  groupName: '',
  groupDescription: '',
  sortOrder: 0
})

const descriptionPreview = computed(() => renderMarkdown(form.groupDescription))

const fetchGroups = async () => {
  loading.value = true
  try {
    const res = await service({ url: '/v1/admin/groups', method: 'get' })
    if (res.code === 200 && res.data) {
      groups.value = res.data.groups || []
      providers.value = res.data.providers || []
    }
  } catch (e) {
    console.error('Failed to fetch groups:', e)
    ElMessage.error('获取分组失败')
  } finally {
    loading.value = false
  }
}

const resetDialog = () => {
  editingGroup.value = null
  selectedProviderIds.value = []
  form.groupName = ''
  form.groupDescription = ''
  form.sortOrder = 0
}

const openCreateDialog = async () => {
  resetDialog()
  dialogVisible.value = true
  await nextTick()
  providerTableRef.value?.clearSelection()
}

const openEditDialog = async (row) => {
  editingGroup.value = row
  form.groupName = row.groupName || ''
  form.groupDescription = row.groupDescription || ''
  form.sortOrder = row.sortOrder || 0
  selectedProviderIds.value = [...(row.providerIds || [])]
  dialogVisible.value = true
  await nextTick()
  providerTableRef.value?.clearSelection()
  providers.value.forEach(provider => {
    if (selectedProviderIds.value.includes(provider.id)) providerTableRef.value?.toggleRowSelection(provider, true)
  })
}

const canSelectProvider = () => true

const onProviderSelectionChange = (selection) => {
  selectedProviderIds.value = selection.map(item => item.id)
}

const saveGroup = async () => {
  if (!form.groupName.trim()) {
    ElMessage.warning('请填写分组名称')
    return
  }
  saving.value = true
  try {
    const data = {
      groupName: form.groupName.trim(),
      groupDescription: form.groupDescription,
      sortOrder: form.sortOrder,
      providerIds: selectedProviderIds.value
    }
    const id = editingGroup.value?.id
    const res = await service({
      url: id ? `/v1/admin/groups/${id}` : '/v1/admin/groups',
      method: id ? 'put' : 'post',
      data
    })
    if (res.code === 200) {
      ElMessage.success('保存成功')
      dialogVisible.value = false
      await fetchGroups()
    } else {
      ElMessage.error(res.msg || res.message || '保存失败')
    }
  } catch (e) {
    ElMessage.error(e?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

const deleteGroup = async (row) => {
  await ElMessageBox.confirm(`确认删除分组「${row.groupName}」？节点会回到未分组状态。`, '删除分组', { type: 'warning' })
  try {
    const res = await service({ url: `/v1/admin/groups/${row.id}`, method: 'delete' })
    if (res.code === 200) {
      ElMessage.success('删除成功')
      await fetchGroups()
    } else {
      ElMessage.error(res.msg || res.message || '删除失败')
    }
  } catch (e) {
    if (e !== 'cancel') ElMessage.error(e?.message || '删除失败')
  }
}

onMounted(fetchGroups)
</script>

<style scoped>
.admin-group-page {
  padding: 20px;
}
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.title {
  font-weight: 600;
  font-size: 16px;
}
.subtitle,
.form-item-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-top: 4px;
}
.description-preview {
  max-height: 150px;
  overflow: auto;
  padding: 8px 10px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
  background: var(--el-fill-color-lighter);
  line-height: 1.6;
}
.description-preview.full {
  max-height: 260px;
  width: 100%;
}
.description-preview :deep(p) {
  margin: 0 0 6px;
}
.description-preview :deep(ul),
.description-preview :deep(ol) {
  padding-left: 20px;
}
</style>
