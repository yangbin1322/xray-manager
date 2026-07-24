<template>
  <div class="node-list">
    <div v-if="rulesStore.loading" class="loading-overlay">
      <div class="spinner"></div>
    </div>

    <table class="rules-table" :class="{ 'shift-pressed': shiftPressed }">
      <thead>
        <tr>
          <th class="col-check">
            <input type="checkbox" :checked="allSelected" @change="rulesStore.selectAll($event.target.checked)" />
          </th>
          <th class="col-alias">别名</th>
          <th class="col-protocol">协议</th>
          <th class="col-server">服务器地址</th>
          <th class="col-sport">服务器端口</th>
          <th class="col-lport">本地端口</th>
          <th class="col-health sortable" @click="rulesStore.setSort('healthLatency')">
            健康
            <span v-if="rulesStore.sortColumn === 'healthLatency'" class="sort-arrow">
              {{ rulesStore.sortDirection === 'asc' ? '▲' : '▼' }}
            </span>
          </th>
          <th class="col-latency sortable" @click="rulesStore.setSort('latency')">
            延迟
            <span v-if="rulesStore.sortColumn === 'latency'" class="sort-arrow">
              {{ rulesStore.sortDirection === 'asc' ? '▲' : '▼' }}
            </span>
          </th>
          <th class="col-speed sortable" @click="rulesStore.setSort('downloadSpeed')">
            速度
            <span v-if="rulesStore.sortColumn === 'downloadSpeed'" class="sort-arrow">
              {{ rulesStore.sortDirection === 'asc' ? '▲' : '▼' }}
            </span>
          </th>
          <th class="col-traffic">实时流量</th>
          <th class="col-traffic-total">今日/累计</th>
          <th class="col-ip">真实IP</th>
          <th class="col-status">状态</th>
          <th class="col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="rulesStore.filteredRules.length === 0">
          <td colspan="14" class="empty-row">暂无节点数据</td>
        </tr>
        <tr
          v-for="rule in rulesStore.filteredRules"
          :key="rule.id"
          :class="[rowClass(rule), { 'row-selected': rulesStore.selectedIds.has(rule.id) }]"
          @click="onRowClick(rule.id, $event)"
        >
          <td class="col-check" @click.stop>
            <input
              type="checkbox"
              :checked="rulesStore.selectedIds.has(rule.id)"
              @change="rulesStore.toggleSelect(rule.id)"
            />
          </td>
          <td class="col-alias" :title="rule.alias">
            <span v-if="rule._nodeType === 'lb'" class="type-badge badge-lb" title="故障转移">转</span>
            <span v-if="rule._nodeType === 'chain'" class="type-badge badge-chain" title="链式代理">链</span>
            {{ rule.alias || '-' }}
          </td>
          <td class="col-protocol">
            <span :class="['protocol-badge', `protocol-${rule.protocol}`]">{{ protocolLabel(rule.protocol) }}</span>
          </td>
          <!-- 普通节点显示服务器信息，LB/链显示节点数 -->
          <td class="col-server" :title="rule.serverAddr">
            <template v-if="rule._nodeType === 'rule'">{{ rule.serverAddr || '-' }}</template>
            <template v-else-if="rule._nodeType === 'lb'">{{ (rule.nodeIds || []).length }} 个子节点</template>
            <template v-else>{{ (rule.chainNodes || []).length }} 节点链</template>
          </td>
          <td class="col-sport">
            <template v-if="rule._nodeType === 'rule'">{{ rule.serverPort || '-' }}</template>
            <template v-else>-</template>
          </td>
          <td class="col-lport">{{ rule.localPort || '-' }}</td>
          <td class="col-health">
            <span
              :class="['health-badge', `health-${rule.healthStatus || 'unknown'}`]"
              :title="healthTitle(rule)"
            >{{ healthLabel(rule) }}</span>
          </td>
          <td class="col-latency">
            <span v-if="rule.testStatus === 'testing'" class="testing">测速中...</span>
            <span v-else-if="rule.latency > 0" :class="latencyClass(rule.latency)">
              {{ rule.latency }}ms
            </span>
            <span v-else class="no-data">-</span>
          </td>
          <td class="col-speed">
            <span v-if="rule.downloadSpeed > 0">{{ rule.downloadSpeed.toFixed(2) }} MB/s</span>
            <span v-else class="no-data">-</span>
          </td>
          <td class="col-traffic">
            <template v-if="rule.enabled && trafficOf(rule)">
              <span class="traffic-speed">↑{{ formatSpeed(trafficOf(rule).upSpeed) }} ↓{{ formatSpeed(trafficOf(rule).downSpeed) }}</span>
            </template>
            <span v-else class="no-data">-</span>
          </td>
          <td class="col-traffic-total" :title="trafficTitle(rule)">
            <span class="traffic-total">{{ formatBytes(todayTotal(rule)) }} / {{ formatBytes(allTotal(rule)) }}</span>
          </td>
          <td class="col-ip" :title="rule.lastError || rule.realIp">
            <span v-if="rule.lastError" class="ip-error">{{ rule.lastError }}</span>
            <span v-else>{{ rule.realIp || '-' }}</span>
          </td>
          <td class="col-status">
            <span :class="['status-dot', statusClass(rule)]" :title="rule.lastError || ''"></span>
          </td>
          <td class="col-actions" @click.stop>
            <button
              v-if="!rule.enabled"
              class="btn-action-sm btn-start"
              @click="handleStart(rule)"
              :disabled="startingIds.has(rule.id)"
            >{{ startingIds.has(rule.id) ? '...' : '启动' }}</button>
            <button
              v-else
              class="btn-action-sm btn-stop"
              @click="handleStop(rule)"
            >停止</button>
            <button v-if="rule._nodeType === 'rule'" class="btn-action-sm" @click="$emit('editRule', rule)">编辑</button>
            <button v-if="rule._nodeType === 'lb'" class="btn-action-sm" @click="$emit('editLB', rule)">编辑</button>
            <button v-if="rule._nodeType === 'chain'" class="btn-action-sm" @click="$emit('editChain', rule)">编辑</button>
            <button class="btn-action-sm btn-test" @click="handleTest(rule)">测速</button>
            <button class="btn-action-sm btn-health" @click="handleHealthCheck(rule)">检测</button>
            <button class="btn-action-sm btn-del" @click="handleDelete(rule)">删除</button>
          </td>
        </tr>
      </tbody>
    </table>

    <!-- 统计信息 -->
    <div class="table-footer">
      <span>总计 {{ rulesStore.totalCount }} 个节点，运行中 {{ rulesStore.runningCount }} 个</span>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useAppStore } from '../stores/app.js'
import * as api from '../api.js'

const emit = defineEmits(['editRule', 'editLB', 'editChain'])

const rulesStore = useRulesStore()
const appStore = useAppStore()
const startingIds = ref(new Set())
const shiftPressed = ref(false) // 按住 Shift 时临时禁用文本选择

// 快捷键：Ctrl+C 复制 / Ctrl+V 粘贴
function handleKeydown(e) {
  if (e.key === 'Shift') shiftPressed.value = true

  // 忽略输入框内的按键
  const tag = e.target.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return

  if ((e.ctrlKey || e.metaKey) && e.key === 'c') {
    e.preventDefault()
    rulesStore.copySelected().then(({ count, skipped }) => {
      if (count > 0) appStore.showToast(`已复制 ${count} 个节点分享链接${skipped ? `，跳过 ${skipped} 个复合代理` : ''}`, 'success')
      else if (skipped > 0) appStore.showToast('故障转移和链式代理没有通用分享链接，未复制', 'warning')
    }).catch(error => appStore.showToast(`写入系统剪贴板失败: ${error}`, 'error'))
  }

  if ((e.ctrlKey || e.metaKey) && e.key === 'v') {
    e.preventDefault()
    rulesStore.pasteNodes().then(result => {
      if (result?.successCount > 0) appStore.showToast(`已从系统剪贴板导入 ${result.successCount} 个节点`, 'success')
      else appStore.showToast('剪贴板中没有可导入的节点分享链接', 'warning')
    }).catch(error => appStore.showToast(`读取或导入剪贴板失败: ${error}`, 'error'))
  }

  // Delete：删除当前多选节点（弹窗确认）
  if ((e.key === 'Delete' || e.key === 'Backspace') && !e.ctrlKey && !e.metaKey && !e.altKey) {
    const count = rulesStore.selectedRuleIds.length
    if (count === 0) return
    e.preventDefault()
    if (!confirm(`确定要删除选中的 ${count} 条规则吗?`)) return
    rulesStore.deleteSelectedRules()
      .then(() => appStore.showToast(`已删除 ${count} 条规则`, 'success'))
      .catch(error => appStore.showToast(`删除失败: ${error}`, 'error'))
  }
}

// 松开 Shift 恢复文本选择；窗口失焦时也重置，避免状态卡住
function handleKeyup(e) {
  if (e.key === 'Shift') shiftPressed.value = false
}
function resetShift() { shiftPressed.value = false }

onMounted(() => {
  window.addEventListener('keydown', handleKeydown)
  window.addEventListener('keyup', handleKeyup)
  window.addEventListener('blur', resetShift)
})
onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('keyup', handleKeyup)
  window.removeEventListener('blur', resetShift)
})

const allSelected = computed(() => {
  const filtered = rulesStore.filteredRules
  return filtered.length > 0 && filtered.every(r => rulesStore.selectedIds.has(r.id))
})

// 行点击：支持 Ctrl（切换单个）/ Shift（范围选）多选
function onRowClick(id, event) {
  const shift = event.shiftKey
  const ctrl = event.ctrlKey || event.metaKey
  // Shift 多选会触发浏览器文本选择，清除它
  if (shift) {
    const sel = window.getSelection && window.getSelection()
    if (sel) sel.removeAllRanges()
  }
  rulesStore.handleRowSelect(id, { shift, ctrl })
}

function rowClass(rule) {
  if (rule._nodeType === 'lb' && rule.enabled) return 'row-lb-running'
  if (rule._nodeType === 'chain' && rule.enabled) return 'row-chain-running'
  return {
    'row-running': rule.enabled && rule._nodeType === 'rule',
    'row-testing': rule.testStatus === 'testing',
  }
}

function latencyClass(latency) {
  if (latency < 100) return 'latency-good'
  if (latency < 300) return 'latency-ok'
  return 'latency-bad'
}

// 协议显示名映射：组合节点显示中文，其余协议原样显示
const protocolLabels = { loadbalance: '故障转移', chain: '链式代理' }
function protocolLabel(protocol) {
  return protocolLabels[protocol] || protocol
}

// ===== 健康检查展示 =====
const healthLabels = {
  checking: '检测中',
  online: '在线',
  high_latency: '延迟高',
  timeout: '超时',
  dns_failed: 'DNS失败',
  tls_failed: 'TLS失败',
  reality_failed: 'Reality失败',
}

function healthLabel(rule) {
  const status = rule.healthStatus
  if (!status) return '未检测'
  if (status === 'online' && rule.healthLatency > 0) return `${rule.healthLatency}ms`
  return healthLabels[status] || status
}

function healthTitle(rule) {
  const parts = []
  if (rule.healthStatus) parts.push(`状态: ${healthLabels[rule.healthStatus] || rule.healthStatus}`)
  if (rule.healthLatency > 0) parts.push(`延迟: ${rule.healthLatency}ms`)
  if (rule.lastHealthCheck) parts.push(`检测时间: ${rule.lastHealthCheck}`)
  return parts.join('\n') || '尚未检测'
}

// ===== 流量展示 =====
function trafficOf(rule) {
  return rulesStore.traffic[rule.id]
}

function todayTotal(rule) {
  const snap = trafficOf(rule)
  if (snap) return (snap.todayUp || 0) + (snap.todayDown || 0)
  const t = rule.traffic
  return t ? (t.todayUp || 0) + (t.todayDown || 0) : 0
}

function allTotal(rule) {
  const snap = trafficOf(rule)
  if (snap) return (snap.totalUp || 0) + (snap.totalDown || 0)
  const t = rule.traffic
  return t ? (t.totalUp || 0) + (t.totalDown || 0) : 0
}

function trafficTitle(rule) {
  const snap = trafficOf(rule) || rule.traffic || {}
  return [
    `今日: ↑${formatBytes(snap.todayUp || 0)} ↓${formatBytes(snap.todayDown || 0)}`,
    `累计: ↑${formatBytes(snap.totalUp || 0)} ↓${formatBytes(snap.totalDown || 0)}`,
    rule.lastStartTime ? `最近启动: ${rule.lastStartTime}` : '',
    rule.lastStopTime ? `最近停止: ${rule.lastStopTime}` : '',
  ].filter(Boolean).join('\n')
}

function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = bytes
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)}${units[i]}`
}

function formatSpeed(bytesPerSec) {
  return `${formatBytes(bytesPerSec)}/s`
}

async function handleHealthCheck(rule) {
  // 故障转移/链式代理经本地代理端口检测，需先启动
  if (rule._nodeType !== 'rule' && !rule.enabled) {
    appStore.showToast(`请先启动「${rule.alias}」再检测`, 'warning')
    return
  }
  try {
    await api.checkNodeHealth(rule.id)
    appStore.showToast(`正在检测: ${rule.alias}`, 'info')
  } catch (e) {
    appStore.showToast(`检测失败: ${e}`, 'error')
  }
}

function statusClass(rule) {
  if (rule.enabled) return 'status-running'
  if (rule.lastError) return 'status-failed'
  if (rule.testStatus === 'failed') return 'status-failed'
  return 'status-stopped'
}

async function handleStart(rule) {
  startingIds.value.add(rule.id)
  try {
    if (rule._nodeType === 'lb') {
      await api.startLoadBalancer(rule.id)
    } else if (rule._nodeType === 'chain') {
      await api.startChainProxy(rule.id)
    } else {
      await rulesStore.startRule(rule.id)
    }
    await rulesStore.loadRules()
  } catch (e) {
    appStore.showToast(`启动失败: ${e}`, 'error')
  } finally {
    startingIds.value.delete(rule.id)
  }
}

async function handleStop(rule) {
  try {
    if (rule._nodeType === 'lb') {
      await api.stopLoadBalancer(rule.id)
    } else if (rule._nodeType === 'chain') {
      await api.stopChainProxy(rule.id)
    } else {
      await rulesStore.stopRule(rule.id)
    }
    await rulesStore.loadRules()
  } catch (e) {
    appStore.showToast(`停止失败: ${e}`, 'error')
  }
}

async function handleTest(rule) {
  // 故障转移/链式代理需先启动（通过本地代理端口测速）
  if (rule._nodeType !== 'rule' && !rule.enabled) {
    appStore.showToast(`请先启动「${rule.alias}」再测速`, 'warning')
    return
  }
  try {
    if (rule._nodeType === 'lb') {
      await api.testLoadBalancerSpeed(rule.id)
    } else if (rule._nodeType === 'chain') {
      await api.testChainProxySpeed(rule.id)
    } else {
      await api.testRuleSpeed(rule.id)
    }
    appStore.showToast(`正在测速: ${rule.alias}`, 'info')
  } catch (e) {
    appStore.showToast(`测速失败: ${e}`, 'error')
  }
}

async function handleDelete(rule) {
  const typeLabel = rule._nodeType === 'lb' ? '故障转移' : (rule._nodeType === 'chain' ? '链式代理' : '规则')
  if (!confirm(`确定要删除${typeLabel}「${rule.alias}」吗?`)) return
  try {
    if (rule._nodeType === 'lb') {
      await api.deleteLoadBalancer(rule.id)
    } else if (rule._nodeType === 'chain') {
      await api.deleteChainProxy(rule.id)
    } else {
      await api.deleteRule(rule.id)
    }
    rulesStore.selectedIds.delete(rule.id)
    await rulesStore.loadRules()
  } catch (e) {
    appStore.showToast(`删除失败: ${e}`, 'error')
  }
}
</script>

<style scoped>
.node-list {
  flex: 1;
  overflow: auto;
  position: relative;
}

.loading-overlay {
  position: absolute;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(255, 255, 255, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10;
}

.spinner {
  width: 30px;
  height: 30px;
  border: 3px solid var(--border-color);
  border-top-color: var(--primary-color);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin { to { transform: rotate(360deg); } }

.rules-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.rules-table th {
  position: sticky;
  top: 0;
  background: var(--bg-secondary);
  padding: 8px 10px;
  text-align: left;
  font-weight: 500;
  font-size: 12px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border-color);
  white-space: nowrap;
  z-index: 1;
}

.rules-table td {
  padding: 7px 10px;
  border-bottom: 1px solid var(--border-color);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.rules-table tr:hover { background: var(--bg-hover); }

.rules-table tbody tr { cursor: default; }
/* 仅在按住 Shift 时禁止文本选择，避免范围多选拖蓝文字；平时可正常选中复制 */
.rules-table.shift-pressed tbody tr { user-select: none; }

.row-running { background: rgba(39, 174, 96, 0.05) !important; }
.row-lb-running { background: rgba(155, 89, 182, 0.08) !important; }
.row-chain-running { background: rgba(52, 152, 219, 0.08) !important; }
.row-testing { background: rgba(243, 156, 18, 0.05) !important; }

/* 选中行高亮（优先级高于运行状态底色） */
.row-selected,
.rules-table tbody tr.row-selected:hover { background: var(--primary-light) !important; }

.col-check { width: 36px; text-align: center; }
.col-alias { max-width: 120px; }
.col-protocol { width: 90px; }
.col-server { max-width: 140px; }
.col-sport { width: 70px; }
.col-lport { width: 70px; }
.col-health { width: 80px; }
.col-latency { width: 80px; }
.col-speed { width: 90px; }
.col-traffic { width: 130px; }
.col-traffic-total { width: 110px; }
.col-ip { max-width: 100px; }
.ip-error { color: #e74c3c; font-size: 11px; }
.col-status { width: 40px; text-align: center; }
.col-actions { width: 230px; }

.health-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 500;
}
.health-unknown { background: #f0f0f0; color: #999; }
.health-checking { background: #fef3e2; color: #e67e22; font-style: italic; }
.health-online { background: #e8f8f5; color: #27ae60; }
.health-high_latency { background: #fef3e2; color: #f39c12; }
.health-timeout { background: #fde8e8; color: #e74c3c; }
.health-dns_failed { background: #fde8e8; color: #c0392b; }
.health-tls_failed { background: #fde8e8; color: #c0392b; }
.health-reality_failed { background: #fde8e8; color: #8e44ad; }

.traffic-speed { font-size: 11px; color: var(--text-primary); }
.traffic-total { font-size: 11px; color: var(--text-secondary); }
.btn-health { color: #16a085; border-color: #16a085; }

.sortable { cursor: pointer; user-select: none; }
.sortable:hover { color: var(--primary-color); }
.sort-arrow { font-size: 10px; }

.empty-row {
  text-align: center;
  padding: 40px !important;
  color: var(--text-secondary);
}

.protocol-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 500;
}

.protocol-vmess { background: #e8f4fd; color: #2980b9; }
.protocol-vless { background: #fef3e2; color: #e67e22; }
.protocol-shadowsocks { background: #e8f8f5; color: #1abc9c; }
.protocol-trojan { background: #fde8e8; color: #e74c3c; }
.protocol-http { background: #f0f0f0; color: #555; }
.protocol-socks { background: #f0f0f0; color: #555; }
.protocol-loadbalance { background: #f3e8fd; color: #9b59b6; }
.protocol-chain { background: #e8f4fd; color: #2980b9; }
.protocol-hysteria2 { background: #e8fdf0; color: #16a085; }
.protocol-tuic { background: #fdf3e8; color: #d35400; }

.type-badge {
  display: inline-block;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 500;
  color: #fff;
  margin-right: 4px;
}
.badge-lb { background: #9b59b6; }
.badge-chain { background: #2980b9; }

.status-dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 50%;
}

.status-running { background: #27ae60; box-shadow: 0 0 6px rgba(39, 174, 96, 0.5); }
.status-failed { background: #e74c3c; }
.status-stopped { background: #bdc3c7; }

.latency-good { color: #27ae60; }
.latency-ok { color: #f39c12; }
.latency-bad { color: #e74c3c; }

.testing { color: #f39c12; font-style: italic; }
.no-data { color: var(--text-secondary); }

.btn-action-sm {
  padding: 3px 8px;
  border: 1px solid var(--border-color);
  border-radius: 3px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 11px;
  margin-right: 3px;
}

.btn-action-sm:hover { background: var(--bg-hover); }
.btn-start { color: #27ae60; border-color: #27ae60; }
.btn-start:hover { background: #27ae60; color: #fff; }
.btn-stop { color: #e74c3c; border-color: #e74c3c; }
.btn-stop:hover { background: #e74c3c; color: #fff; }
.btn-test { color: #3498db; border-color: #3498db; }
.btn-del { color: #95a5a6; }
.btn-del:hover { color: #e74c3c; border-color: #e74c3c; }

.table-footer {
  padding: 8px 16px;
  font-size: 12px;
  color: var(--text-secondary);
  border-top: 1px solid var(--border-color);
  background: var(--bg-secondary);
}
</style>
