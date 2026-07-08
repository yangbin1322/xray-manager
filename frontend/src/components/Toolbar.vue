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
      <button class="btn-action" @click="handleBatchEdit" title="批量编辑选中的普通节点">批量编辑</button>
      <button class="btn-action" @click="handleCheckHealth" title="健康检测选中节点，未选中时检测全部">健康检测</button>
      <button class="btn-action" @click="handleResetTraffic" title="清零选中节点流量，未选中时清零全部">流量清零</button>
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

      <!-- 健康检查设置 -->
      <button class="btn-icon" @click="openHealthSettings" title="健康检查设置">⚙</button>

      <!-- 主题切换 -->
      <button class="btn-icon" @click="appStore.toggleTheme()" :title="themeTitle">
        {{ appStore.theme === 'dark' ? '☀' : '☾' }}
      </button>
    </div>

    <!-- 设置对话框 -->
    <div v-if="showHealthSettings" class="dialog-overlay" @click.self="showHealthSettings = false">
      <div class="dialog dialog-settings">
        <div class="dialog-header">
          <h3>设置</h3>
          <button class="dialog-close" @click="showHealthSettings = false">&times;</button>
        </div>

        <!-- 标签页 -->
        <div class="settings-tabs">
          <button
            v-for="tab in settingsTabs" :key="tab.key"
            :class="['settings-tab', { active: activeTab === tab.key }]"
            @click="activeTab = tab.key"
          >{{ tab.label }}</button>
        </div>

        <div class="dialog-body">
          <!-- 健康检查 -->
          <div v-show="activeTab === 'health'">
            <div class="form-group-line">
              <label>
                <input type="checkbox" v-model="healthCfg.enabled" />
                启用后台自动检测（无需启动节点即可检测连通性）
              </label>
            </div>
            <div class="form-group-line">
              <label>检测周期（秒）：</label>
              <input type="number" v-model.number="healthCfg.intervalSec" min="10" />
            </div>
            <div class="form-group-line">
              <label>检测超时（秒）：</label>
              <input type="number" v-model.number="healthCfg.timeoutSec" min="1" />
            </div>
            <div class="form-group-line">
              <label>延迟较高阈值（毫秒）：</label>
              <input type="number" v-model.number="healthCfg.latencyThreshold" min="50" />
            </div>
          </div>

          <!-- 测速 -->
          <div v-show="activeTab === 'speed'">
            <div class="form-group-block">
              <label>下载测速 URL：</label>
              <input type="text" v-model="speedCfg.url" class="full-input" placeholder="留空使用默认" />
            </div>
            <div class="form-group-block">
              <label>请求头（每行一个，格式 <code>名称: 值</code>）：</label>
              <textarea v-model="speedHeadersText" class="headers-textarea" rows="8"
                placeholder="User-Agent: Mozilla/5.0 ..."></textarea>
            </div>
            <div class="settings-hint">
              <button class="btn-link" @click="restoreSpeedDefaults">恢复默认</button>
              <span>部分测速服务器需要特定请求头，否则会返回 500。</span>
            </div>
          </div>
        </div>
        <div class="dialog-footer">
          <button class="btn-action" @click="showHealthSettings = false">取消</button>
          <button class="btn-action btn-primary-solid" @click="saveSettings">保存</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useAppStore } from '../stores/app.js'
import * as api from '../api.js'

const emit = defineEmits(['batchEdit'])

const rulesStore = useRulesStore()
const appStore = useAppStore()

function handleBatchEdit() {
  // 收集选中的普通节点（故障转移/链式代理不支持批量编辑）
  const selected = rulesStore.selectedRuleIds
  if (selected.length === 0) {
    appStore.showToast('请先选择要编辑的节点', 'warning')
    return
  }
  const selectedSet = new Set(selected)
  const nodes = rulesStore.rules.filter(r => selectedSet.has(r.id))
  const skippedCount = selected.length - nodes.length
  if (nodes.length === 0) {
    appStore.showToast('选中的项中没有可编辑的普通节点', 'warning')
    return
  }
  emit('batchEdit', { nodes, skippedCount })
}

const showHealthSettings = ref(false)
const activeTab = ref('health')
const settingsTabs = [
  { key: 'health', label: '健康检查' },
  { key: 'speed', label: '测速' },
]
const healthCfg = ref({ enabled: false, intervalSec: 60, timeoutSec: 5, latencyThreshold: 500 })
const speedCfg = ref({ url: '', headers: {} })
const speedHeadersText = ref('')

// headers 对象 <-> "名称: 值" 多行文本
function headersToText(headers) {
  return Object.entries(headers || {}).map(([k, v]) => `${k}: ${v}`).join('\n')
}
function textToHeaders(text) {
  const headers = {}
  for (const line of (text || '').split('\n')) {
    const s = line.trim()
    if (!s) continue
    const idx = s.indexOf(':')
    if (idx <= 0) continue
    const k = s.slice(0, idx).trim()
    const v = s.slice(idx + 1).trim()
    if (k) headers[k] = v
  }
  return headers
}

async function openHealthSettings() {
  activeTab.value = 'health'
  try {
    const cfg = await api.getHealthCheckConfig()
    if (cfg) healthCfg.value = cfg
  } catch (e) {
    console.error('获取健康检查配置失败:', e)
  }
  try {
    const sc = await api.getSpeedTestConfig()
    if (sc) {
      speedCfg.value = { url: sc.url || '', headers: sc.headers || {} }
      speedHeadersText.value = headersToText(sc.headers)
    }
  } catch (e) {
    console.error('获取测速配置失败:', e)
  }
  showHealthSettings.value = true
}

async function restoreSpeedDefaults() {
  try {
    const def = await api.getDefaultSpeedTestConfig()
    speedCfg.value.url = def.url || ''
    speedHeadersText.value = headersToText(def.headers)
    appStore.showToast('已填入默认测速配置，保存后生效', 'info')
  } catch (e) {
    appStore.showToast(`获取默认配置失败: ${e}`, 'error')
  }
}

async function saveSettings() {
  try {
    await api.setHealthCheckConfig(healthCfg.value)
    await api.setSpeedTestConfig({
      url: speedCfg.value.url.trim(),
      headers: textToHeaders(speedHeadersText.value),
    })
    showHealthSettings.value = false
    appStore.showToast('设置已保存', 'success')
  } catch (e) {
    appStore.showToast(`保存失败: ${e}`, 'error')
  }
}

async function handleCheckHealth() {
  try {
    const checked = await rulesStore.checkSelectedHealth()
    if (!checked) {
      await rulesStore.checkAllHealth()
      appStore.showToast('正在检测全部节点...', 'info')
    } else {
      appStore.showToast('正在检测选中节点...', 'info')
    }
  } catch (e) {
    appStore.showToast(`健康检测失败: ${e}`, 'error')
  }
}

async function handleResetTraffic() {
  const ids = rulesStore.selectedRuleIds.filter(id => rulesStore.rules.some(r => r.id === id))
  const label = ids.length > 0 ? `选中的 ${ids.length} 个节点` : '全部节点'
  if (!confirm(`确定要清零${label}的流量统计吗？`)) return
  try {
    if (ids.length > 0) {
      for (const id of ids) {
        await api.resetRuleTraffic(id)
      }
      await rulesStore.loadRules()
    } else {
      await rulesStore.resetTraffic('')
    }
    appStore.showToast('流量统计已清零', 'success')
  } catch (e) {
    appStore.showToast(`清零失败: ${e}`, 'error')
  }
}

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
  color: var(--text-primary);
}

/* 健康检查设置对话框 */
.dialog-overlay {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.dialog {
  background: var(--bg-primary);
  border-radius: 8px;
  width: 420px;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.2);
}

.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-color);
}

.dialog-settings { width: 560px; }
.dialog-header h3 { margin: 0; font-size: 16px; }
.dialog-close { border: none; background: none; font-size: 20px; cursor: pointer; color: var(--text-secondary); }
.dialog-body { padding: 20px; max-height: 70vh; overflow-y: auto; }

/* 标签页 */
.settings-tabs {
  display: flex;
  gap: 2px;
  padding: 0 20px;
  border-bottom: 1px solid var(--border-color);
}
.settings-tab {
  padding: 10px 16px;
  border: none;
  background: none;
  cursor: pointer;
  font-size: 13px;
  color: var(--text-secondary);
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
}
.settings-tab:hover { color: var(--text-primary); }
.settings-tab.active {
  color: var(--primary-color);
  border-bottom-color: var(--primary-color);
  font-weight: 500;
}

.form-group-block { margin-bottom: 12px; }
.form-group-block label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  color: var(--text-primary);
}
.form-group-block code {
  background: var(--bg-secondary);
  padding: 1px 4px;
  border-radius: 3px;
  font-size: 12px;
}
.full-input {
  width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}
.headers-textarea {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 12px;
  font-family: monospace;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
  resize: vertical;
}
.settings-hint {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  color: var(--text-secondary);
}
.btn-link {
  border: none;
  background: none;
  color: var(--primary-color);
  cursor: pointer;
  font-size: 12px;
  padding: 0;
  text-decoration: underline;
}
.dialog-footer {
  padding: 12px 20px;
  border-top: 1px solid var(--border-color);
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.form-group-line {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
  font-size: 13px;
}

.form-group-line label {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 150px;
  color: var(--text-primary);
}

.form-group-line input[type="number"] {
  flex: 1;
  padding: 6px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.btn-primary-solid {
  background: var(--primary-color) !important;
  color: #fff !important;
  border-color: var(--primary-color) !important;
}
</style>
