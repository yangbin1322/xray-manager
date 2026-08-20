<template>
  <div class="node-list" ref="scroller" @scroll.passive="onScroll">
    <div v-if="rulesStore.loading" class="loading-overlay">
      <div class="spinner"></div>
    </div>

    <!-- 列显示设置：点表头的 ☰ 打开，点空白处关闭 -->
    <div v-if="showColumnMenu" class="col-menu-mask" @click="showColumnMenu = false"></div>
    <div v-if="showColumnMenu" class="col-menu" :style="columnMenuStyle" @click.stop>
      <div class="col-menu-header">
        <span>显示列</span>
        <div class="col-menu-links">
          <button class="btn-link" @click="cols.setAll(true)">全选</button>
          <button class="btn-link" @click="cols.reset()">恢复默认</button>
        </div>
      </div>
      <label v-for="c in NODE_COLUMNS" :key="c.key" class="col-menu-item">
        <input type="checkbox" :checked="cols.isVisible(c.key)" @change="cols.toggle(c.key)" />
        {{ c.label }}
      </label>
      <div class="col-menu-hint">勾选框和操作列固定显示</div>
    </div>

    <table class="rules-table" :class="{ 'shift-pressed': shiftPressed }">
      <thead>
        <tr>
          <th class="col-check">
            <input type="checkbox" :checked="allSelected" @change="rulesStore.selectAll($event.target.checked)" />
          </th>
          <th v-if="cols.isVisible('alias')" class="col-alias sortable" :style="cols.styleOf('alias')"
              @click="rulesStore.setSort('alias')">
            别名<span v-if="rulesStore.sortColumn === 'alias'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('alias', $event)"></span>
          </th>
          <th v-if="cols.isVisible('remark')" class="col-remark sortable" :style="cols.styleOf('remark')"
              @click="rulesStore.setSort('remark')">
            备注<span v-if="rulesStore.sortColumn === 'remark'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('remark', $event)"></span>
          </th>
          <th v-if="cols.isVisible('group')" class="col-group" :style="cols.styleOf('group')">
            所属分组
            <span class="col-resizer" @mousedown.stop.prevent="startResize('group', $event)"></span>
          </th>
          <th v-if="cols.isVisible('protocol')" class="col-protocol sortable" :style="cols.styleOf('protocol')"
              @click="rulesStore.setSort('protocol')">
            协议<span v-if="rulesStore.sortColumn === 'protocol'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('protocol', $event)"></span>
          </th>
          <th v-if="cols.isVisible('server')" class="col-server sortable" :style="cols.styleOf('server')"
              @click="rulesStore.setSort('serverAddr')">
            服务器地址<span v-if="rulesStore.sortColumn === 'serverAddr'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('server', $event)"></span>
          </th>
          <th v-if="cols.isVisible('sport')" class="col-sport sortable" :style="cols.styleOf('sport')"
              @click="rulesStore.setSort('serverPort')">
            服务器端口<span v-if="rulesStore.sortColumn === 'serverPort'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('sport', $event)"></span>
          </th>
          <th v-if="cols.isVisible('lport')" class="col-lport sortable" :style="cols.styleOf('lport')"
              @click="rulesStore.setSort('localPort')">
            本地端口<span v-if="rulesStore.sortColumn === 'localPort'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('lport', $event)"></span>
          </th>
          <th v-if="cols.isVisible('health')" class="col-health sortable" :style="cols.styleOf('health')"
              @click="rulesStore.setSort('healthLatency')">
            健康<span v-if="rulesStore.sortColumn === 'healthLatency'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('health', $event)"></span>
          </th>
          <th v-if="cols.isVisible('latency')" class="col-latency sortable" :style="cols.styleOf('latency')"
              @click="rulesStore.setSort('latency')">
            延迟<span v-if="rulesStore.sortColumn === 'latency'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('latency', $event)"></span>
          </th>
          <th v-if="cols.isVisible('speed')" class="col-speed sortable" :style="cols.styleOf('speed')"
              @click="rulesStore.setSort('downloadSpeed')">
            速度<span v-if="rulesStore.sortColumn === 'downloadSpeed'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('speed', $event)"></span>
          </th>
          <th v-if="cols.isVisible('traffic')" class="col-traffic" :style="cols.styleOf('traffic')">
            实时流量
            <span class="col-resizer" @mousedown.stop.prevent="startResize('traffic', $event)"></span>
          </th>
          <th v-if="cols.isVisible('trafficTotal')" class="col-traffic-total" :style="cols.styleOf('trafficTotal')">
            今日/累计
            <span class="col-resizer" @mousedown.stop.prevent="startResize('trafficTotal', $event)"></span>
          </th>
          <th v-if="cols.isVisible('ip')" class="col-ip sortable" :style="cols.styleOf('ip')"
              @click="rulesStore.setSort('realIp')">
            真实IP<span v-if="rulesStore.sortColumn === 'realIp'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('ip', $event)"></span>
          </th>
          <th v-if="cols.isVisible('boundIp')" class="col-bound-ip sortable" :style="cols.styleOf('boundIp')"
              @click="rulesStore.setSort('boundExitIP')">
            绑定IP<span v-if="rulesStore.sortColumn === 'boundExitIP'" class="sort-arrow">{{ sortArrow }}</span>
            <span class="col-resizer" @mousedown.stop.prevent="startResize('boundIp', $event)"></span>
          </th>
          <th v-if="cols.isVisible('status')" class="col-status" :style="cols.styleOf('status')">状态</th>
          <th class="col-actions">
            操作
            <!-- 列设置入口放在表头最右侧，靠近它要控制的对象 -->
            <button class="col-config-btn" title="显示/隐藏列" @click.stop="openColumnMenu">☰</button>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="rulesStore.filteredRules.length === 0">
          <td :colspan="cols.visibleCount" class="empty-row">暂无节点数据</td>
        </tr>
        <!-- 虚拟滚动：只渲染可视区域内的行，用上下两个空行撑出滚动高度。
             订阅动辄上万节点，全量渲染会让滚动、点击都卡死。 -->
        <tr v-if="topPadding > 0" class="v-spacer" :style="{ height: topPadding + 'px' }"></tr>
        <tr
          v-for="rule in visibleRules"
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
          <td v-if="cols.isVisible('alias')" class="col-alias" :title="rule.alias">
            <span v-if="rule._nodeType === 'lb'" class="type-badge badge-lb" title="故障转移">转</span>
            <span v-if="rule._nodeType === 'chain'" class="type-badge badge-chain" title="链式代理">链</span>
            <span v-if="rule._nodeType === 'relay'" class="type-badge badge-relay" title="动态会话代理">会</span>
            {{ rule.alias || '-' }}
          </td>
          <td v-if="cols.isVisible('remark')" class="col-remark" :title="rule.remark">
            {{ rule.remark || '-' }}
          </td>
          <td v-if="cols.isVisible('group')" class="col-group" :title="groupTitle(rule)">
            <span v-if="rule.groupName" class="group-cell">{{ rule.groupName }}</span>
            <span v-else class="no-data">未分组</span>
          </td>
          <td v-if="cols.isVisible('protocol')" class="col-protocol">
            <span :class="['protocol-badge', `protocol-${rule.protocol}`]">{{ protocolLabel(rule.protocol) }}</span>
          </td>
          <!-- 普通节点显示服务器信息，LB/链显示节点数 -->
          <td v-if="cols.isVisible('server')" class="col-server" :title="rule.serverAddr">
            <template v-if="rule._nodeType === 'rule'">{{ rule.serverAddr || '-' }}</template>
            <template v-else-if="rule._nodeType === 'lb'">{{ (rule.nodeIds || []).length }} 个子节点</template>
            <template v-else-if="rule._nodeType === 'relay'">{{ rule.upstreamAddr || '-' }}</template>
            <template v-else>{{ (rule.chainNodes || []).length }} 节点链</template>
          </td>
          <td v-if="cols.isVisible('sport')" class="col-sport">
            <template v-if="rule._nodeType === 'rule'">{{ rule.serverPort || '-' }}</template>
            <template v-else>-</template>
          </td>
          <td v-if="cols.isVisible('lport')" class="col-lport">
            <span
              v-if="rule.localPort > 0"
              class="lport-copy"
              title="点击复制本地 HTTP 代理地址"
              @click.stop="copyLocalProxy(rule)"
            >{{ rule.localPort }}</span>
            <template v-else>-</template>
          </td>
          <td v-if="cols.isVisible('health')" class="col-health">
            <span
              :class="['health-badge', `health-${rule.healthStatus || 'unknown'}`]"
              :title="healthTitle(rule)"
            >{{ healthLabel(rule) }}</span>
          </td>
          <td v-if="cols.isVisible('latency')" class="col-latency">
            <span v-if="rule.testStatus === 'testing'" class="testing">测速中...</span>
            <span v-else-if="rule.latency > 0" :class="latencyClass(rule.latency)">
              {{ rule.latency }}ms
            </span>
            <span v-else class="no-data">-</span>
          </td>
          <td v-if="cols.isVisible('speed')" class="col-speed">
            <span v-if="rule.downloadSpeed > 0">{{ rule.downloadSpeed.toFixed(2) }} MB/s</span>
            <span v-else class="no-data">-</span>
          </td>
          <td v-if="cols.isVisible('traffic')" class="col-traffic">
            <template v-if="rule.enabled && trafficOf(rule)">
              <span class="traffic-speed">↑{{ formatSpeed(trafficOf(rule).upSpeed) }} ↓{{ formatSpeed(trafficOf(rule).downSpeed) }}</span>
            </template>
            <span v-else class="no-data">-</span>
          </td>
          <td v-if="cols.isVisible('trafficTotal')" class="col-traffic-total" :title="trafficTitle(rule)">
            <span class="traffic-total">{{ formatBytes(todayTotal(rule)) }} / {{ formatBytes(allTotal(rule)) }}</span>
          </td>
          <td v-if="cols.isVisible('ip')" class="col-ip" :title="rule.lastError || relayTitle(rule) || rule.realIp">
            <span v-if="rule.lastError" class="ip-error">{{ rule.lastError }}</span>
            <span v-else-if="rule._nodeType === 'relay'">{{ relaySummary(rule) }}</span>
            <span v-else>{{ rule.realIp || '-' }}</span>
          </td>
          <!-- 绑定/解绑做成单元格点击：操作列已经排了五个按钮，再加会溢出，
               而绑定是低频的单节点配置操作，放在它自己的列里更顺手 -->
          <td v-if="cols.isVisible('boundIp')" class="col-bound-ip" :title="boundIpTitle(rule)" @click.stop>
            <span v-if="rule._nodeType !== 'rule'" class="no-data">-</span>
            <span
              v-else-if="!rule.bindExitIP"
              class="bound-action"
              :class="{ 'is-disabled': !rule.realIp }"
              @click="rule.realIp && handleBindIP(rule)"
            >绑定</span>
            <span v-else-if="!rule.boundExitIP" class="bound-pending" @click="handleBindIP(rule)">待绑定</span>
            <span v-else class="bound-ip" @click="handleBindIP(rule)">{{ rule.boundExitIP }}</span>
          </td>
          <td v-if="cols.isVisible('status')" class="col-status">
            <span :class="['status-dot', statusClass(rule)]" :title="statusTitle(rule)"></span>
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
            <button v-if="rule._nodeType === 'relay'" class="btn-action-sm" @click="$emit('editRelay', rule)">编辑</button>
            <!-- 会话代理的出口 IP 由客户端用户名决定，统一测速没有意义 -->
            <button
              v-if="rule._nodeType !== 'relay'"
              class="btn-action-sm btn-test"
              @click="handleTest(rule)"
            >测速</button>
          </td>
        </tr>
        <tr v-if="bottomPadding > 0" class="v-spacer" :style="{ height: bottomPadding + 'px' }"></tr>
      </tbody>
    </table>

    <!-- 统计信息 -->
    <div class="table-footer">
      <span>总计 {{ rulesStore.totalCount }} 个节点，运行中 {{ rulesStore.runningCount }} 个</span>
      <!-- 验证中单独计数：这些节点已启动但还不确定能否用，不计入运行中 -->
      <span v-if="rulesStore.verifyingCount > 0">，验证中 {{ rulesStore.verifyingCount }} 个</span>
      <span v-if="rulesStore.filteredRules.length !== rulesStore.totalCount">
        ，当前筛选 {{ rulesStore.filteredRules.length }} 个
      </span>
      <!-- 有列被隐藏时明确提示，避免用户以为数据丢了 -->
      <button v-if="cols.hiddenCount > 0" class="btn-link footer-link" @click.stop="openColumnMenu">
        已隐藏 {{ cols.hiddenCount }} 列
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useColumnsStore, NODE_COLUMNS } from '../stores/columns.js'
import { useAppStore } from '../stores/app.js'
import * as api from '../api.js'

const emit = defineEmits(['editRule', 'editLB', 'editChain', 'editRelay'])

const rulesStore = useRulesStore()
const cols = useColumnsStore()
const appStore = useAppStore()

// 当前排序方向的箭头，各可排序列共用
const sortArrow = computed(() => (rulesStore.sortDirection === 'asc' ? '▲' : '▼'))

// ===== 列宽拖动 =====
//
// 表格是 table-layout: fixed，给 th 设了宽度就会生效。
// 拖动过程中直接改 store 里的宽度（响应式，立即可见），
// 松手后才落盘——每帧写一次 localStorage 会明显掉帧。
let resizeState = null

function startResize(key, event) {
  const th = event.currentTarget.parentElement
  resizeState = {
    key,
    startX: event.clientX,
    startWidth: th.getBoundingClientRect().width,
  }
  window.addEventListener('mousemove', onResizing)
  window.addEventListener('mouseup', stopResize)
  // 拖动时禁掉文本选中，否则会选中表头文字、光标也会闪
  document.body.style.userSelect = 'none'
  document.body.style.cursor = 'col-resize'
}

function onResizing(event) {
  if (!resizeState) return
  cols.setWidth(resizeState.key, resizeState.startWidth + (event.clientX - resizeState.startX))
}

function stopResize() {
  if (!resizeState) return
  resizeState = null
  window.removeEventListener('mousemove', onResizing)
  window.removeEventListener('mouseup', stopResize)
  document.body.style.userSelect = ''
  document.body.style.cursor = ''
  cols.commitWidths()
}

// ===== 列显示菜单 =====
// 菜单用 fixed 定位，打开时按触发按钮的位置算一次坐标
const showColumnMenu = ref(false)
const columnMenuStyle = ref({})

function openColumnMenu(event) {
  if (showColumnMenu.value) {
    showColumnMenu.value = false
    return
  }
  const rect = event.currentTarget.getBoundingClientRect()
  // 贴着按钮右对齐；靠近视口右边时收一点，避免菜单被裁掉
  const right = Math.max(8, window.innerWidth - rect.right - 4)
  // 下方放不下（例如从底栏打开）就向上弹，否则菜单会掉出视口
  const estimatedHeight = Math.min(NODE_COLUMNS.length * 27 + 70, window.innerHeight * 0.7)
  const style = { right: `${right}px` }
  if (rect.bottom + estimatedHeight + 8 > window.innerHeight) {
    style.bottom = `${window.innerHeight - rect.top + 4}px`
  } else {
    style.top = `${rect.bottom + 4}px`
  }
  columnMenuStyle.value = style
  showColumnMenu.value = true
}
// Set 本身的 add/delete 不是响应式的，必须整体替换才能触发重渲染，
// 否则"启动中..."的按钮状态不会更新
const startingIds = ref(new Set())
const shiftPressed = ref(false) // 按住 Shift 时临时禁用文本选择

// ===== 虚拟滚动 =====
// 行高与 CSS 中的 padding + 字号保持一致；变动时同步修改 ROW_HEIGHT
const ROW_HEIGHT = 31
const OVERSCAN = 8 // 视口上下各多渲染几行，减少快速滚动时的白屏
const scroller = ref(null)
const scrollTop = ref(0)
const viewportHeight = ref(600)

function onScroll() {
  const el = scroller.value
  if (el) scrollTop.value = el.scrollTop
}

function measureViewport() {
  const el = scroller.value
  if (el) viewportHeight.value = el.clientHeight || 600
}

let resizeObserver = null

const startIndex = computed(() => {
  const first = Math.floor(scrollTop.value / ROW_HEIGHT) - OVERSCAN
  return Math.max(0, first)
})

const endIndex = computed(() => {
  const count = Math.ceil(viewportHeight.value / ROW_HEIGHT) + OVERSCAN * 2
  return Math.min(rulesStore.filteredRules.length, startIndex.value + count)
})

const visibleRules = computed(() => rulesStore.filteredRules.slice(startIndex.value, endIndex.value))

const topPadding = computed(() => startIndex.value * ROW_HEIGHT)
const bottomPadding = computed(() =>
  Math.max(0, (rulesStore.filteredRules.length - endIndex.value) * ROW_HEIGHT)
)

// 过滤/搜索导致列表变短时，滚动位置可能超出新内容高度，回到顶部避免空白
watch(() => rulesStore.filteredRules.length, (len) => {
  const maxTop = Math.max(0, len * ROW_HEIGHT - viewportHeight.value)
  if (scrollTop.value > maxTop) {
    scrollTop.value = 0
    nextTick(() => { if (scroller.value) scroller.value.scrollTop = 0 })
  }
})

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
    appStore.confirmDialog(`确定要删除选中的 ${count} 条规则吗？`, {
      title: '删除规则', confirmText: '删除',
    }).then(ok => {
      if (!ok) return
      return rulesStore.deleteSelectedRules()
        .then(() => appStore.showToast(`已删除 ${count} 条规则`, 'success'))
        .catch(error => appStore.showToast(`删除失败: ${error}`, 'error'))
    })
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

  measureViewport()
  if (scroller.value && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(measureViewport)
    resizeObserver.observe(scroller.value)
  }
})
onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('keyup', handleKeyup)
  window.removeEventListener('blur', resetShift)
  if (resizeObserver) resizeObserver.disconnect()
  // 拖动中途卸载时收尾，避免监听器和光标样式残留
  stopResize()
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

function groupTitle(rule) {
  return rule.groupName || '未分组'
}

function rowClass(rule) {
  if (rule._nodeType === 'lb' && rule.enabled) return 'row-lb-running'
  if (rule._nodeType === 'chain' && rule.enabled) return 'row-chain-running'
  if (rule._nodeType === 'relay' && rule.enabled) return 'row-relay-running'
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
const protocolLabels = { loadbalance: '故障转移', chain: '链式代理', relay: '会话代理' }
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
  ipv6_only: '仅IPv6',
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

// ===== 动态会话代理展示 =====
// 出口 IP 由客户端用户名决定，没有单一"真实 IP"，改为展示会话/连接数
function relaySummary(rule) {
  if (!rule.enabled) return '-'
  return `${rule.sessionCount || 0} 会话 / ${rule.activeConns || 0} 连接`
}

function relayTitle(rule) {
  if (rule._nodeType !== 'relay') return ''
  return [
    `上游网关: ${rule.upstreamAddr || '-'}`,
    `用户名模板: ${rule.usernameTemplate || '（原样透传）'}`,
    rule.preProxyNodeId ? '经前置节点加速' : '直连上游',
    `累计连接: ${rule.totalConns || 0}`,
  ].join('\n')
}

// ===== 出口 IP 绑定 =====
function boundIpTitle(rule) {
  if (rule._nodeType !== 'rule') return ''
  if (!rule.bindExitIP) {
    return rule.realIp
      ? `点击把当前出口 IP ${rule.realIp} 固定下来`
      : '需要先启动节点并拿到真实出口 IP 才能绑定'
  }
  if (!rule.boundExitIP) return '已启用绑定，将以首次获取到的真实 IP 为准\n点击解除绑定'
  return `已绑定出口 IP ${rule.boundExitIP}\n启动时实际出口 IP 与此不符会自动停用\n点击解除绑定`
}

async function handleBindIP(rule) {
  try {
    if (rule.bindExitIP) {
      await api.unbindIP(rule.id)
      appStore.showToast(`「${rule.alias}」已解除出口 IP 绑定`, 'success')
    } else {
      await api.bindCurrentIP(rule.id)
      appStore.showToast(`「${rule.alias}」已绑定出口 IP ${rule.realIp}`, 'success')
    }
    await rulesStore.loadRules()
  } catch (e) {
    appStore.showToast(`操作失败: ${e}`, 'error')
  }
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

// 点击本地端口，复制该节点的本地 HTTP 代理地址。
//
// 与工具栏的批量复制格式保持一致，便于直接粘进爬虫等程序。
// 未启动的节点也允许复制：地址本身是确定的，用户可能先取地址再启动。
async function copyLocalProxy(rule) {
  const address = `http://127.0.0.1:${rule.localPort}`
  try {
    await navigator.clipboard.writeText(address)
    appStore.showToast(`已复制 ${address}`, 'success')
  } catch (e) {
    appStore.showToast(`复制失败: ${e}`, 'error')
  }
}

function statusClass(rule) {
  // 已启动但连通性还没验证完：不能显示成「运行中」，
  // 否则用户会以为节点已经可用，而它可能几秒后就被判定不通并自动停用
  if (rule.verifying) return 'status-verifying'
  if (rule.enabled) return 'status-running'
  if (rule.lastError) return 'status-failed'
  if (rule.testStatus === 'failed') return 'status-failed'
  return 'status-stopped'
}

// 状态圆点的悬停说明。失败时优先显示具体原因。
function statusTitle(rule) {
  if (rule.verifying) return '正在验证连通性…'
  if (rule.lastError) return rule.lastError
  if (rule.enabled) return '运行中'
  return '已停止'
}

// Set 需整体替换才会触发视图更新
function markStarting(id, starting) {
  const next = new Set(startingIds.value)
  if (starting) next.add(id)
  else next.delete(id)
  startingIds.value = next
}

async function handleStart(rule) {
  if (startingIds.value.has(rule.id)) return
  markStarting(rule.id, true)
  try {
    await startNode(rule)
    await rulesStore.loadRules()
  } catch (e) {
    // 端口被别的程序占着时，光报「启动失败」用户无从下手：
    // 查出占用者，问一句要不要结束它，愿意就结束并自动重试
    if (await offerKillPortOccupants(rule, e)) {
      try {
        await startNode(rule)
        await rulesStore.loadRules()
        appStore.showToast(`「${rule.alias}」已启动`, 'success')
        return
      } catch (retryErr) {
        appStore.showToast(`启动失败: ${retryErr}`, 'error')
        return
      }
    }
    appStore.showToast(`启动失败: ${e}`, 'error')
  } finally {
    markStarting(rule.id, false)
  }
}

function startNode(rule) {
  if (rule._nodeType === 'lb') return api.startLoadBalancer(rule.id)
  if (rule._nodeType === 'chain') return api.startChainProxy(rule.id)
  if (rule._nodeType === 'relay') return api.startSessionRelay(rule.id)
  return rulesStore.startRule(rule.id)
}

// 启动失败疑似端口被占用时，询问用户是否结束占用进程。
// 返回 true 表示已结束、调用方可以重试启动。
async function offerKillPortOccupants(rule, err) {
  if (!/端口|port/i.test(String(err))) return false

  let info
  try {
    info = await api.inspectNodePort(rule.id)
  } catch {
    return false // 查不到占用信息就按普通失败处理
  }
  const killable = (info?.occupants || []).filter(o => o.killable)
  if (killable.length === 0) {
    // 端口空着（失败另有原因），或占用者不能安全终止（系统进程/权限不足）
    const blocked = (info?.occupants || []).find(o => o.reason)
    if (blocked) {
      appStore.showToast(`端口 ${info.port} 被 ${blocked.name || '未知进程'} 占用：${blocked.reason}`, 'warning')
    }
    return false
  }

  const list = killable.map(o => `  · ${o.name || '未知进程'} (PID: ${o.pid})${o.self ? '（本客户端遗留的内核进程）' : ''}`).join('\n')
  const ok = await appStore.confirmDialog(
    `节点「${rule.alias}」的本地端口 ${info.port} 已被以下进程占用：\n\n${list}\n\n结束这些进程后重新启动该节点？`,
    { title: '端口被占用', confirmText: '结束并启动', cancelText: '取消' },
  )
  if (!ok) return false

  try {
    await api.killPortOccupants(Number(info.port), killable.map(o => o.pid))
    return true
  } catch (killErr) {
    appStore.showToast(`结束进程失败: ${killErr}`, 'error')
    return false
  }
}

async function handleStop(rule) {
  try {
    if (rule._nodeType === 'lb') {
      await api.stopLoadBalancer(rule.id)
    } else if (rule._nodeType === 'chain') {
      await api.stopChainProxy(rule.id)
    } else if (rule._nodeType === 'relay') {
      await api.stopSessionRelay(rule.id)
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
  /* 固定布局：虚拟滚动下每次只渲染部分行，自动布局会让列宽随可视内容跳动 */
  table-layout: fixed;
  /* 定宽列合计约 894px，再给文本列留出可读空间。
     窗口更宽时文本列自动摊开；比这更窄则退化为表格内横向滚动，
     而不是把操作按钮挤到不可用 */
  min-width: 1140px;
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

/* 行高必须固定且与 JS 中的 ROW_HEIGHT 一致，否则虚拟滚动的位移会算错 */
.rules-table tbody tr { height: 31px; }
/* 撑开滚动高度的占位行，不参与样式 */
.rules-table tbody tr.v-spacer { height: auto; }
.rules-table tbody tr.v-spacer:hover { background: transparent; }

.rules-table tr:hover { background: var(--bg-hover); }

.rules-table tbody tr { cursor: default; }
/* 仅在按住 Shift 时禁止文本选择，避免范围多选拖蓝文字；平时可正常选中复制 */
.rules-table.shift-pressed tbody tr { user-select: none; }

.row-running { background: rgba(39, 174, 96, 0.05) !important; }
.row-lb-running { background: rgba(155, 89, 182, 0.08) !important; }
.row-chain-running { background: rgba(52, 152, 219, 0.08) !important; }
.row-relay-running { background: rgba(230, 126, 34, 0.08) !important; }
.row-testing { background: rgba(243, 156, 18, 0.05) !important; }

/* 选中行高亮（优先级高于运行状态底色） */
.row-selected,
.rules-table tbody tr.row-selected:hover { background: var(--primary-light) !important; }

/* 列宽策略：
   数值/徽标类列内容长度固定，给精确的 px；文本类列（别名、地址、分组、IP）
   不指定 width，由 table-layout: fixed 把剩余空间平均分给它们——
   窗口变宽时它们一起变宽，隐藏部分列后剩下的也会自动摊开，不留空白。
   不用百分比：百分比与固定列相加容易超过 100%，反而会撑出横向滚动条。
   超出的文本统一省略号（td 已设 ellipsis）。 */
.col-check { width: 34px; text-align: center; }
.col-protocol { width: 72px; }
.group-cell { color: var(--text-secondary); }
.col-sport { width: 58px; }
.col-lport { width: 58px; }
/* 本地端口可点击复制：默认不加色，避免整列变花；悬停时才提示可点 */
.lport-copy { cursor: pointer; }
.lport-copy:hover {
  color: var(--primary-color);
  text-decoration: underline;
}
.col-health { width: 70px; }
.col-latency { width: 62px; }
.col-speed { width: 74px; }
.col-traffic { width: 98px; }
.col-traffic-total { width: 86px; }
.ip-error { color: #e74c3c; font-size: 11px; }
.col-status { width: 36px; text-align: center; }
/* 三个按钮（启停/编辑/测速）各约 47px，合计约 141px，留点余量。
   检测与删除已移除——两者在工具栏都有批量入口（勾选后「健康检测」「删除」），
   逐行放一份既挤又容易误删。
   这列宽度是按按钮个数算死的，新增的单节点操作请放到它自己的列里做成
   可点击单元格（参考本地端口列的复制、绑定 IP 列的绑定/解绑），不要往这里塞。 */
.col-actions { width: 152px; }

/* 操作列放的是按钮不是文本：td 上的 text-overflow 会把最后一个按钮
   截成省略号。这里取消截断，让按钮完整显示。 */
.rules-table td.col-actions {
  overflow: visible;
  text-overflow: clip;
}
/* 以下列不设 width，自动瓜分剩余宽度 */
.col-alias,
.col-remark,
.col-group,
.col-server,
.col-ip { width: auto; }

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
/* 仅 IPv6：不是故障，是本机没有 IPv6 出口检测不了，用中性色区别于失败 */
.health-ipv6_only { background: #eef2f7; color: #5b6b7d; }

.traffic-speed { font-size: 11px; color: var(--text-primary); }
.traffic-total { font-size: 11px; color: var(--text-secondary); }
/* 绑定 IP 列：内容是定长的 IPv4，给固定宽度即可 */
.col-bound-ip { width: 120px; }
/* 绑定列的三种状态都可点击，交互与本地端口列一致 */
.bound-ip,
.bound-pending,
.bound-action { cursor: pointer; }
.bound-ip:hover,
.bound-pending:hover,
.bound-action:hover { text-decoration: underline; }
.bound-ip { color: #2980b9; font-size: 12px; }
.bound-pending { color: var(--text-secondary); font-size: 11px; }
.bound-action { color: var(--text-secondary); font-size: 11px; }
/* 还没拿到真实出口 IP 时无从绑定，置灰且不响应点击 */
.bound-action.is-disabled { cursor: not-allowed; opacity: 0.5; }
.bound-action.is-disabled:hover { text-decoration: none; }

.sortable { cursor: pointer; user-select: none; }
.sortable:hover { color: var(--primary-color); }
.sort-arrow { font-size: 10px; margin-left: 2px; }

/* 列宽拖动把手：贴在表头右缘的一条窄热区。
   th 是 sticky，本身就建立了定位上下文，这里直接绝对定位即可。 */
.col-resizer {
  position: absolute;
  top: 0;
  right: 0;
  width: 6px;
  height: 100%;
  cursor: col-resize;
  /* 半透明竖线在浅色/深色主题下都可见，且不喧宾夺主 */
  border-right: 2px solid transparent;
  transition: border-color 0.15s;
}
.col-resizer:hover,
.col-resizer:active {
  border-right-color: var(--primary-color);
}

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
.protocol-relay { background: #fdf0e3; color: #e67e22; }
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
.badge-relay { background: #e67e22; }

.status-dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 50%;
}

.status-running { background: #27ae60; box-shadow: 0 0 6px rgba(39, 174, 96, 0.5); }
.status-failed { background: #e74c3c; }
.status-stopped { background: #bdc3c7; }

/* 验证中：琥珀色 + 呼吸效果。既区别于「运行中」的绿色，
   也不像红色那样让人误以为出错——此时结论还没出来。 */
.status-verifying {
  background: #f39c12;
  animation: status-pulse 1.2s ease-in-out infinite;
}

@keyframes status-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

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

/* ===== 列显示设置 ===== */
.col-config-btn {
  float: right;
  border: none;
  background: none;
  cursor: pointer;
  color: var(--text-secondary);
  font-size: 12px;
  padding: 0 2px;
  line-height: 1;
}
.col-config-btn:hover { color: var(--primary-color); }

/* 遮罩负责"点外面关闭"，z-index 低于菜单本身 */
.col-menu-mask {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  z-index: 50;
}
/* 用 fixed 而不是 absolute：容器本身是滚动区，absolute 会让菜单跟着内容滚走。
   位置由打开时按钮的实际坐标算出（见 openColumnMenu）。 */
.col-menu {
  position: fixed;
  z-index: 51;
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: 6px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.18);
  padding: 8px 0;
  min-width: 170px;
  max-height: 70vh;
  overflow-y: auto;
}
.col-menu-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 4px 12px 8px;
  border-bottom: 1px solid var(--border-color);
  font-size: 12px;
  color: var(--text-secondary);
}
.col-menu-links { display: flex; gap: 8px; }
.col-menu-item {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 5px 12px;
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
}
.col-menu-item:hover { background: var(--bg-hover); }
.col-menu-hint {
  padding: 6px 12px 2px;
  border-top: 1px solid var(--border-color);
  font-size: 11px;
  color: var(--text-secondary);
}

.btn-link {
  border: none;
  background: none;
  padding: 0;
  cursor: pointer;
  color: var(--primary-color);
  font-size: 11px;
}
.btn-link:hover { text-decoration: underline; }
.footer-link { margin-left: 10px; }

.table-footer {
  position: sticky;
  bottom: 0;
  padding: 8px 16px;
  font-size: 12px;
  color: var(--text-secondary);
  border-top: 1px solid var(--border-color);
  background: var(--bg-secondary);
}
</style>
