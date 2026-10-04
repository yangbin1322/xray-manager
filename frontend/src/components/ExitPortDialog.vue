<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-large">
      <div class="dialog-header">
        <h3>{{ isEditing ? '编辑出口端口' : '添加出口端口' }}</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-body">
        <div class="intro">
          <div>只需填好出口 IP 和本地端口，保存后立即开始监听，不用挑选节点。</div>
          <div>之后任何节点启动、探测到的出口是这个 IP，就自动加入；IP 变了或节点停了自动退出。</div>
          <div>多个节点出口相同时，优先走延迟最低的；它挂了或 IP 变了就切到下一个，端口不变。</div>
          <div>没有任何节点承载该 IP 时拒绝连接，绝不会从别的 IP 出去。</div>
        </div>

        <div class="form-section">
          <h4>基本信息</h4>
          <div class="form-row">
            <div class="form-group">
              <label>出口 IP（IPv4）：</label>
              <input v-model="form.exitIp" type="text" list="exit-ip-options" placeholder="例如：203.0.113.10" />
              <datalist id="exit-ip-options">
                <option v-for="o in ipOptions" :key="o.ip" :value="o.ip">{{ o.count }} 个运行中节点</option>
              </datalist>
            </div>
            <div class="form-group">
              <label>本地监听端口（混合端口，同时支持 HTTP/SOCKS5）：</label>
              <input v-model.number="form.localPort" type="number" placeholder="留空自动分配" min="0" max="65535" />
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>别名（可选）：</label>
              <input v-model="form.alias" type="text" :placeholder="form.exitIp ? `出口-${form.exitIp.trim()}` : '留空按出口 IP 命名'" />
            </div>
            <div class="form-group">
              <label>所属分组：</label>
              <select v-model="form.groupId">
                <option value="">无分组</option>
                <option v-for="g in groupsStore.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
              </select>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>备注（可选）：</label>
              <input v-model="form.remark" type="text" maxlength="200" placeholder="例如：某平台白名单 IP、给爬虫 A 用" />
            </div>
          </div>
          <div v-if="form.exitIp.trim()" class="hint" :class="{ 'hint-warn': matchedNodes.length === 0 }">
            <template v-if="matchedNodes.length">
              当前有 {{ matchedNodes.length }} 个运行中节点出口为该 IP：{{ matchedPreview }}
            </template>
            <template v-else>
              当前没有运行中的节点出口为该 IP。可以先建好，节点启动并探测到该 IP 后自动加入；在此之前连接会被拒绝。
            </template>
          </div>
        </div>

        <div class="form-section">
          <h4>客户端用法</h4>
          <pre class="usage">curl -x "http://127.0.0.1:{{ usagePort }}" https://api.ipify.org
curl -x "socks5h://127.0.0.1:{{ usagePort }}" https://api.ipify.org</pre>
          <ul class="hint hint-list">
            <li>节点的出口 IP 每隔几分钟复查一次，发现变化会立即摘除该节点并断开经它的连接。</li>
            <li>想让漂移的节点直接停用，可再给它开启节点级的「绑定出口 IP」。</li>
          </ul>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn-secondary" @click="close">取消</button>
        <button class="btn-primary" @click="handleSave" :disabled="saving">
          {{ saving ? '保存中...' : (isEditing ? '保存' : '添加') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useGroupsStore } from '../stores/groups.js'
import { useAppStore } from '../stores/app.js'
import * as api from '../api.js'

const props = defineProps({
  visible: Boolean,
  editingExit: Object,
})
const emit = defineEmits(['close'])

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()
const saving = ref(false)

const isEditing = computed(() => !!props.editingExit)

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/

// 运行中节点已探测到的出口 IP，作为输入建议，按节点数降序
const ipOptions = computed(() => {
  const counts = new Map()
  for (const r of rulesStore.rules) {
    if (r.enabled && IPV4.test(r.realIp || '')) counts.set(r.realIp, (counts.get(r.realIp) || 0) + 1)
  }
  return [...counts].map(([ip, count]) => ({ ip, count })).sort((a, b) => b.count - a.count)
})

const matchedNodes = computed(() => {
  const ip = form.value.exitIp.trim()
  if (!ip) return []
  return rulesStore.rules.filter(r => r.enabled && r.realIp === ip)
})
const matchedPreview = computed(() => {
  const names = matchedNodes.value.slice(0, 3).map(r => r.alias)
  return names.join('、') + (matchedNodes.value.length > 3 ? ' 等' : '')
})

const usagePort = computed(() => form.value.localPort || '端口')

const defaultForm = () => ({ alias: '', exitIp: '', localPort: 0, groupId: '', remark: '' })
const form = ref(defaultForm())

watch(() => props.visible, (v) => {
  if (!v) return
  if (props.editingExit) {
    form.value = {
      alias: props.editingExit.alias || '',
      exitIp: props.editingExit.exitIp || '',
      localPort: props.editingExit.localPort || 0,
      groupId: props.editingExit.groupId || '',
      remark: props.editingExit.remark || '',
    }
  } else {
    form.value = defaultForm()
  }
})

async function handleSave() {
  const exitIp = form.value.exitIp.trim()
  if (!IPV4.test(exitIp)) {
    appStore.showToast('请输入合法的 IPv4 出口 IP', 'warning')
    return
  }
  const port = form.value.localPort
  if (port && (port < 1 || port > 65535)) {
    appStore.showToast('请输入有效端口，或留空自动分配', 'warning')
    return
  }

  saving.value = true
  try {
    const payload = {
      alias: form.value.alias.trim(),
      exitIp,
      localPort: port || 0,
      groupId: form.value.groupId,
      remark: form.value.remark.trim(),
    }
    if (isEditing.value) {
      payload.id = props.editingExit.id
      await api.updateExitPort(payload)
      appStore.showToast('出口端口已更新', 'success')
    } else {
      await api.addExitPort(payload)
      appStore.showToast('出口端口已开始监听，出口为该 IP 的节点启动后会自动加入', 'success', 5000)
    }
    await rulesStore.loadRules()
    close()
  } catch (e) {
    appStore.showToast(`操作失败: ${e}`, 'error', 5000)
  } finally {
    saving.value = false
  }
}

function close() { emit('close') }
</script>

<style scoped>
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
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.2);
  max-height: 90vh;
  overflow-y: auto;
}
.dialog-large { width: 640px; max-width: calc(100vw - 40px); }
.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-color);
  position: sticky; top: 0;
  background: var(--bg-primary);
  z-index: 1;
}
.dialog-header h3 { margin: 0; font-size: 16px; }
.dialog-close { border: none; background: none; font-size: 20px; cursor: pointer; color: var(--text-secondary); }
.dialog-body { padding: 20px; }
.dialog-footer {
  padding: 12px 20px;
  border-top: 1px solid var(--border-color);
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  position: sticky; bottom: 0;
  background: var(--bg-primary);
}

.intro {
  font-size: 12px;
  line-height: 1.7;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border-radius: 4px;
  padding: 10px 12px;
  margin-bottom: 16px;
}
.intro div + div { margin-top: 2px; }

.form-section { margin-bottom: 16px; }
.form-section h4 {
  margin: 0 0 10px 0;
  font-size: 14px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border-color);
  padding-bottom: 6px;
}
.form-row { display: flex; gap: 12px; flex-wrap: wrap; margin-bottom: 8px; }
.form-group { flex: 1; min-width: 150px; }
.form-group label {
  display: block;
  margin-bottom: 4px;
  font-size: 12px;
  color: var(--text-secondary);
}
.form-group input,
.form-group select {
  width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}

.hint {
  font-size: 12px;
  color: var(--text-secondary);
  margin-top: 6px;
  line-height: 1.7;
}
.hint-list {
  margin: 6px 0 0;
  padding-left: 18px;
}
.hint-list li { margin-bottom: 2px; }
.hint-list li:last-child { margin-bottom: 0; }
.hint-warn { color: #e67e22; }

.usage {
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: 10px 12px;
  font-size: 12px;
  overflow-x: auto;
  margin: 0;
  color: var(--text-primary);
}

.btn-primary {
  padding: 8px 20px;
  background: var(--primary-color);
  color: #fff;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
.btn-primary:disabled { opacity: 0.6; cursor: not-allowed; }
.btn-secondary {
  padding: 8px 20px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
</style>
