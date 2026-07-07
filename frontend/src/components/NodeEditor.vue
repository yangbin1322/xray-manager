<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-large">
      <div class="dialog-header">
        <h3>{{ isEditing ? '编辑规则' : '添加规则' }}</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-body">
        <!-- 基本信息 -->
        <div class="form-section">
          <h4>基本信息</h4>
          <div class="form-row">
            <div class="form-group">
              <label>别名：</label>
              <input v-model="form.alias" type="text" placeholder="例如：香港节点" />
            </div>
            <div class="form-group">
              <label>所属分组：</label>
              <select v-model="form.groupId">
                <option value="">无分组</option>
                <option v-for="g in groupsStore.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
              </select>
            </div>
            <div class="form-group">
              <label>本地代理端口（混合端口，同时支持 HTTP/SOCKS5）：</label>
              <div class="port-row">
                <input v-model.number="form.localPort" type="number" placeholder="1080" min="1" max="65535" />
                <button class="btn-small" @click="handleRecommendPort">推荐</button>
              </div>
            </div>
          </div>
        </div>

        <!-- 服务器信息 -->
        <div class="form-section">
          <h4>服务器信息</h4>
          <div class="form-row">
            <div class="form-group">
              <label>协议类型：</label>
              <select v-model="form.protocol">
                <option value="shadowsocks">Shadowsocks</option>
                <option value="vmess">VMess</option>
                <option value="vless">VLESS</option>
                <option value="trojan">Trojan</option>
                <option value="hysteria2">Hysteria2</option>
                <option value="tuic">TUIC v5</option>
                <option value="http">HTTP</option>
                <option value="socks">SOCKS</option>
              </select>
            </div>
            <div class="form-group">
              <label>服务器地址：</label>
              <input v-model="form.serverAddr" type="text" placeholder="例如：example.com" />
            </div>
            <div class="form-group">
              <label>服务器端口：</label>
              <input v-model.number="form.serverPort" type="number" placeholder="443" min="1" max="65535" />
            </div>
          </div>
        </div>

        <!-- Shadowsocks 配置 -->
        <div v-if="form.protocol === 'shadowsocks'" class="form-section">
          <h4>Shadowsocks 配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>加密方法：</label>
              <select v-model="form.settings.ssMethod">
                <option value="aes-256-gcm">aes-256-gcm</option>
                <option value="aes-128-gcm">aes-128-gcm</option>
                <option value="chacha20-poly1305">chacha20-poly1305</option>
                <option value="chacha20-ietf-poly1305">chacha20-ietf-poly1305</option>
              </select>
            </div>
            <div class="form-group">
              <label>密码：</label>
              <input v-model="form.settings.ssPassword" type="text" placeholder="密码" />
            </div>
          </div>
        </div>

        <!-- VMess 配置 -->
        <div v-if="form.protocol === 'vmess'" class="form-section">
          <h4>VMess 配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>用户ID (UUID)：</label>
              <input v-model="form.settings.vmessUserId" type="text" placeholder="UUID" />
            </div>
            <div class="form-group">
              <label>额外ID：</label>
              <input v-model.number="form.settings.vmessAlterId" type="number" placeholder="0" />
            </div>
            <div class="form-group">
              <label>加密方式：</label>
              <select v-model="form.settings.vmessSecurity">
                <option value="auto">auto</option>
                <option value="aes-128-gcm">aes-128-gcm</option>
                <option value="chacha20-poly1305">chacha20-poly1305</option>
                <option value="none">none</option>
              </select>
            </div>
          </div>
        </div>

        <!-- VLESS 配置 -->
        <div v-if="form.protocol === 'vless'" class="form-section">
          <h4>VLESS 配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>用户ID (UUID)：</label>
              <input v-model="form.settings.vlessUserId" type="text" placeholder="UUID" />
            </div>
            <div class="form-group">
              <label>流控模式：</label>
              <input v-model="form.settings.vlessFlow" type="text" placeholder="如 xtls-rprx-vision" />
            </div>
            <div class="form-group">
              <label>加密方式：</label>
              <input v-model="form.settings.vlessEncryption" type="text" placeholder="none" />
            </div>
          </div>
        </div>

        <!-- Trojan 配置 -->
        <div v-if="form.protocol === 'trojan'" class="form-section">
          <h4>Trojan 配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>密码：</label>
              <input v-model="form.settings.trojanPassword" type="text" placeholder="Trojan 密码" />
            </div>
          </div>
        </div>

        <!-- Hysteria2 配置 -->
        <div v-if="form.protocol === 'hysteria2'" class="form-section">
          <h4>Hysteria2 配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>认证密码：</label>
              <input v-model="form.settings.hy2Password" type="text" placeholder="密码" />
            </div>
            <div class="form-group">
              <label>混淆类型：</label>
              <select v-model="form.settings.hy2Obfs">
                <option value="">无</option>
                <option value="salamander">salamander</option>
              </select>
            </div>
            <div class="form-group" v-if="form.settings.hy2Obfs">
              <label>混淆密码：</label>
              <input v-model="form.settings.hy2ObfsPassword" type="text" placeholder="混淆密码" />
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>上行带宽 (Mbps，0=自动)：</label>
              <input v-model.number="form.settings.hy2UpMbps" type="number" min="0" placeholder="0" />
            </div>
            <div class="form-group">
              <label>下行带宽 (Mbps，0=自动)：</label>
              <input v-model.number="form.settings.hy2DownMbps" type="number" min="0" placeholder="0" />
            </div>
          </div>
        </div>

        <!-- TUIC 配置 -->
        <div v-if="form.protocol === 'tuic'" class="form-section">
          <h4>TUIC v5 配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>用户ID (UUID)：</label>
              <input v-model="form.settings.tuicUserId" type="text" placeholder="UUID" />
            </div>
            <div class="form-group">
              <label>密码：</label>
              <input v-model="form.settings.tuicPassword" type="text" placeholder="密码" />
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>拥塞控制：</label>
              <select v-model="form.settings.tuicCongestion">
                <option value="bbr">bbr</option>
                <option value="cubic">cubic</option>
                <option value="new_reno">new_reno</option>
              </select>
            </div>
            <div class="form-group">
              <label>UDP 中继模式：</label>
              <select v-model="form.settings.tuicUdpRelayMode">
                <option value="native">native</option>
                <option value="quic">quic</option>
              </select>
            </div>
          </div>
        </div>

        <!-- Hysteria2 / TUIC 的 TLS 配置（强制启用 TLS） -->
        <div v-if="form.protocol === 'hysteria2' || form.protocol === 'tuic'" class="form-section">
          <h4>TLS 配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>SNI (服务器名)：</label>
              <input v-model="tlsServerName" type="text" placeholder="SNI" />
            </div>
            <div class="form-group">
              <label>ALPN：</label>
              <input v-model="tlsAlpn" type="text" placeholder="h3" />
            </div>
            <div class="form-group form-group-checkbox">
              <label>
                <input type="checkbox" v-model="tlsAllowInsecure" />
                允许不安全连接（跳过证书验证）
              </label>
            </div>
          </div>
        </div>

        <!-- HTTP 代理配置 -->
        <div v-if="form.protocol === 'http'" class="form-section">
          <h4>HTTP 代理配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>用户名（可选）：</label>
              <input v-model="form.settings.httpUsername" type="text" placeholder="用户名" />
            </div>
            <div class="form-group">
              <label>密码（可选）：</label>
              <input v-model="form.settings.httpPassword" type="text" placeholder="密码" />
            </div>
          </div>
        </div>

        <!-- SOCKS 代理配置 -->
        <div v-if="form.protocol === 'socks'" class="form-section">
          <h4>SOCKS 代理配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>SOCKS 版本：</label>
              <select v-model="form.settings.socksVersion">
                <option value="socks5">SOCKS5</option>
                <option value="socks4">SOCKS4</option>
              </select>
            </div>
            <div class="form-group">
              <label>用户名（可选）：</label>
              <input v-model="form.settings.socksUsername" type="text" placeholder="用户名" />
            </div>
            <div class="form-group">
              <label>密码（可选）：</label>
              <input v-model="form.settings.socksPassword" type="text" placeholder="密码" />
            </div>
          </div>
        </div>

        <!-- 传输层配置 -->
        <div v-if="['shadowsocks','vmess','vless','trojan'].includes(form.protocol)" class="form-section">
          <h4>传输层配置</h4>
          <div class="form-row">
            <div class="form-group">
              <label>传输协议：</label>
              <select v-model="form.settings.network">
                <option value="tcp">TCP</option>
                <option value="ws">WebSocket</option>
                <option value="grpc">gRPC</option>
                <option value="h2">HTTP/2</option>
              </select>
            </div>
            <div class="form-group">
              <label>传输层安全：</label>
              <select v-model="form.settings.security">
                <option value="none">None</option>
                <option value="tls">TLS</option>
                <option value="reality">Reality</option>
              </select>
            </div>
          </div>

          <!-- TLS 配置 -->
          <div v-if="form.settings.security === 'tls' || form.settings.security === 'reality'" class="form-row">
            <div class="form-group">
              <label>SNI (服务器名)：</label>
              <input v-model="tlsServerName" type="text" placeholder="SNI" />
            </div>
            <div class="form-group">
              <label>ALPN：</label>
              <input v-model="tlsAlpn" type="text" placeholder="h2,http/1.1" />
            </div>
            <div class="form-group form-group-checkbox">
              <label>
                <input type="checkbox" v-model="tlsAllowInsecure" />
                允许不安全连接（跳过证书验证）
              </label>
            </div>
          </div>

          <!-- WebSocket 配置 -->
          <div v-if="form.settings.network === 'ws'" class="form-row">
            <div class="form-group">
              <label>WS 路径：</label>
              <input v-model="wsPath" type="text" placeholder="/" />
            </div>
            <div class="form-group">
              <label>WS Host：</label>
              <input v-model="wsHost" type="text" placeholder="Host 头" />
            </div>
          </div>

          <!-- gRPC 配置 -->
          <div v-if="form.settings.network === 'grpc'" class="form-row">
            <div class="form-group">
              <label>gRPC 服务名：</label>
              <input v-model="grpcServiceName" type="text" placeholder="服务名" />
            </div>
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn-secondary" @click="close">取消</button>
        <button class="btn-primary" @click="handleSave">{{ isEditing ? '保存' : '添加' }}</button>
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
  editingRule: Object,
})

const emit = defineEmits(['close'])

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()

const isEditing = computed(() => !!props.editingRule)

function defaultForm() {
  return {
    alias: '',
    localType: 'mixed',
    localPort: 0,
    protocol: 'shadowsocks',
    serverAddr: '',
    serverPort: 443,
    groupId: '',
    settings: {
      ssMethod: 'aes-256-gcm',
      ssPassword: '',
      vmessUserId: '',
      vmessAlterId: 0,
      vmessSecurity: 'auto',
      vlessUserId: '',
      vlessFlow: '',
      vlessEncryption: 'none',
      trojanPassword: '',
      httpUsername: '',
      httpPassword: '',
      socksUsername: '',
      socksPassword: '',
      socksVersion: 'socks5',
      hy2Password: '',
      hy2Obfs: '',
      hy2ObfsPassword: '',
      hy2UpMbps: 0,
      hy2DownMbps: 0,
      tuicUserId: '',
      tuicPassword: '',
      tuicCongestion: 'bbr',
      tuicUdpRelayMode: 'native',
      network: 'tcp',
      security: 'none',
      tls: null,
      ws: null,
      grpc: null,
      h2: null,
    },
  }
}

const form = ref(defaultForm())

// 传输层计算属性
const tlsServerName = computed({
  get: () => form.value.settings.tls?.serverName || '',
  set: (v) => {
    if (!form.value.settings.tls) form.value.settings.tls = {}
    form.value.settings.tls.serverName = v
  },
})

const tlsAlpn = computed({
  get: () => (form.value.settings.tls?.alpn || []).join(','),
  set: (v) => {
    if (!form.value.settings.tls) form.value.settings.tls = {}
    form.value.settings.tls.alpn = v ? v.split(',').map(s => s.trim()) : []
  },
})

const tlsAllowInsecure = computed({
  get: () => form.value.settings.tls?.allowInsecure || false,
  set: (v) => {
    if (!form.value.settings.tls) form.value.settings.tls = {}
    form.value.settings.tls.allowInsecure = v
  },
})

const wsPath = computed({
  get: () => form.value.settings.ws?.path || '',
  set: (v) => {
    if (!form.value.settings.ws) form.value.settings.ws = {}
    form.value.settings.ws.path = v
  },
})

const wsHost = computed({
  get: () => form.value.settings.ws?.headers?.Host || '',
  set: (v) => {
    if (!form.value.settings.ws) form.value.settings.ws = {}
    if (!form.value.settings.ws.headers) form.value.settings.ws.headers = {}
    form.value.settings.ws.headers.Host = v
  },
})

const grpcServiceName = computed({
  get: () => form.value.settings.grpc?.serviceName || '',
  set: (v) => {
    if (!form.value.settings.grpc) form.value.settings.grpc = {}
    form.value.settings.grpc.serviceName = v
  },
})

// 当 editingRule 变化时填充表单
watch(() => props.editingRule, (rule) => {
  if (rule) {
    form.value = JSON.parse(JSON.stringify(rule))
    if (!form.value.settings) form.value.settings = defaultForm().settings
  } else {
    form.value = defaultForm()
  }
}, { immediate: true })

watch(() => props.visible, (v) => {
  if (v && !props.editingRule) {
    form.value = defaultForm()
  }
})

async function handleRecommendPort() {
  try {
    form.value.localPort = await api.recommendPort()
  } catch (e) {
    appStore.showToast('获取推荐端口失败', 'error')
  }
}

async function handleSave() {
  // 基本校验
  if (!form.value.serverAddr) {
    appStore.showToast('请输入服务器地址', 'warning')
    return
  }
  if (!form.value.serverPort || form.value.serverPort <= 0) {
    appStore.showToast('请输入有效的服务器端口', 'warning')
    return
  }
  if (!form.value.localPort || form.value.localPort <= 0) {
    appStore.showToast('请输入有效的本地端口', 'warning')
    return
  }

  // 构建传输层配置
  buildTransportSettings()

  try {
    if (isEditing.value) {
      await rulesStore.updateRule(form.value.id, form.value)
      appStore.showToast('规则已更新', 'success')
    } else {
      await rulesStore.addRule(form.value)
      appStore.showToast('规则已添加', 'success')
    }
    close()
  } catch (e) {
    appStore.showToast(`保存失败: ${e}`, 'error')
  }
}

function buildTransportSettings() {
  const s = form.value.settings

  // 本地代理统一为混合端口
  form.value.localType = 'mixed'

  // Hysteria2 / TUIC 强制 TLS
  if (form.value.protocol === 'hysteria2' || form.value.protocol === 'tuic') {
    s.security = 'tls'
    s.network = ''
  }

  // TLS
  if (s.security === 'tls' || s.security === 'reality') {
    if (!s.tls) s.tls = {}
  } else {
    s.tls = null
  }

  // WebSocket
  if (s.network === 'ws') {
    if (!s.ws) s.ws = {}
  } else {
    s.ws = null
  }

  // gRPC
  if (s.network === 'grpc') {
    if (!s.grpc) s.grpc = {}
  } else {
    s.grpc = null
  }

  // H2
  if (s.network === 'h2') {
    if (!s.h2) s.h2 = {}
  } else {
    s.h2 = null
  }
}

function close() {
  emit('close')
}
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
  position: sticky;
  top: 0;
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
  position: sticky;
  bottom: 0;
  background: var(--bg-primary);
}

.form-section { margin-bottom: 16px; }
.form-section h4 {
  margin: 0 0 10px 0;
  font-size: 14px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border-color);
  padding-bottom: 6px;
}

.form-row {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.form-group {
  flex: 1;
  min-width: 150px;
}

.form-group-checkbox {
  display: flex;
  align-items: flex-end;
  min-width: 150px;
}

.form-group-checkbox label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  cursor: pointer;
  white-space: nowrap;
}

.form-group-checkbox input[type="checkbox"] {
  width: auto;
  margin: 0;
}

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

.port-row {
  display: flex;
  gap: 6px;
}

.port-row input { flex: 1; }

.btn-primary {
  padding: 8px 20px;
  background: var(--primary-color);
  color: #fff;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}

.btn-secondary {
  padding: 8px 20px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}

.btn-small {
  padding: 7px 12px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
  white-space: nowrap;
}
</style>
