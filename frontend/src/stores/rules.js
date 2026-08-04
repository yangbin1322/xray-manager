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
  const sessionRelays = ref([])
  const selectedIds = ref(new Set())
  const lastSelectedId = ref(null) // Shift 范围选的锚点（上次点击的行）
  const statusFilter = ref('all') // all | running | stopped
  const searchKeyword = ref('')
  const groupFilter = ref(null) // null=所有节点, UNGROUPED_FILTER=未分组, 其他=分组ID
  const sortColumn = ref(null)
  const sortDirection = ref('asc')
  const loading = ref(false)
  const clipboard = ref([]) // 最近写入系统剪贴板的普通节点 ID
  const traffic = ref({}) // 实时流量快照 { ruleId: { upSpeed, downSpeed, todayUp, todayDown, totalUp, totalDown } }

  // === 合并所有节点（规则 + 故障转移 + 链式代理 + 动态会话代理） ===
  const allNodes = computed(() => {
    const ruleNodes = rules.value.map(r => ({ ...r, _nodeType: 'rule' }))
    const lbNodes = loadBalancers.value.map(lb => ({ ...lb, _nodeType: 'lb', protocol: 'loadbalance' }))
    const chainNodes = chainProxies.value.map(c => ({ ...c, _nodeType: 'chain', protocol: 'chain' }))
    // 会话代理的上游网关填入 serverAddr，便于列表统一展示和搜索
    const relayNodes = sessionRelays.value.map(r => ({
      ...r, _nodeType: 'relay', protocol: 'relay', serverAddr: r.upstreamAddr,
    }))
    return [...ruleNodes, ...lbNodes, ...chainNodes, ...relayNodes]
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
      try { sessionRelays.value = await api.getSessionRelays() || [] } catch { sessionRelays.value = [] }
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
    if (selectedIds.value.has(id)) {
      const next = new Set(selectedIds.value)
      next.delete(id)
      selectedIds.value = next
    }
    await loadRules()
  }

  async function startRule(id) {
    await api.startRule(id)
    // 不需要 loadRules，后端会发 ruleUpdated 事件
  }

  async function stopRule(id) {
    await api.stopRule(id)
  }

  // 节点 ID -> 节点 的索引。批量操作原先对每个选中 ID 都做一次 allNodes.find（O(n²)），
  // 上万节点时仅仅"删除/启动选中项"就要卡住主线程。
  const nodeById = computed(() => {
    const map = new Map()
    for (const n of allNodes.value) map.set(n.id, n)
    return map
  })

  async function deleteSelectedRules() {
    const ids = selectedRuleIds.value
    const index = nodeById.value
    for (const id of ids) {
      const node = index.get(id)
      if (!node) continue
      try {
        if (node._nodeType === 'lb') {
          await api.deleteLoadBalancer(id)
        } else if (node._nodeType === 'chain') {
          await api.deleteChainProxy(id)
        } else if (node._nodeType === 'relay') {
          await api.deleteSessionRelay(id)
        } else {
          await api.deleteRule(id)
        }
      } catch (e) {
        console.error('删除失败:', e)
      }
    }
    selectedIds.value = new Set()
    await loadRules()
  }

  async function startSelectedRules() {
    // 只启动当前未运行的选中节点，交给后端并发处理、只保存一次配置
    const index = nodeById.value
    const ids = selectedRuleIds.value.filter(id => {
      const node = index.get(id)
      return node && !node.enabled
    })
    if (ids.length === 0) return
    try {
      await api.startNodes(ids)
    } catch (e) { console.error(e) }
    await loadRules()
  }

  async function stopSelectedRules() {
    const index = nodeById.value
    const ids = selectedRuleIds.value.filter(id => {
      const node = index.get(id)
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

  // 应用会话代理统计（relayStatsUpdate 事件）。
  // 会话代理不经内核进程，统计由后端定时推送而非随 trafficUpdate 到达。
  function applyRelayStats(snap) {
    if (!snap || !snap.relayId) return
    const idx = sessionRelays.value.findIndex(r => r.id === snap.relayId)
    if (idx >= 0) {
      sessionRelays.value[idx] = {
        ...sessionRelays.value[idx],
        activeConns: snap.activeConns,
        totalConns: snap.totalConns,
        sessionCount: snap.sessionCount,
      }
    }
    // 复用流量表，让列表的实时流量/累计列与其他节点类型一致
    traffic.value = {
      ...traffic.value,
      [snap.relayId]: {
        ruleId: snap.relayId,
        upSpeed: snap.upSpeed,
        downSpeed: snap.downSpeed,
        totalUp: snap.bytesUp,
        totalDown: snap.bytesDown,
        todayUp: snap.bytesUp,
        todayDown: snap.bytesDown,
      },
    }
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
    const index = nodeById.value
    const ids = selectedRuleIds.value.filter(id => index.has(id))
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

  // 取消选中单个节点（Set 必须整体替换才有响应性）
  function deselect(id) {
    if (!selectedIds.value.has(id)) return
    const next = new Set(selectedIds.value)
    next.delete(id)
    selectedIds.value = next
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

  async function copySelected() {
    if (selectedIds.value.size === 0) return { count: 0, skipped: 0 }
    const selected = allNodes.value.filter(node => selectedIds.value.has(node.id))
    const ruleIds = selected.filter(node => node._nodeType === 'rule').map(node => node.id)
    const skipped = selected.length - ruleIds.length
    if (ruleIds.length === 0) return { count: 0, skipped }
    const text = await api.exportShareLinks(ruleIds)
    await navigator.clipboard.writeText(text)
    clipboard.value = ruleIds
    return { count: ruleIds.length, skipped }
  }

  async function pasteNodes() {
    const text = (await navigator.clipboard.readText()).trim()
    if (!text) return { successCount: 0, failCount: 0, errors: [] }
    const result = await api.importShareLinks(text)
    if (result?.successCount > 0) await loadRules()
    return result
  }

  return {
    // State
    rules, loadBalancers, chainProxies, sessionRelays,
    selectedIds, statusFilter, searchKeyword,
    groupFilter, sortColumn, sortDirection, loading, clipboard, traffic,
    // Computed
    allNodes, nodeById,
    filteredRules, selectedRuleIds, runningCount, totalCount, ungroupedCount, groupCounts,
    // Actions
    loadRules, addRule, updateRule, updateNodes, deleteRule,
    startRule, stopRule, deleteSelectedRules,
    startSelectedRules, stopSelectedRules, testSelectedSpeed,
    updateRuleInList, toggleSelect, selectAll, deselect, handleRowSelect, setSort,
    copySelected, pasteNodes,
    applyTrafficUpdate, applyRelayStats, applyHealthCheckResult,
    checkSelectedHealth, checkAllHealth, resetTraffic,
  }
})
