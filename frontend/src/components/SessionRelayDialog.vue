<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-large">
      <div class="dialog-header">
        <h3>{{ isEditing ? '编辑动态会话代理' : '添加动态会话代理' }}</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-body">
        <div class="intro">
          <div>单个端口按客户端用户名动态切换住宅代理出口 IP。</div>
          <div>客户端在代理用户名里传入会话标识，中继按模板拼成上游真实用户名。</div>
          <div>换一个用户名就是换一个出口 IP，无需重启。</div>
          <div>HTTP 与 SOCKS5 客户端均可接入同一个端口。</div>
        </div>

        <div class="form-section">
          <h4>基本信息</h4>
          <div class="form-row">
            <div class="form-group">
              <label>别名：</label>
              <input v-model="form.alias" type="text" placeholder="例如：住宅代理-澳洲" />
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
              <label>本地监听端口（混合端口，同时支持 HTTP/SOCKS5）：</label>
              <input v-model.number="form.localPort" type="number" placeholder="留空自动分配" min="0" max="65535" />
            </div>
            <div class="form-group">
              <label>客户端密码（可选，留空不校验）：</label>
              <input v-model="form.localPassword" type="text" placeholder="防止本机其他程序滥用" />
            </div>
          </div>
        </div>

        <div class="form-section">
          <h4>上游住宅代理</h4>
          <div class="form-row">
            <div class="form-group">
              <label>网关地址（host:port）：</label>
              <input v-model="form.upstreamAddr" type="text" placeholder="gw.dataimpulse.com:823" />
            </div>
            <div class="form-group">
              <label>上游密码：</label>
              <input v-model="form.upstreamPassword" type="text" placeholder="服务商提供的密码" />
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>
                用户名模板（含 <code>{session}</code> 占位符，留空则原样透传）：
              </label>
              <input v-model="form.usernameTemplate" type="text" placeholder="login__cr.au;sessid.{session}" />
            </div>
          </div>
          <div v-if="preview" class="preview">
            客户端用 <code>{{ previewSession }}</code> 作用户名 → 上游收到 <code>{{ preview }}</code>
          </div>
        </div>

        <div class="form-section">
          <h4>前置加速（可选）</h4>
          <div class="form-row">
            <div class="form-group">
              <label>经由节点连接上游（直连很慢或不通时使用）：</label>
              <select v-model="form.preProxyNodeId">
                <option value="">直连上游</option>
                <option value="__global__">
                  跟随全局前置代理{{ globalPreProxyAlias ? `（当前：${globalPreProxyAlias}）` : '（当前未设置 = 直连）' }}
                </option>
                <optgroup v-if="ruleOptions.length" label="普通节点">
                  <option v-for="n in ruleOptions" :key="n.id" :value="n.id">
                    {{ n.alias }}{{ n.enabled ? '' : '（未启动）' }}
                  </option>
                </optgroup>
                <optgroup v-if="chainOptions.length" label="链式代理">
                  <option v-for="n in chainOptions" :key="n.id" :value="n.id">
                    {{ n.alias }}{{ n.enabled ? '' : '（未启动）' }}
                  </option>
                </optgroup>
                <optgroup v-if="lbOptions.length" label="故障转移">
                  <option v-for="n in lbOptions" :key="n.id" :value="n.id">
                    {{ n.alias }}{{ n.enabled ? '' : '（未启动）' }}
                  </option>
                </optgroup>
              </select>
            </div>
          </div>
          <ul class="hint hint-list">
            <li>选「跟随全局前置代理」后随设置里的全局前置自动变化，无需两处分别维护。</li>
            <li>全局未设置时等同于直连上游。</li>
            <li>若单独指定的节点本身又经全局前置出站，链路会多一跳（全局前置 → 该节点 → 上游网关）。</li>
          </ul>
          <div v-if="form.preProxyNodeId" class="hint hint-warn">
            启动本代理前需先启动作为前置的那个节点——中继只做转发，不会替你拉起节点进程。
            <template v-if="form.preProxyNodeId === '__global__' && !globalPreProxyAlias">
              当前全局前置未设置，本代理将直连上游。
            </template>
          </div>
        </div>

        <div class="form-section">
          <h4>客户端用法</h4>
          <pre class="usage">curl -x "http://{{ usageUser }}:{{ usagePass }}@127.0.0.1:{{ usagePort }}" https://api.ipify.org
curl -x "socks5h://{{ usageUser }}:{{ usagePass }}@127.0.0.1:{{ usagePort }}" https://api.ipify.org</pre>
          <ul class="hint hint-list">
            <li>把用户名换成别的会话标识，就会拿到另一个出口 IP。</li>
            <li>SOCKS5 客户端必须启用用户名认证，否则无法携带会话标识。</li>
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
  editingRelay: Object,
})
const emit = defineEmits(['close'])

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()
const saving = ref(false)

const isEditing = computed(() => !!props.editingRelay)

// 前置加速可选普通节点/链式/故障转移；会话代理自身不能作为前置
const ruleOptions = computed(() => rulesStore.rules)
const chainOptions = computed(() => rulesStore.chainProxies)
const lbOptions = computed(() => rulesStore.loadBalancers)

// 全局前置代理的当前值，用于在「跟随全局」选项里回显到底会走哪个节点
const globalPreProxyAlias = ref('')
async function loadGlobalPreProxy() {
  try {
    const cfg = await api.getPreProxy()
    globalPreProxyAlias.value = cfg?.nodeId ? (cfg.alias || '节点已失效') : ''
  } catch {
    globalPreProxyAlias.value = ''
  }
}

const previewSession = 'au-123'
const preview = computed(() => {
  const tpl = form.value.usernameTemplate.trim()
  if (!tpl) return previewSession
  if (!tpl.includes('{session}')) return ''
  return tpl.replaceAll('{session}', previewSession)
})

const usageUser = computed(() => form.value.usernameTemplate.trim() ? previewSession : '完整上游用户名')
const usagePass = computed(() => form.value.localPassword.trim() || 'x')
const usagePort = computed(() => form.value.localPort || '端口')

const defaultForm = () => ({
  alias: '',
  localPort: 0,
  upstreamAddr: '',
  usernameTemplate: '',
  upstreamPassword: '',
  localPassword: '',
  preProxyNodeId: '',
  groupId: '',
})
const form = ref(defaultForm())

watch(() => props.visible, (v) => {
  if (!v) return
  loadGlobalPreProxy()
  if (props.editingRelay) {
    form.value = {
      alias: props.editingRelay.alias || '',
      localPort: props.editingRelay.localPort || 0,
      upstreamAddr: props.editingRelay.upstreamAddr || '',
      usernameTemplate: props.editingRelay.usernameTemplate || '',
      upstreamPassword: props.editingRelay.upstreamPassword || '',
      localPassword: props.editingRelay.localPassword || '',
      preProxyNodeId: props.editingRelay.preProxyNodeId || '',
      groupId: props.editingRelay.groupId || '',
    }
  } else {
    form.value = defaultForm()
  }
})

async function handleSave() {
  const alias = form.value.alias.trim()
  const upstreamAddr = form.value.upstreamAddr.trim()
  const usernameTemplate = form.value.usernameTemplate.trim()

  if (!alias) { appStore.showToast('请输入别名', 'warning'); return }
  if (!upstreamAddr) { appStore.showToast('请输入上游网关地址', 'warning'); return }
  if (!/^[^\s:]+:\d{1,5}$/.test(upstreamAddr)) {
    appStore.showToast('上游网关地址需为 host:port 格式，例如 gw.dataimpulse.com:823', 'warning')
    return
  }
  if (usernameTemplate && !usernameTemplate.includes('{session}')) {
    appStore.showToast('用户名模板必须包含 {session} 占位符', 'warning')
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
      alias,
      localPort: port || 0,
      upstreamAddr,
      usernameTemplate,
      upstreamPassword: form.value.upstreamPassword,
      localPassword: form.value.localPassword,
      preProxyNodeId: form.value.preProxyNodeId,
      groupId: form.value.groupId,
    }

    if (isEditing.value) {
      payload.id = props.editingRelay.id
      await api.updateSessionRelay(payload)
      appStore.showToast('动态会话代理已更新，重新启动后生效', 'success')
    } else {
      await api.addSessionRelay(payload)
      appStore.showToast('动态会话代理已添加', 'success')
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
.dialog-large { width: 700px; }
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

code {
  background: var(--bg-hover);
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 12px;
}

.preview {
  font-size: 12px;
  color: var(--text-secondary);
  padding: 6px 0 0;
}

.hint {
  font-size: 12px;
  color: var(--text-secondary);
  margin-top: 6px;
  line-height: 1.7;
}

/* 每条要点单独一行，避免长句在窄对话框里随机折行难以阅读 */
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
