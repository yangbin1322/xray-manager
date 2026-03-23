<template>
  <div class="log-panel">
    <div class="log-header">
      <span>日志输出</span>
      <div class="log-controls">
        <input
          v-model="searchKeyword"
          type="text"
          placeholder="搜索日志..."
          class="log-search"
        />
        <select v-model="levelFilter" class="log-filter">
          <option value="ALL">所有日志</option>
          <option value="INFO">INFO</option>
          <option value="WARN">WARN</option>
          <option value="ERROR">ERROR</option>
          <option value="XRAY">XRAY</option>
        </select>
        <button class="btn-small" @click="appStore.clearLogsList()">清空</button>
      </div>
    </div>
    <div class="log-content" ref="logContainer">
      <div
        v-for="log in filteredLogs"
        :key="log.id"
        :class="['log-entry', logLevelClass(log.message)]"
      >
        <span class="log-time">{{ log.time }}</span>
        <span class="log-msg">{{ log.message }}</span>
      </div>
      <div v-if="filteredLogs.length === 0" class="log-empty">暂无日志</div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, nextTick } from 'vue'
import { useAppStore } from '../stores/app.js'
import { storeToRefs } from 'pinia'

const appStore = useAppStore()
const { logs } = storeToRefs(appStore)

const searchKeyword = ref('')
const levelFilter = ref('ALL')
const logContainer = ref(null)

const filteredLogs = computed(() => {
  let result = logs.value

  if (levelFilter.value !== 'ALL') {
    const level = levelFilter.value
    result = result.filter(l => l.message.includes(`[${level}]`))
  }

  if (searchKeyword.value) {
    const kw = searchKeyword.value.toLowerCase()
    result = result.filter(l => l.message.toLowerCase().includes(kw))
  }

  return result
})

function logLevelClass(message) {
  if (message.includes('[错误]') || message.includes('[ERROR]')) return 'log-error'
  if (message.includes('[警告]') || message.includes('[WARN]')) return 'log-warn'
  if (message.includes('[成功]')) return 'log-success'
  return ''
}

// 自动滚动到底部
watch(() => logs.value.length, () => {
  nextTick(() => {
    if (logContainer.value) {
      logContainer.value.scrollTop = logContainer.value.scrollHeight
    }
  })
})
</script>

<style scoped>
.log-panel {
  height: 200px;
  border-top: 1px solid var(--border-color);
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
}

.log-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 6px 12px;
  border-bottom: 1px solid var(--border-color);
  background: var(--bg-secondary);
  font-size: 13px;
  font-weight: 500;
}

.log-controls {
  display: flex;
  gap: 6px;
  align-items: center;
}

.log-search {
  padding: 3px 8px;
  border: 1px solid var(--border-color);
  border-radius: 3px;
  font-size: 12px;
  width: 150px;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.log-filter {
  padding: 3px 6px;
  border: 1px solid var(--border-color);
  border-radius: 3px;
  font-size: 12px;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.btn-small {
  padding: 3px 10px;
  border: 1px solid var(--border-color);
  border-radius: 3px;
  background: var(--bg-primary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}

.log-content {
  flex: 1;
  overflow-y: auto;
  padding: 4px 8px;
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  line-height: 1.6;
}

.log-entry {
  padding: 1px 4px;
  border-radius: 2px;
}

.log-entry:hover { background: var(--bg-hover); }

.log-time {
  color: var(--text-secondary);
  margin-right: 8px;
  font-size: 11px;
}

.log-error { color: #e74c3c; }
.log-warn { color: #f39c12; }
.log-success { color: #27ae60; }

.log-empty {
  text-align: center;
  padding: 20px;
  color: var(--text-secondary);
}
</style>
