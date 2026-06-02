<template>
  <div
    class="menu-item"
    :class="{ 'is-collapse': isCollapse }"
  >
    <el-icon
      v-if="icon"
      class="menu-icon"
    >
      <component :is="icon" />
    </el-icon>
    <span
      v-if="!isCollapse"
      class="menu-title"
    >{{ $t(title) }}</span>
  </div>
</template>

<script setup>
defineProps({
  icon: {
    type: String,
    default: ''
  },
  title: {
    type: String,
    default: ''
  },
  isCollapse: {
    type: Boolean,
    default: false
  }
})
</script>

<style scoped>
.menu-item {
  display: flex;
  align-items: center;
  width: 100%;
  min-width: 0;
}

.menu-icon {
  margin-right: 8px;
  font-size: 16px;
  flex-shrink: 0;
}

.menu-title {
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.menu-item.is-collapse {
  justify-content: center;
}

.menu-item.is-collapse .menu-icon {
  margin-right: 0;
}
</style>
