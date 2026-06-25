<!-- eslint-disable vue/no-v-html -->
<template>
  <div
    v-if="announcements.length > 0"
    class="topbar-announcement"
  >
    <div class="announcement-container">
      <!-- 可滚动的公告内容 -->
      <div
        ref="scrollContainer"
        class="announcement-scroll"
      >
        <div 
          v-for="(announcement, index) in announcements" 
          :key="announcement.id"
          class="announcement-item"
          :class="{ 'active': currentIndex === index }"
        >
          <div class="announcement-content">
            <span class="announcement-badge">{{ $t('common.announcement') }}</span>
            <span class="announcement-title">{{ announcement.title }}</span>
            <div 
              class="announcement-text" 
              v-html="announcement.contentHtml || announcement.content"
            />
          </div>
        </div>
      </div>
      
      <!-- 控制按钮 -->
      <div
        v-if="announcements.length > 1"
        class="announcement-controls"
      >
        <button
          class="control-btn"
          @click="prevAnnouncement"
        >
          <svg
            width="16"
            height="16"
            fill="currentColor"
            viewBox="0 0 16 16"
          >
            <path
              fill-rule="evenodd"
              d="M11.354 1.646a.5.5 0 0 1 0 .708L5.707 8l5.647 5.646a.5.5 0 0 1-.708.708l-6-6a.5.5 0 0 1 0-.708l6-6a.5.5 0 0 1 .708 0z"
            />
          </svg>
        </button>
        <span class="announcement-indicator">{{ currentIndex + 1 }} / {{ announcements.length }}</span>
        <button
          class="control-btn"
          @click="nextAnnouncement"
        >
          <svg
            width="16"
            height="16"
            fill="currentColor"
            viewBox="0 0 16 16"
          >
            <path
              fill-rule="evenodd"
              d="M4.646 1.646a.5.5 0 0 1 .708 0l6 6a.5.5 0 0 1 0 .708l-6 6a.5.5 0 0 1-.708-.708L10.293 8 4.646 2.354a.5.5 0 0 1 0-.708z"
            />
          </svg>
        </button>
      </div>
      
      <!-- 关闭按钮 -->
      <button
        class="close-btn"
        @click="closeAnnouncement"
      >
        <svg
          width="16"
          height="16"
          fill="currentColor"
          viewBox="0 0 16 16"
        >
          <path d="M2.146 2.854a.5.5 0 1 1 .708-.708L8 7.293l5.146-5.147a.5.5 0 0 1 .708.708L8.707 8l5.147 5.146a.5.5 0 0 1-.708.708L8 8.707l-5.146 5.147a.5.5 0 0 1-.708-.708L7.293 8 2.146 2.854Z" />
        </svg>
      </button>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { getPublicAnnouncements } from '@/api/public'

const emit = defineEmits(['visible-change'])
const announcements = ref([])
const currentIndex = ref(0)
const scrollContainer = ref()
let autoScrollTimer = null

const getAnnouncementKey = (item) => `${item.id}:${item.updatedAt || item.createdAt || ''}`

const getWeekKey = () => {
  const now = new Date()
  const day = now.getDay() || 7
  const monday = new Date(now)
  monday.setDate(now.getDate() - day + 1)
  monday.setHours(0, 0, 0, 0)
  const year = monday.getFullYear()
  const month = String(monday.getMonth() + 1).padStart(2, '0')
  const date = String(monday.getDate()).padStart(2, '0')
  return `${year}-${month}-${date}`
}

const readClosedState = () => {
  const raw = localStorage.getItem('topbar_announcement_closed')
  if (!raw) return { weekKey: getWeekKey(), hiddenKeys: [] }
  try {
    const parsed = JSON.parse(raw)
    if (parsed && parsed.weekKey === getWeekKey() && Array.isArray(parsed.hiddenKeys)) {
      return parsed
    }
  } catch {
    // Older versions stored a single timestamp. Ignore it so weekly per-item state starts cleanly.
  }
  return { weekKey: getWeekKey(), hiddenKeys: [] }
}

const writeClosedState = (state) => {
  localStorage.setItem('topbar_announcement_closed', JSON.stringify({
    weekKey: getWeekKey(),
    hiddenKeys: [...new Set(state.hiddenKeys || [])]
  }))
}

const filterVisibleAnnouncements = (items) => {
  const state = readClosedState()
  const hidden = new Set(state.hiddenKeys)
  return items.filter(item => !hidden.has(getAnnouncementKey(item)))
}

const setAnnouncements = (items) => {
  announcements.value = items
  if (currentIndex.value >= announcements.value.length) {
    currentIndex.value = 0
  }
  emit('visible-change', announcements.value.length > 0)
}

// 获取顶部栏公告
const fetchTopbarAnnouncements = async () => {
  try {
    const response = await getPublicAnnouncements('topbar')
    if (response.code === 200) {
      const items = response.data || []
      const visibleItems = filterVisibleAnnouncements(items)
      if (visibleItems.length > 0) {
        setAnnouncements(visibleItems)
        startAutoScroll()
      } else {
        setAnnouncements([])
      }
    } else {
      setAnnouncements([])
    }
  } catch (error) {
    console.error('获取顶部栏公告失败:', error)
    setAnnouncements([])
  }
}

// 上一条公告
const prevAnnouncement = () => {
  currentIndex.value = currentIndex.value > 0 ? currentIndex.value - 1 : announcements.value.length - 1
  resetAutoScroll()
}

// 下一条公告
const nextAnnouncement = () => {
  currentIndex.value = currentIndex.value < announcements.value.length - 1 ? currentIndex.value + 1 : 0
  resetAutoScroll()
}

// 开始自动滚动
const startAutoScroll = () => {
  if (announcements.value.length <= 1) return
  
  autoScrollTimer = setInterval(() => {
    nextAnnouncement()
  }, 5000) // 每5秒切换一次
}

// 重置自动滚动
const resetAutoScroll = () => {
  if (autoScrollTimer) {
    clearInterval(autoScrollTimer)
    autoScrollTimer = null
  }
  startAutoScroll()
}

// 关闭公告栏
const closeAnnouncement = () => {
  const current = announcements.value[currentIndex.value]
  if (!current) return

  const state = readClosedState()
  state.hiddenKeys = [...(state.hiddenKeys || []), getAnnouncementKey(current)]
  writeClosedState(state)

  if (autoScrollTimer) {
    clearInterval(autoScrollTimer)
    autoScrollTimer = null
  }

  const nextItems = announcements.value.filter(item => getAnnouncementKey(item) !== getAnnouncementKey(current))
  setAnnouncements(nextItems)
  if (announcements.value.length > 1) {
    startAutoScroll()
  }
}

onMounted(() => {
  fetchTopbarAnnouncements()
})

onUnmounted(() => {
  if (autoScrollTimer) {
    clearInterval(autoScrollTimer)
  }
})
</script>

<style scoped>
.topbar-announcement {
  background: linear-gradient(135deg, #16a34a, #22c55e);
  color: white;
  padding: 6px 0;
  position: sticky;
  top: 0;
  z-index: calc(var(--z-navbar, 1002) + 2);
  box-shadow: 0 2px 10px rgba(22, 163, 74, 0.2);
  border-bottom: 1px solid rgba(255, 255, 255, 0.2);
}

.announcement-container {
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 20px;
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 36px;
}

.announcement-scroll {
  flex: 1;
  overflow: hidden;
  position: relative;
  height: 36px;
}

.announcement-item {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  opacity: 0;
  transform: translateX(100%);
  transition: all 0.5s ease;
}

.announcement-item.active {
  opacity: 1;
  transform: translateX(0);
}

.announcement-content {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.announcement-badge {
  background: rgba(255, 255, 255, 0.2);
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 12px;
  font-weight: 600;
  flex-shrink: 0;
}

.announcement-title {
  font-weight: 600;
  font-size: 14px;
  flex-shrink: 0;
  margin-right: 8px;
}

.announcement-text {
  font-size: 14px;
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 富文本内容样式重置 */
.announcement-text :deep(*) {
  color: inherit !important;
  font-size: inherit !important;
  font-weight: inherit !important;
  margin: 0 !important;
  padding: 0 !important;
  line-height: inherit !important;
}

.announcement-text :deep(strong) {
  font-weight: 700 !important;
}

.announcement-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.announcement-indicator {
  font-size: 12px;
  opacity: 0.8;
  min-width: 40px;
  text-align: center;
}

.control-btn,
.close-btn {
  background: rgba(255, 255, 255, 0.2);
  border: none;
  color: white;
  width: 32px;
  height: 32px;
  border-radius: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: all 0.2s ease;
  flex-shrink: 0;
}

.control-btn:hover,
.close-btn:hover {
  background: rgba(255, 255, 255, 0.3);
  transform: scale(1.1);
}

.close-btn {
  margin-left: 8px;
}

/* 响应式设计 */
@media (max-width: 768px) {
  .announcement-container {
    padding: 0 16px;
    gap: 8px;
  }
  
  .announcement-title {
    display: none;
  }
  
  .announcement-badge {
    font-size: 11px;
    padding: 2px 6px;
  }
  
  .announcement-text {
    font-size: 13px;
  }
  
  .announcement-indicator {
    display: none;
  }
  
  .control-btn,
  .close-btn {
    width: 28px;
    height: 28px;
  }
}
</style>
