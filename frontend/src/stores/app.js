import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '../api.js'

export const useAppStore = defineStore('app', () => {
  // === 状态 ===
  const logs = ref([])
  const theme = ref(localStorage.getItem('theme') || 'light')
  const autoStart = ref(false)
  const sysProxyEnabled = ref(false)
  const toasts = ref([])
  let toastId = 0
  // 自增序号，Date.now() 在同一毫秒内会重复，导致 v-for key 冲突和 DOM 复用错乱
  let logId = 0

  // === 日志 ===
  function addLog(message) {
    const entry = {
      id: ++logId,
      time: new Date().toLocaleTimeString(),
      message,
    }
    logs.value.push(entry)
    // 限制日志数量
    if (logs.value.length > 1000) {
      logs.value = logs.value.slice(-500)
    }
  }

  function clearLogsList() {
    logs.value = []
    api.clearLogs().catch(() => {})
  }

  // === Toast 通知 ===
  function showToast(message, type = 'info', duration = 3000) {
    const id = ++toastId
    toasts.value.push({ id, message, type })
    setTimeout(() => {
      toasts.value = toasts.value.filter(t => t.id !== id)
    }, duration)
  }

  // === 确认对话框 ===
  // 不能用原生 window.confirm：macOS 的 WKWebView 默认不实现 JS 对话框，
  // confirm() 直接返回 false，导致所有"先确认再执行"的操作静默失效
  // （删除节点/分组/订阅全都点了没反应）。这里用应用内对话框替代。
  const confirmState = ref(null)
  let confirmResolve = null

  function confirmDialog(message, options = {}) {
    // 已有对话框时先拒掉旧的，避免 promise 悬着不回收
    if (confirmResolve) {
      confirmResolve(false)
      confirmResolve = null
    }
    confirmState.value = {
      message: String(message ?? ''),
      title: options.title || '确认操作',
      confirmText: options.confirmText || '确定',
      cancelText: options.cancelText || '取消',
      danger: options.danger !== false, // 确认框基本都用于删除类操作，默认红色
    }
    return new Promise(resolve => { confirmResolve = resolve })
  }

  function resolveConfirm(ok) {
    confirmState.value = null
    if (confirmResolve) {
      confirmResolve(ok)
      confirmResolve = null
    }
  }

  // === 主题 ===
  function toggleTheme() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    localStorage.setItem('theme', theme.value)
    document.documentElement.setAttribute('data-theme', theme.value)
  }

  function initTheme() {
    document.documentElement.setAttribute('data-theme', theme.value)
  }

  // === 自启动 ===
  async function loadAutoStart() {
    try {
      autoStart.value = await api.getAutoStart()
    } catch (e) {
      console.error('加载自启动状态失败:', e)
    }
  }

  async function setAutoStartEnabled(enabled) {
    try {
      await api.setAutoStart(enabled)
      autoStart.value = enabled
      showToast(enabled ? '已启用开机自启' : '已禁用开机自启', 'success')
    } catch (e) {
      showToast(`设置自启动失败: ${e}`, 'error')
    }
  }

  // === 系统代理 ===
  async function loadSysProxyStatus() {
    try {
      sysProxyEnabled.value = await api.getSystemProxyStatus()
    } catch (e) {
      console.error('加载系统代理状态失败:', e)
    }
  }

  async function enableSysProxy(ruleID) {
    try {
      await api.enableSystemProxy(ruleID)
      sysProxyEnabled.value = true
      showToast('已设为系统代理', 'success')
    } catch (e) {
      showToast(`设置系统代理失败: ${e}`, 'error')
    }
  }

  async function disableSysProxy() {
    try {
      await api.disableSystemProxy()
      sysProxyEnabled.value = false
      showToast('已取消系统代理', 'success')
    } catch (e) {
      showToast(`取消系统代理失败: ${e}`, 'error')
    }
  }

  // === 导入导出 ===
  async function doExportConfig(ruleIds = [], includeSubscriptions = false) {
    try {
      const filePath = await api.exportConfig(ruleIds, includeSubscriptions)
      const hint = ruleIds.length > 0 ? `（已选 ${ruleIds.length} 条）` : '（全部）'
      showToast(`配置已导出到: ${filePath}${hint}`, 'success')
      addLog(`[系统] 配置已导出到: ${filePath}${hint}`)
    } catch (e) {
      if (e && e.toString().includes('用户取消')) return
      showToast(`导出失败: ${e}`, 'error')
      addLog(`[错误] 导出配置失败: ${e}`)
    }
  }

  async function doImportConfig() {
    try {
      const result = await api.importConfig()
      let msg = `导入完成：规则 ${result.rulesImported} 条`
      if (result.rulesSkipped > 0) msg += `，跳过重复 ${result.rulesSkipped} 条`
      if (result.groupsImported > 0) msg += `，分组 ${result.groupsImported} 个`
      if (result.subsImported > 0) msg += `，订阅 ${result.subsImported} 个`

      showToast(msg, 'success', 5000)
      addLog(`[系统] ${msg}`)

      if (result.warnings) result.warnings.forEach(w => addLog(`[警告] ${w}`))
      if (result.errors) result.errors.forEach(e => addLog(`[错误] ${e}`))

      return result
    } catch (e) {
      if (e && e.toString().includes('用户取消')) return null
      showToast(`导入失败: ${e}`, 'error')
      addLog(`[错误] 导入配置失败: ${e}`)
      return null
    }
  }

  async function doImportShareLinks(text, groupId = '', newGroupName = '') {
    try {
      const result = await api.importShareLinks(text, groupId, newGroupName)
      let msg = `成功导入 ${result.successCount} 个节点`
      if (result.failCount > 0) msg += `，失败 ${result.failCount} 个`
      showToast(msg, result.failCount > 0 ? 'warning' : 'success')
      addLog(`[导入] ${msg}`)
      if (result.errors) result.errors.forEach(e => addLog(`[导入错误] ${e}`))
      return result
    } catch (e) {
      showToast(`批量导入失败: ${e}`, 'error')
      addLog(`[错误] 批量导入失败: ${e}`)
      return null
    }
  }

  return {
    // State
    logs, theme, autoStart, sysProxyEnabled, toasts, confirmState,
    // Actions
    addLog, clearLogsList, showToast,
    confirmDialog, resolveConfirm,
    toggleTheme, initTheme,
    loadAutoStart, setAutoStartEnabled,
    loadSysProxyStatus, enableSysProxy, disableSysProxy,
    doExportConfig, doImportConfig, doImportShareLinks,
  }
})
