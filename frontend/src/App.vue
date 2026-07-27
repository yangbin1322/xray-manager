<template>
  <div class="app-container" :class="{ 'dark-theme': appStore.theme === 'dark' }">
    <!-- 侧边栏 -->
    <Sidebar
      @showSubDialog="showSubscriptionDialog = true"
      @showLBDialog="editingLB = null; showLBDialog = true"
      @showChainDialog="editingChain = null; showChainDialog = true"
      @showRelayDialog="editingRelay = null; showRelayDialog = true"
    />

    <!-- 主内容区 -->
    <div class="main-content">
      <!-- 工具栏 -->
      <Toolbar @batchEdit="handleBatchEdit" />

      <!-- 节点列表 -->
      <NodeList
        @editRule="handleEditRule"
        @editLB="handleEditLB"
        @editChain="handleEditChain"
        @editRelay="handleEditRelay"
      />

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

    <!-- 订阅管理对话框 -->
    <SubscriptionDialog
      :visible="showSubscriptionDialog"
      @close="showSubscriptionDialog = false"
    />

    <!-- 故障转移对话框 -->
    <LoadBalancerDialog
      :visible="showLBDialog"
      :editingLB="editingLB"
      @close="showLBDialog = false; editingLB = null"
    />

    <!-- 链式代理对话框 -->
    <ChainProxyDialog
      :visible="showChainDialog"
      :editingChain="editingChain"
      @close="showChainDialog = false; editingChain = null"
    />

    <!-- 动态会话代理对话框 -->
    <SessionRelayDialog
      :visible="showRelayDialog"
      :editingRelay="editingRelay"
      @close="showRelayDialog = false; editingRelay = null"
    />

    <!-- 批量编辑对话框 -->
    <BatchNodeEditor
      :visible="showBatchEditor"
      :nodes="batchNodes"
      :skippedCount="batchSkipped"
      @close="showBatchEditor = false; batchNodes = []"
    />

    <PortConflictDialog
      :visible="showPortConflicts"
      :conflicts="portConflicts"
      :saving="resolvingPortConflicts"
      @keep="showPortConflicts = false"
      @resolve="resolveSelectedPortConflicts"
    />

    <!-- Toast 通知 -->
    <ToastContainer />
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
import SubscriptionDialog from './components/SubscriptionDialog.vue'
import LoadBalancerDialog from './components/LoadBalancerDialog.vue'
import ChainProxyDialog from './components/ChainProxyDialog.vue'
import SessionRelayDialog from './components/SessionRelayDialog.vue'
import BatchNodeEditor from './components/BatchNodeEditor.vue'
import PortConflictDialog from './components/PortConflictDialog.vue'
import * as api from './api.js'

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()

// 对话框状态
const showEditor = ref(false)
const editingRule = ref(null)
const showSubscriptionDialog = ref(false)
const showLBDialog = ref(false)
const showChainDialog = ref(false)
const showRelayDialog = ref(false)
const editingLB = ref(null)
const editingChain = ref(null)
const editingRelay = ref(null)
const showBatchEditor = ref(false)
const batchNodes = ref([])
const batchSkipped = ref(0)
const showPortConflicts = ref(false)
const portConflicts = ref([])
const resolvingPortConflicts = ref(false)

async function resolveSelectedPortConflicts(resourceIds) {
  resolvingPortConflicts.value = true
  try {
    const remaining = await api.resolvePortConflicts(resourceIds) || []
    portConflicts.value = remaining
    showPortConflicts.value = false
    await rulesStore.loadRules()
    appStore.showToast(`已为 ${resourceIds.length} 个节点重新分配端口`, 'success')
  } catch (e) {
    appStore.showToast(`端口重新分配失败: ${e}`, 'error', 5000)
  } finally {
    resolvingPortConflicts.value = false
  }
}

function handleBatchEdit({ nodes, skippedCount }) {
  batchNodes.value = nodes
  batchSkipped.value = skippedCount
  showBatchEditor.value = true
}

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

function handleEditLB(lb) {
  editingLB.value = lb
  showLBDialog.value = true
}

function handleEditChain(chain) {
  editingChain.value = chain
  showChainDialog.value = true
}

function handleEditRelay(relay) {
  editingRelay.value = relay
  showRelayDialog.value = true
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

  // 实时流量更新
  Events.On('trafficUpdate', (event) => {
    rulesStore.applyTrafficUpdate(event.data)
  })

  // 会话代理统计（连接数/会话数/速度）
  Events.On('relayStatsUpdate', (event) => {
    rulesStore.applyRelayStats(event.data)
  })

  // 健康检查结果（单个节点）
  Events.On('healthCheckResult', (event) => {
    rulesStore.applyHealthCheckResult(event.data)
  })

  // 健康检查完成（一轮批量）
  Events.On('healthCheckComplete', () => {
    rulesStore.loadRules()
  })

  // 节点启动后不通，已被自动停用
  Events.On('nodeFailed', (event) => {
    const d = event.data || {}
    appStore.showToast(`节点「${d.alias || ''}」启动后不通（${d.reason || '未知原因'}），已自动停用`, 'error', 5000)
  })

  // 启动自动检查发现新版本
  Events.On('updateAvailable', (event) => {
    const d = event.data || {}
    appStore.showToast(d.message || `发现新版本 v${d.latestVersion || ''}`, 'info', 8000)
  })
  Events.On('updateError', (event) => {
    appStore.showToast(`自动更新失败: ${event.data || ''}`, 'error', 6000)
  })
  Events.On('updateProgress', (event) => {
    appStore.showToast(String(event.data || '更新进行中'), 'success', 6000)
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
  portConflicts.value = await api.getPendingPortConflicts() || []
  showPortConflicts.value = portConflicts.value.length > 0
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
