<template>
  <div class="toolbar">
    <!-- 状态过滤 -->
    <div class="filter-group">
      <label>状态：</label>
      <button
        v-for="f in filters" :key="f.value"
        :class="['filter-btn', { active: rulesStore.statusFilter === f.value }]"
        @click="rulesStore.statusFilter = f.value"
      >{{ f.label }}</button>
    </div>

    <!-- 搜索 -->
    <div class="search-box">
      <input
        type="text"
        v-model="rulesStore.searchKeyword"
        placeholder="搜索节点..."
        class="search-input"
      />
    </div>

    <!-- 操作按钮 -->
    <div class="toolbar-actions">
      <button class="btn-action" @click="rulesStore.startSelectedRules()" title="启动选中">启动选中</button>
      <button class="btn-action" @click="rulesStore.stopSelectedRules()" title="停止选中">停止选中</button>
      <button class="btn-action" @click="rulesStore.testSelectedSpeed()" title="测速选中">选中测速</button>
      <button class="btn-action btn-danger" @click="handleDeleteSelected" title="删除选中">删除选中</button>
    </div>

    <div class="toolbar-right">
      <!-- 系统代理 -->
      <button
        v-if="!appStore.sysProxyEnabled"
        class="btn-action btn-small"
        @click="handleEnableSysProxy"
      >设为系统代理</button>
      <button
        v-else
        class="btn-action btn-small btn-active"
        @click="appStore.disableSysProxy()"
      >取消系统代理</button>

      <!-- 主题切换 -->
      <button class="btn-icon" @click="appStore.toggleTheme()" :title="themeTitle">
        {{ appStore.theme === 'dark' ? '☀' : '☾' }}
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useAppStore } from '../stores/app.js'

const rulesStore = useRulesStore()
const appStore = useAppStore()

const filters = [
  { label: '全部', value: 'all' },
  { label: '已启动', value: 'running' },
  { label: '未启动', value: 'stopped' },
]

const themeTitle = computed(() =>
  appStore.theme === 'dark' ? '切换亮色模式' : '切换深色模式'
)

function handleDeleteSelected() {
  const count = rulesStore.selectedRuleIds.length
  if (count === 0) {
    appStore.showToast('请先选择要删除的规则', 'warning')
    return
  }
  if (confirm(`确定要删除选中的 ${count} 条规则吗?`)) {
    rulesStore.deleteSelectedRules()
  }
}

function handleEnableSysProxy() {
  const selected = rulesStore.selectedRuleIds
  if (selected.length !== 1) {
    appStore.showToast('请选择一个节点设为系统代理', 'warning')
    return
  }
  appStore.enableSysProxy(selected[0])
}
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border-color);
  flex-wrap: wrap;
}

.filter-group {
  display: flex;
  align-items: center;
  gap: 4px;
}

.filter-group label {
  font-size: 13px;
  color: var(--text-secondary);
}

.filter-btn {
  padding: 4px 12px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}

.filter-btn.active {
  background: var(--primary-color);
  color: #fff;
  border-color: var(--primary-color);
}

.search-box {
  flex: 1;
  min-width: 150px;
  max-width: 250px;
}

.search-input {
  width: 100%;
  padding: 6px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.toolbar-actions {
  display: flex;
  gap: 6px;
}

.toolbar-right {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.btn-action {
  padding: 5px 12px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}

.btn-action:hover { background: var(--bg-hover); }

.btn-danger { color: #e74c3c; border-color: #e74c3c; }
.btn-danger:hover { background: #e74c3c; color: #fff; }

.btn-active { background: var(--primary-color); color: #fff; border-color: var(--primary-color); }

.btn-small { padding: 4px 10px; font-size: 11px; }

.btn-icon {
  padding: 4px 8px;
  border: none;
  background: none;
  cursor: pointer;
  font-size: 18px;
}
</style>
