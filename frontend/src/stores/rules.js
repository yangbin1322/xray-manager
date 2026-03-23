import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api.js'

export const useRulesStore = defineStore('rules', () => {
  // === 状态 ===
  const rules = ref([])
  const loadBalancers = ref([])
  const chainProxies = ref([])
  const selectedIds = ref(new Set())
  const statusFilter = ref('all') // all | running | stopped
  const searchKeyword = ref('')
  const groupFilter = ref(null) // null = 所有分组
  const sortColumn = ref(null)
  const sortDirection = ref('asc')
  const loading = ref(false)
  const clipboard = ref([]) // 复制的节点数据

  // === 合并所有节点（规则 + 负载均衡 + 链式代理） ===
  const allNodes = computed(() => {
    const ruleNodes = rules.value.map(r => ({ ...r, _nodeType: 'rule' }))
    const lbNodes = loadBalancers.value.map(lb => ({ ...lb, _nodeType: 'lb', protocol: 'loadbalance' }))
    const chainNodes = chainProxies.value.map(c => ({ ...c, _nodeType: 'chain', protocol: 'chain' }))
    return [...ruleNodes, ...lbNodes, ...chainNodes]
  })

  // === 计算属性 ===
  const filteredRules = computed(() => {
    let result = allNodes.value

    // 分组过滤
    if (groupFilter.value !== null) {
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
    for (const id of selectedRuleIds.value) {
      const node = allNodes.value.find(n => n.id === id)
      if (!node || node.enabled) continue
      try {
        if (node._nodeType === 'lb') {
          await api.startLoadBalancer(id)
        } else if (node._nodeType === 'chain') {
          await api.startChainProxy(id)
        } else {
          await api.startRule(id)
        }
      } catch (e) { console.error(e) }
    }
    await loadRules()
  }

  async function stopSelectedRules() {
    for (const id of selectedRuleIds.value) {
      const node = allNodes.value.find(n => n.id === id)
      if (!node || !node.enabled) continue
      try {
        if (node._nodeType === 'lb') {
          await api.stopLoadBalancer(id)
        } else if (node._nodeType === 'chain') {
          await api.stopChainProxy(id)
        } else {
          await api.stopRule(id)
        }
      } catch (e) { console.error(e) }
    }
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

  function toggleSelect(id) {
    if (selectedIds.value.has(id)) {
      selectedIds.value.delete(id)
    } else {
      selectedIds.value.add(id)
    }
  }

  function selectAll(checked) {
    if (checked) {
      filteredRules.value.forEach(r => selectedIds.value.add(r.id))
    } else {
      selectedIds.value.clear()
    }
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
    groupFilter, sortColumn, sortDirection, loading, clipboard,
    // Computed
    filteredRules, selectedRuleIds, runningCount, totalCount,
    // Actions
    loadRules, addRule, updateRule, deleteRule,
    startRule, stopRule, deleteSelectedRules,
    startSelectedRules, stopSelectedRules, testSelectedSpeed,
    updateRuleInList, toggleSelect, selectAll, setSort,
    copySelected, pasteNodes,
  }
})
