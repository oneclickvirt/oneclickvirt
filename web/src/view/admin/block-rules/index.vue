<template>
  <div class="block-rules-container">
    <!-- Rules Tab -->
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.blockRules.rules') }}</span>
          <div>
            <el-button
              type="primary"
              @click="handleCreateRule"
            >
              {{ t('admin.blockRules.addRule') }}
            </el-button>
            <el-button
              type="success"
              :disabled="selectedRules.length === 0"
              @click="showApplyDialog = true"
            >
              {{ t('admin.blockRules.applyRules') }}
            </el-button>
          </div>
        </div>
      </template>

      <el-table
        v-loading="loadingRules"
        :data="rules"
        stripe
        @selection-change="handleRuleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="name"
          :label="t('admin.blockRules.ruleName')"
          min-width="150"
        />
        <el-table-column
          prop="category"
          :label="t('admin.blockRules.category')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag
              :type="categoryTagType(row.category)"
              size="small"
            >
              {{ t(`admin.blockRules.categories.${row.category}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="t('admin.blockRules.description')"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column
          :label="t('admin.blockRules.strings')"
          width="120"
        >
          <template #default="{ row }">
            {{ parseStrings(row.strings).length }} {{ t('admin.blockRules.strings') }}
          </template>
        </el-table-column>
        <el-table-column
          :label="t('admin.blockRules.enabled')"
          width="100"
        >
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              @change="(val) => handleToggleEnabled(row, val)"
            />
          </template>
        </el-table-column>
        <el-table-column
          :label="t('admin.blockRules.builtin')"
          width="80"
        >
          <template #default="{ row }">
            <el-tag
              v-if="row.is_builtin"
              type="info"
              size="small"
            >
              {{ t('admin.blockRules.builtin') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('admin.blockRules.actions')"
          width="160"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button
              link
              type="primary"
              @click="handleEditRule(row)"
            >
              <el-icon><Edit /></el-icon>
            </el-button>
            <el-button
              link
              type="danger"
              @click="handleDeleteRule(row)"
            >
              <el-icon><Delete /></el-icon>
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Applications Card -->
    <el-card style="margin-top: 16px;">
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.blockRules.applications') }}</span>
          <el-button
            type="danger"
            :disabled="selectedApps.length === 0"
            @click="handleRemoveApplications"
          >
            {{ t('admin.blockRules.removeApplications') }}
          </el-button>
        </div>
      </template>

      <el-table
        v-loading="loadingApps"
        :data="applications"
        stripe
        @selection-change="handleAppSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="rule_id"
          :label="t('admin.blockRules.ruleId')"
          width="90"
        />
        <el-table-column
          prop="scope"
          :label="t('admin.blockRules.scope')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag size="small">
              {{ t(`admin.blockRules.scopes.${row.scope}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="target_name"
          :label="t('admin.blockRules.targetName')"
          min-width="150"
        />
        <el-table-column
          prop="status"
          :label="t('admin.blockRules.status')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag
              :type="statusTagType(row.status)"
              size="small"
            >
              {{ t(`admin.blockRules.statuses.${row.status}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="ip_version"
          :label="t('admin.blockRules.ipVersion')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag
              size="small"
              type="info"
            >
              {{ t(`admin.blockRules.ipVersions.${row.ip_version || 'both'}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="created_at"
          :label="t('admin.blockRules.createdAt')"
          width="180"
        >
          <template #default="{ row }">
            {{ formatDate(row.created_at) }}
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Create/Edit Rule Dialog -->
    <el-dialog
      v-model="showRuleDialog"
      :title="isEdit ? t('admin.blockRules.editRule') : t('admin.blockRules.addRule')"
      width="600px"
      destroy-on-close
    >
      <el-form
        ref="ruleFormRef"
        :model="ruleForm"
        :rules="ruleFormRules"
        label-width="120px"
      >
        <el-form-item
          :label="t('admin.blockRules.ruleName')"
          prop="name"
        >
          <el-input v-model="ruleForm.name" />
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.category')"
          prop="category"
        >
          <el-select
            v-model="ruleForm.category"
            style="width: 100%;"
          >
            <el-option
              v-for="cat in categories"
              :key="cat"
              :label="t(`admin.blockRules.categories.${cat}`)"
              :value="cat"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('admin.blockRules.description')">
          <el-input
            v-model="ruleForm.description"
            type="textarea"
            :rows="2"
          />
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.strings')"
          prop="stringsText"
        >
          <el-input
            v-model="ruleForm.stringsText"
            type="textarea"
            :rows="8"
            :placeholder="t('admin.blockRules.stringsPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="t('admin.blockRules.enabled')">
          <el-switch v-model="ruleForm.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showRuleDialog = false">
          {{ t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="handleSubmitRule"
        >
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- Apply Rules Dialog -->
    <el-dialog
      v-model="showApplyDialog"
      :title="t('admin.blockRules.applyRules')"
      width="600px"
      destroy-on-close
    >
      <el-form
        ref="applyFormRef"
        :model="applyForm"
        :rules="applyFormRules"
        label-width="120px"
      >
        <el-form-item :label="t('admin.blockRules.selectRules')">
          <div>
            <el-tag
              v-for="rule in selectedRules"
              :key="rule.id"
              style="margin: 2px;"
              size="small"
            >
              {{ rule.name }}
            </el-tag>
          </div>
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.scope')"
          prop="scope"
        >
          <el-select
            v-model="applyForm.scope"
            style="width: 100%;"
            @change="handleScopeChange"
          >
            <el-option
              v-for="s in scopeOptions"
              :key="s"
              :label="t(`admin.blockRules.scopes.${s}`)"
              :value="s"
            />
          </el-select>
        </el-form-item>
        <el-form-item
          v-if="applyForm.scope === 'provider'"
          :label="t('admin.blockRules.selectTargets')"
          prop="target_ids"
        >
          <el-select
            v-model="applyForm.target_ids"
            multiple
            filterable
            style="width: 100%;"
          >
            <el-option
              v-for="p in providerOptions"
              :key="p.id"
              :label="p.name || `Provider #${p.id}`"
              :value="p.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item
          v-if="applyForm.scope === 'instance'"
          :label="t('admin.blockRules.selectTargets')"
          prop="target_ids"
        >
          <div style="width: 100%;">
            <el-select
              v-model="instanceProviderFilter"
              :placeholder="t('admin.blockRules.filterByProvider')"
              clearable
              style="width: 100%; margin-bottom: 8px;"
              @change="fetchInstancesForProvider"
            >
              <el-option
                v-for="p in providerOptions"
                :key="p.id"
                :label="p.name || `Provider #${p.id}`"
                :value="p.id"
              />
            </el-select>
            <el-select
              v-model="applyForm.target_ids"
              multiple
              filterable
              :loading="loadingInstances"
              style="width: 100%;"
            >
              <el-option
                v-for="inst in instanceOptions"
                :key="inst.id"
                :label="`${inst.name} (${inst.status})`"
                :value="inst.id"
              />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.ipVersion')"
          prop="ip_version"
        >
          <el-select
            v-model="applyForm.ip_version"
            style="width: 100%;"
          >
            <el-option
              v-for="v in ipVersionOptions"
              :key="v"
              :label="t(`admin.blockRules.ipVersions.${v}`)"
              :value="v"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showApplyDialog = false">
          {{ t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="handleApplyRules"
        >
          {{ t('admin.blockRules.applyRules') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { Edit, Delete } from '@element-plus/icons-vue'
import { useBlockRuleManagement } from './composables/useBlockRuleManagement.js'
import { useI18n } from 'vue-i18n'
const { t } = useI18n()

const {
  rules,
  applications,
  providerOptions,
  instanceOptions,
  selectedRules,
  selectedApps,
  loadingRules,
  loadingApps,
  loadingInstances,
  submitting,
  showRuleDialog,
  showApplyDialog,
  isEdit,
  ruleFormRef,
  applyFormRef,
  instanceProviderFilter,
  categories,
  scopeOptions,
  ipVersionOptions,
  ruleForm,
  applyForm,
  ruleFormRules,
  applyFormRules,
  parseStrings,
  formatDate,
  categoryTagType,
  statusTagType,
  fetchInstancesForProvider,
  handleScopeChange,
  handleRuleSelectionChange,
  handleAppSelectionChange,
  handleCreateRule,
  handleEditRule,
  handleSubmitRule,
  handleDeleteRule,
  handleToggleEnabled,
  handleApplyRules,
  handleRemoveApplications
} = useBlockRuleManagement()
</script>

<style scoped>
.block-rules-container {
  padding: 20px;
}
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
</style>
