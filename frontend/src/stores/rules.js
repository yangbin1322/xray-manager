import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api.js'

// groupFilter 的特殊值：仅显示未分组节点
export const UNGROUPED_FILTER = '__ungrouped__'

export const useRulesStore = defineStore('rules', () => {
  // === 状态 ===
  const rules = ref([])
  const loadBalancers = ref([])
  const chainProxies = ref([])
  const selectedIds = ref(new Set())
  const lastSelectedId = ref(null) // Shift 范围选的锚点（上次点击的行）
  const statusFilter = ref('all') // all | running | stopped
  const searchKeyword = ref('')
  const groupFilter = ref(null) // null=所有节点, UNGROUPED_FILTER=未分组, 其他=分组ID
  const sortColumn = ref(null)
  const sortDirection = ref('asc')
  const loading = ref(false)
  const clipboard = ref([]) // 复制的节点数据
  const traffic = ref({}) // 实时流量快照 { ruleId: { upSpeed, downSpeed, todayUp, todayDown, totalUp, totalDown } }

  // === 合并所有节点（规则 + 故障转移 + 链式代理） ===
  const allNodes = computed(() => {
    const ruleNodes = rules.value.map(r => ({ ...r, _nodeType: 'rule' }))
    const lbNodes = loadBalancers.value.map(lb => ({ ...lb, _nodeType: 'lb', protocol: 'loadbalance' }))
    const chainNodes = chainProxies.value.map(c => ({ ...c, _nodeType: 'chain', protocol: 'chain' }))
    return [...ruleNodes, ...lbNodes, ...chainNodes]
  })

  // === 计算属性 ===
  const filteredRules = computed(() => {
    let result = allNodes.value

    // 分组过滤：null=所有节点，UNGROUPED_FILTER=仅未分组，其余=指定分组
    if (groupFilter.value === UNGROUPED_FILTER) {
      result = result.filter(r => !r.groupId)
    } else if (groupFilter.value !== null) {
      result = result.filter(r => r.groupId === groupFilter.value)
    }

    // 状态过滤
    if (statusFilter.value === 'running') {
      result = result.filter(r => r.enabled)
    } else if (statusFilter.value === 'stopped') {
      result = result.filter(r => !r.enabled)
    }

    // 搜索过滤
    if (searchKeyword.value) {
      const kw = searchKeyword.value.toLowerCase()
      result = result.filter(r =>
        (r.alias && r.alias.toLowerCase().includes(kw)) ||
        (r.serverAddr && r.serverAddr.toLowerCase().includes(kw)) ||
        (r.protocol && r.protocol.toLowerCase().includes(kw)) ||
        (r.groupName && r.groupName.toLowerCase().includes(kw)) ||
        (r.localPort && String(r.localPort).includes(kw))
      )
    }

    // 排序
    if (sortColumn.value) {
      result = [...result].sort((a, b) => {
        const aVal = a[sortColumn.value] || 0
        const bVal = b[sortColumn.value] || 0
        return sortDirection.value === 'asc' ? aVal - bVal : bVal - aVal
      })
    }

    return result
  })

  const selectedRuleIds = computed(() => Array.from(selectedIds.value))

  const runningCount = computed(() => allNodes.value.filter(r => r.enabled).length)
  const totalCount = computed(() => allNodes.value.length)
  const ungroupedCount = computed(() => allNodes.value.filter(r => !r.groupId).length)

  // 各分组的节点数映射 { groupId: count }，一次遍历统计
  const groupCounts = computed(() => {
    const counts = {}
    for (const n of allNodes.value) {
      if (n.groupId) counts[n.groupId] = (counts[n.groupId] || 0) + 1
    }
    return counts
  })

  // === 动作 ===
  async function loadRules() {
    loading.value = true
    try {
      rules.value = await api.getRules() || []
      try { loadBalancers.value = await api.getLoadBalancers() || [] } catch { loadBalancers.value = [] }
      try { chainProxies.value = await api.getChainProxies() || [] } catch { chainProxies.value = [] }
    } catch (e) {
      console.error('加载规则失败:', e)
    } finally {
      loading.value = false
    }
  }

  async function addRule(rule) {
    await api.addRule(rule)
    await loadRules()
  }

  async function updateRule(id, rule) {
    await api.updateRule(id, rule)
    await loadRules()
  }

  async function updateNodes(rules) {
    await api.updateNodes(rules)
    await loadRules()
  }

  async function deleteRule(id) {
    await api.deleteRule(id)
    selectedIds.value.delete(id)
    await loadRules()
  }

  async function startRule(id) {
    await api.startRule(id)
    // 不需要 loadRules，后端会发 ruleUpdated 事件
  }

  async function stopRule(id) {
    await api.stopRule(id)
  }

  async function deleteSelectedRules() {
    const ids = selectedRuleIds.value
    for (const id of ids) {
      const node = allNodes.value.find(n => n.id === id)
      if (!node) continue
      try {
        if (node._nodeType === 'lb') {
          await api.deleteLoadBalancer(id)
        } else if (node._nodeType === 'chain') {
          await api.deleteChainProxy(id)
        } else {
          await api.deleteRule(id)
        }
      } catch (e) {
        console.error('删除失败:', e)
      }
    }
    selectedIds.value.clear()
    await loadRules()
  }

  async function startSelectedRules() {
    // 只启动当前未运行的选中节点，交给后端并发处理、只保存一次配置
    const ids = selectedRuleIds.value.filter(id => {
      const node = allNodes.value.find(n => n.id === id)
      return node && !node.enabled
    })
    if (ids.length === 0) return
    try {
      await api.startNodes(ids)
    } catch (e) { console.error(e) }
    await loadRules()
  }

  async function stopSelectedRules() {
    const ids = selectedRuleIds.value.filter(id => {
      const node = allNodes.value.find(n => n.id === id)
      return node && node.enabled
    })
    if (ids.length === 0) return
    try {
      await api.stopNodes(ids)
    } catch (e) { console.error(e) }
    await loadRules()
  }

  async function testSelectedSpeed() {
    const ids = selectedRuleIds.value
    if (ids.length === 0) return
    await api.testSelectedRulesSpeed(ids)
  }

  function updateRuleInList(updatedRule) {
    const idx = rules.value.findIndex(r => r.id === updatedRule.id)
    if (idx >= 0) {
      rules.value[idx] = { ...rules.value[idx], ...updatedRule }
    }
  }

  // 应用实时流量快照（trafficUpdate 事件）
  function applyTrafficUpdate(snap) {
    if (!snap || !snap.ruleId) return
    traffic.value = { ...traffic.value, [snap.ruleId]: snap }
  }

  // 应用健康检查结果（healthCheckResult 事件）
  function applyHealthCheckResult(result) {
    if (!result || !result.ruleId) return
    const patch = {
      healthStatus: result.status,
      healthLatency: result.latency,
      lastHealthCheck: result.timestamp,
    }
    // 结果 ID 可能属于普通节点/故障转移/链式代理，逐个数组查找
    for (const list of [rules, loadBalancers, chainProxies]) {
      const idx = list.value.findIndex(r => r.id === result.ruleId)
      if (idx >= 0) {
        list.value[idx] = { ...list.value[idx], ...patch }
        return
      }
    }
  }

  async function checkSelectedHealth() {
    // 普通节点直连检测，已启动的故障转移/链式代理经代理端口检测
    const ids = selectedRuleIds.value.filter(id => allNodes.value.some(n => n.id === id))
    if (ids.length === 0) return false
    await api.checkSelectedNodesHealth(ids)
    return true
  }

  async function checkAllHealth() {
    await api.checkAllNodesHealth()
  }

  async function resetTraffic(ruleID = '') {
    await api.resetRuleTraffic(ruleID)
    if (ruleID) {
      const snap = traffic.value[ruleID]
      if (snap) {
        traffic.value = { ...traffic.value, [ruleID]: { ...snap, todayUp: 0, todayDown: 0, totalUp: 0, totalDown: 0 } }
      }
    } else {
      traffic.value = {}
    }
    await loadRules()
  }

  function toggleSelect(id) {
    const next = new Set(selectedIds.value)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    selectedIds.value = next
    lastSelectedId.value = id
  }

  function selectAll(checked) {
    const next = new Set()
    if (checked) filteredRules.value.forEach(r => next.add(r.id))
    selectedIds.value = next
  }

  // 处理行点击的多选：
  // - 无修饰键：仅选中该行（清除其余）
  // - Ctrl/Meta：切换该行选中状态
  // - Shift：从锚点到该行范围全选（基于当前显示顺序 filteredRules）
  function handleRowSelect(id, { shift = false, ctrl = false } = {}) {
    if (shift && lastSelectedId.value != null) {
      const list = filteredRules.value
      const from = list.findIndex(r => r.id === lastSelectedId.value)
      const to = list.findIndex(r => r.id === id)
      if (from >= 0 && to >= 0) {
        const [lo, hi] = from <= to ? [from, to] : [to, from]
        const next = new Set(selectedIds.value)
        for (let i = lo; i <= hi; i++) next.add(list[i].id)
        selectedIds.value = next
        // Shift 选择不移动锚点，便于连续调整范围
        return
      }
    }

    if (ctrl) {
      toggleSelect(id)
      return
    }

    // 普通单击：仅选中该行
    selectedIds.value = new Set([id])
    lastSelectedId.value = id
  }

  function setSort(column) {
    if (sortColumn.value === column) {
      sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'
    } else {
      sortColumn.value = column
      sortDirection.value = 'asc'
    }
  }

  function copySelected() {
    if (selectedIds.value.size === 0) return 0
    clipboard.value = allNodes.value
      .filter(n => selectedIds.value.has(n.id))
      .map(n => JSON.parse(JSON.stringify(n)))
    return clipboard.value.length
  }

  async function pasteNodes() {
    if (clipboard.value.length === 0) return 0
    let count = 0
    for (const node of clipboard.value) {
      const copy = JSON.parse(JSON.stringify(node))
      copy.alias = (copy.alias || '') + ' (副本)'
      // 清除运行时状态
      delete copy.id
      delete copy.enabled
      delete copy.processId
      delete copy.latency
      delete copy.downloadSpeed
      delete copy.realIp
      delete copy.testStatus
      delete copy._nodeType
      try {
        if (node._nodeType === 'lb') {
          delete copy.protocol
          await api.addLoadBalancer(copy)
        } else if (node._nodeType === 'chain') {
          delete copy.protocol
          await api.addChainProxy(copy)
        } else {
          await api.addRule(copy)
        }
        count++
      } catch (e) {
        console.error('粘贴节点失败:', e)
      }
    }
    if (count > 0) await loadRules()
    return count
  }

  return {
    // State
    rules, loadBalancers, chainProxies,
    selectedIds, statusFilter, searchKeyword,
    groupFilter, sortColumn, sortDirection, loading, clipboard, traffic,
    // Computed
    filteredRules, selectedRuleIds, runningCount, totalCount, ungroupedCount, groupCounts,
    // Actions
    loadRules, addRule, updateRule, updateNodes, deleteRule,
    startRule, stopRule, deleteSelectedRules,
    startSelectedRules, stopSelectedRules, testSelectedSpeed,
    updateRuleInList, toggleSelect, selectAll, handleRowSelect, setSort,
    copySelected, pasteNodes,
    applyTrafficUpdate, applyHealthCheckResult,
    checkSelectedHealth, checkAllHealth, resetTraffic,
  }
})
