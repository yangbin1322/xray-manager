<template>
  <div class="app-container" :class="{ 'dark-theme': appStore.theme === 'dark' }">
    <!-- 侧边栏 -->
    <Sidebar
      @showSubDialog="showSubscriptionDialog = true"
      @showLBDialog="showLBDialog = true"
      @showChainDialog="showChainDialog = true"
    />

    <!-- 主内容区 -->
    <div class="main-content">
      <!-- 工具栏 -->
      <Toolbar />

      <!-- 节点列表 -->
      <NodeList @editRule="handleEditRule" />

      <!-- 日志面板 -->
      <LogPanel />

      <!-- 底部控制栏 -->
      <BottomBar @addRule="handleAddRule" />
    </div>

    <!-- 节点编辑器对话框 -->
    <NodeEditor
      :visible="showEditor"
      :editingRule="editingRule"
      @close="closeEditor"
    />

    <!-- Toast 通知 -->
    <ToastContainer />

    <!-- TODO: 订阅管理、负载均衡、链式代理 对话框可按需添加 -->
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Events } from '@wailsio/runtime'
import { useRulesStore } from './stores/rules.js'
import { useGroupsStore } from './stores/groups.js'
import { useAppStore } from './stores/app.js'

import Sidebar from './components/Sidebar.vue'
import Toolbar from './components/Toolbar.vue'
import NodeList from './components/NodeList.vue'
import LogPanel from './components/LogPanel.vue'
import BottomBar from './components/BottomBar.vue'
import NodeEditor from './components/NodeEditor.vue'
import ToastContainer from './components/ToastContainer.vue'

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()

// 对话框状态
const showEditor = ref(false)
const editingRule = ref(null)
const showSubscriptionDialog = ref(false)
const showLBDialog = ref(false)
const showChainDialog = ref(false)

function handleAddRule() {
  editingRule.value = null
  showEditor.value = true
}

function handleEditRule(rule) {
  editingRule.value = rule
  showEditor.value = true
}

function closeEditor() {
  showEditor.value = false
  editingRule.value = null
}

// 监听后端事件
function listenToBackendEvents() {
  Events.On('log', (event) => {
    appStore.addLog(event.data)
  })

  Events.On('ruleUpdated', (event) => {
    rulesStore.updateRuleInList(event.data)
  })

  Events.On('loadRules', () => {
    rulesStore.loadRules()
  })

  Events.On('speedTestResult', () => {
    // 测速结果已通过 ruleUpdated 更新
  })

  Events.On('allSpeedTestComplete', () => {
    appStore.showToast('批量测速完成', 'success')
    rulesStore.loadRules()
  })
}

// 初始化
onMounted(async () => {
  appStore.initTheme()
  listenToBackendEvents()

  await rulesStore.loadRules()
  await groupsStore.loadGroups()
  await groupsStore.loadSubscriptions()
  await appStore.loadAutoStart()
  await appStore.loadSysProxyStatus()
})
</script>

<style>
/* CSS 变量 - 亮色主题 */
:root {
  --primary-color: #3498db;
  --primary-light: rgba(52, 152, 219, 0.1);
  --bg-primary: #ffffff;
  --bg-secondary: #f8f9fa;
  --bg-hover: #e9ecef;
  --text-primary: #2c3e50;
  --text-secondary: #7f8c8d;
  --border-color: #e0e0e0;
}

/* 深色主题 */
[data-theme="dark"] {
  --primary-color: #5dade2;
  --primary-light: rgba(93, 173, 226, 0.15);
  --bg-primary: #1e1e2e;
  --bg-secondary: #252535;
  --bg-hover: #2d2d3f;
  --text-primary: #e0e0e0;
  --text-secondary: #a0a0a0;
  --border-color: #3a3a4a;
}

* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 14px;
}

.app-container {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

.main-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 滚动条样式 */
::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}

::-webkit-scrollbar-track {
  background: transparent;
}

::-webkit-scrollbar-thumb {
  background: var(--border-color);
  border-radius: 3px;
}

::-webkit-scrollbar-thumb:hover {
  background: var(--text-secondary);
}
</style>
