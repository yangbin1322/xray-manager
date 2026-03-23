<template>
  <div class="node-list">
    <div v-if="rulesStore.loading" class="loading-overlay">
      <div class="spinner"></div>
    </div>

    <table class="rules-table">
      <thead>
        <tr>
          <th class="col-check">
            <input type="checkbox" :checked="allSelected" @change="rulesStore.selectAll($event.target.checked)" />
          </th>
          <th class="col-alias">别名</th>
          <th class="col-protocol">协议</th>
          <th class="col-server">服务器地址</th>
          <th class="col-sport">服务器端口</th>
          <th class="col-local">本地代理</th>
          <th class="col-lport">本地端口</th>
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
          <th class="col-ip">真实IP</th>
          <th class="col-status">状态</th>
          <th class="col-actions">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="rulesStore.filteredRules.length === 0">
          <td colspan="12" class="empty-row">暂无节点数据</td>
        </tr>
        <tr
          v-for="rule in rulesStore.filteredRules"
          :key="rule.id"
          :class="{ 'row-running': rule.enabled, 'row-testing': rule.testStatus === 'testing' }"
        >
          <td class="col-check">
            <input
              type="checkbox"
              :checked="rulesStore.selectedIds.has(rule.id)"
              @change="rulesStore.toggleSelect(rule.id)"
            />
          </td>
          <td class="col-alias" :title="rule.alias">{{ rule.alias || '-' }}</td>
          <td class="col-protocol">
            <span :class="['protocol-badge', `protocol-${rule.protocol}`]">{{ rule.protocol }}</span>
          </td>
          <td class="col-server" :title="rule.serverAddr">{{ rule.serverAddr || '-' }}</td>
          <td class="col-sport">{{ rule.serverPort || '-' }}</td>
          <td class="col-local">{{ rule.localType || 'socks' }}</td>
          <td class="col-lport">{{ rule.localPort || '-' }}</td>
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
          <td class="col-ip" :title="rule.realIp">{{ rule.realIp || '-' }}</td>
          <td class="col-status">
            <span :class="['status-dot', statusClass(rule)]"></span>
          </td>
          <td class="col-actions">
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
            <button class="btn-action-sm" @click="$emit('editRule', rule)">编辑</button>
            <button class="btn-action-sm btn-test" @click="handleTest(rule)">测速</button>
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
import { computed, ref } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useAppStore } from '../stores/app.js'
import * as api from '../api.js'

const emit = defineEmits(['editRule'])

const rulesStore = useRulesStore()
const appStore = useAppStore()
const startingIds = ref(new Set())

const allSelected = computed(() => {
  const filtered = rulesStore.filteredRules
  return filtered.length > 0 && filtered.every(r => rulesStore.selectedIds.has(r.id))
})

function latencyClass(latency) {
  if (latency < 100) return 'latency-good'
  if (latency < 300) return 'latency-ok'
  return 'latency-bad'
}

function statusClass(rule) {
  if (rule.enabled) return 'status-running'
  if (rule.testStatus === 'failed') return 'status-failed'
  return 'status-stopped'
}

async function handleStart(rule) {
  startingIds.value.add(rule.id)
  try {
    await rulesStore.startRule(rule.id)
    await rulesStore.loadRules()
  } catch (e) {
    appStore.showToast(`启动失败: ${e}`, 'error')
  } finally {
    startingIds.value.delete(rule.id)
  }
}

async function handleStop(rule) {
  try {
    await rulesStore.stopRule(rule.id)
    await rulesStore.loadRules()
  } catch (e) {
    appStore.showToast(`停止失败: ${e}`, 'error')
  }
}

async function handleTest(rule) {
  try {
    await api.testRuleSpeed(rule.id)
    appStore.showToast(`正在测速: ${rule.alias}`, 'info')
  } catch (e) {
    appStore.showToast(`测速失败: ${e}`, 'error')
  }
}

function handleDelete(rule) {
  if (confirm(`确定要删除规则「${rule.alias}」吗?`)) {
    rulesStore.deleteRule(rule.id).catch(e => {
      appStore.showToast(`删除失败: ${e}`, 'error')
    })
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

.row-running { background: rgba(39, 174, 96, 0.05) !important; }
.row-testing { background: rgba(243, 156, 18, 0.05) !important; }

.col-check { width: 36px; text-align: center; }
.col-alias { max-width: 120px; }
.col-protocol { width: 90px; }
.col-server { max-width: 140px; }
.col-sport { width: 70px; }
.col-local { width: 70px; }
.col-lport { width: 70px; }
.col-latency { width: 80px; }
.col-speed { width: 100px; }
.col-ip { max-width: 100px; }
.col-status { width: 40px; text-align: center; }
.col-actions { width: 200px; }

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
