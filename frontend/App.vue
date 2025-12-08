<template>
  <div class="app-container">
    <el-container>
      <!-- 头部标题 -->
      <el-header height="60px" class="app-header">
        <h1><el-icon><Connection /></el-icon> Xray 管理器</h1>
      </el-header>

      <el-main class="app-main">
        <!-- 规则表格区域 -->
        <el-card class="table-card" shadow="never">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><List /></el-icon> 代理规则</span>
              <div class="header-actions">
                <el-button type="primary" :icon="Plus" @click="openAddDialog">添加规则</el-button>
                <el-button :icon="Upload" @click="importConfig">导入规则</el-button>
                <el-button :icon="Download" @click="exportConfig">导出规则</el-button>
              </div>
            </div>
          </template>

          <el-table
            :data="rules"
            style="width: 100%"
            :height="300"
            @selection-change="handleSelectionChange"
          >
            <el-table-column type="selection" width="55" />
            <el-table-column prop="alias" label="别名" width="150" show-overflow-tooltip />
            <el-table-column prop="protocol" label="协议" width="120" />
            <el-table-column prop="serverAddr" label="服务器地址" min-width="180" show-overflow-tooltip />
            <el-table-column prop="serverPort" label="服务器端口" width="120" />
            <el-table-column prop="localType" label="本地代理类型" width="130" />
            <el-table-column prop="localPort" label="本地端口" width="100" />
            <el-table-column prop="realIp" label="真实IP" width="140" show-overflow-tooltip />
            <el-table-column label="状态" width="100">
              <template #default="{ row }">
                <el-switch
                  v-model="row.enabled"
                  @change="handleEnableChange(row)"
                />
              </template>
            </el-table-column>
            <el-table-column label="操作" width="180" fixed="right">
              <template #default="{ row }">
                <el-button type="primary" size="small" :icon="Edit" @click="editRule(row)">编辑</el-button>
                <el-button type="danger" size="small" :icon="Delete" @click="deleteRule(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>

          <!-- 批量操作按钮 -->
          <div class="batch-actions">
            <el-button type="success" :icon="VideoPlay" @click="startSelectedRules" :disabled="selectedRules.length === 0">
              启动选中 ({{ selectedRules.length }})
            </el-button>
            <el-button type="warning" :icon="VideoPause" @click="stopSelectedRules" :disabled="selectedRules.length === 0">
              停止选中 ({{ selectedRules.length }})
            </el-button>
            <el-button type="danger" :icon="Delete" @click="deleteSelectedRules" :disabled="selectedRules.length === 0">
              删除选中 ({{ selectedRules.length }})
            </el-button>
            <div style="flex: 1;"></div>
            <el-checkbox v-model="autoStart" @change="handleAutoStartChange" label="开机自启" size="large" />
          </div>
        </el-card>

        <!-- 日志区域 -->
        <el-card class="log-card" shadow="never">
          <template #header>
            <div class="card-header">
              <span class="card-title"><el-icon><Document /></el-icon> 日志输出</span>
              <el-button size="small" :icon="Delete" @click="clearLog">清空日志</el-button>
            </div>
          </template>
          <el-input
            v-model="logContent"
            type="textarea"
            :rows="8"
            readonly
            class="log-textarea"
          />
        </el-card>
      </el-main>
    </el-container>

    <!-- 添加/编辑规则对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="800px"
      :close-on-click-modal="false"
    >
      <el-form :model="formData" label-width="140px">
        <!-- 基本信息 -->
        <el-divider content-position="left">基本信息</el-divider>
        <el-row :gutter="20">
          <el-col :span="8">
            <el-form-item label="别名">
              <el-input v-model="formData.alias" placeholder="例如：香港节点" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="本地代理类型">
              <el-select v-model="formData.localType" style="width: 100%">
                <el-option label="SOCKS" value="socks" />
                <el-option label="HTTP" value="http" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="本地代理端口">
              <el-input-number v-model="formData.localPort" :min="1" :max="65535" style="width: 100%" />
            </el-form-item>
          </el-col>
        </el-row>

        <!-- 服务器信息 -->
        <el-divider content-position="left">服务器信息</el-divider>
        <el-row :gutter="20">
          <el-col :span="8">
            <el-form-item label="协议类型">
              <el-select v-model="formData.protocol" @change="onProtocolChange" style="width: 100%">
                <el-option label="Shadowsocks" value="shadowsocks" />
                <el-option label="VMess" value="vmess" />
                <el-option label="VLESS" value="vless" />
                <el-option label="Trojan" value="trojan" />
                <el-option label="HTTP" value="http" />
                <el-option label="SOCKS" value="socks" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="服务器地址">
              <el-input v-model="formData.serverAddr" placeholder="例如：example.com" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="服务器端口">
              <el-input-number v-model="formData.serverPort" :min="1" :max="65535" style="width: 100%" />
            </el-form-item>
          </el-col>
        </el-row>

        <!-- Shadowsocks 配置 -->
        <template v-if="formData.protocol === 'shadowsocks'">
          <el-divider content-position="left">Shadowsocks 配置</el-divider>
          <el-row :gutter="20">
            <el-col :span="12">
              <el-form-item label="加密方法">
                <el-select v-model="formData.settings.ssMethod" style="width: 100%">
                  <el-option label="aes-256-gcm" value="aes-256-gcm" />
                  <el-option label="aes-128-gcm" value="aes-128-gcm" />
                  <el-option label="chacha20-poly1305" value="chacha20-poly1305" />
                  <el-option label="chacha20-ietf-poly1305" value="chacha20-ietf-poly1305" />
                </el-select>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="密码">
                <el-input v-model="formData.settings.ssPassword" placeholder="密码" show-password />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- VMess 配置 -->
        <template v-if="formData.protocol === 'vmess'">
          <el-divider content-position="left">VMess 配置</el-divider>
          <el-row :gutter="20">
            <el-col :span="12">
              <el-form-item label="用户ID (UUID)">
                <el-input v-model="formData.settings.vmessUserId" placeholder="b1e1e5c4-xxxx-xxxx-xxxx-xxxxxxxxxxxx" />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item label="额外ID">
                <el-input-number v-model="formData.settings.vmessAlterId" :min="0" style="width: 100%" />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item label="加密方式">
                <el-select v-model="formData.settings.vmessSecurity" style="width: 100%">
                  <el-option label="auto" value="auto" />
                  <el-option label="aes-128-gcm" value="aes-128-gcm" />
                  <el-option label="chacha20-poly1305" value="chacha20-poly1305" />
                  <el-option label="none" value="none" />
                </el-select>
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- VLESS 配置 -->
        <template v-if="formData.protocol === 'vless'">
          <el-divider content-position="left">VLESS 配置</el-divider>
          <el-row :gutter="20">
            <el-col :span="12">
              <el-form-item label="用户ID (UUID)">
                <el-input v-model="formData.settings.vlessUserId" placeholder="b1e1e5c4-xxxx-xxxx-xxxx-xxxxxxxxxxxx" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="Flow (流控)">
                <el-input v-model="formData.settings.vlessFlow" placeholder="留空或填写：xtls-rprx-vision" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- Trojan 配置 -->
        <template v-if="formData.protocol === 'trojan'">
          <el-divider content-position="left">Trojan 配置</el-divider>
          <el-row :gutter="20">
            <el-col :span="24">
              <el-form-item label="密码">
                <el-input v-model="formData.settings.trojanPassword" placeholder="Trojan 密码" show-password />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- HTTP 配置 -->
        <template v-if="formData.protocol === 'http'">
          <el-divider content-position="left">HTTP 代理配置</el-divider>
          <el-row :gutter="20">
            <el-col :span="12">
              <el-form-item label="用户名（可选）">
                <el-input v-model="formData.settings.httpUsername" placeholder="如果需要认证请填写" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="密码（可选）">
                <el-input v-model="formData.settings.httpPassword" placeholder="如果需要认证请填写" show-password />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- SOCKS 配置 -->
        <template v-if="formData.protocol === 'socks'">
          <el-divider content-position="left">SOCKS 代理配置</el-divider>
          <el-row :gutter="20">
            <el-col :span="8">
              <el-form-item label="SOCKS 版本">
                <el-select v-model="formData.settings.socksVersion" style="width: 100%">
                  <el-option label="SOCKS5" value="socks5" />
                  <el-option label="SOCKS4" value="socks4" />
                </el-select>
              </el-form-item>
            </el-col>
            <el-col :span="8">
              <el-form-item label="用户名（可选）">
                <el-input v-model="formData.settings.socksUsername" placeholder="如果需要认证请填写" />
              </el-form-item>
            </el-col>
            <el-col :span="8">
              <el-form-item label="密码（可选）">
                <el-input v-model="formData.settings.socksPassword" placeholder="如果需要认证请填写" show-password />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- 传输层配置 -->
        <el-divider content-position="left">传输层配置</el-divider>
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item label="传输协议">
              <el-select v-model="formData.settings.network" @change="onNetworkChange" style="width: 100%">
                <el-option label="TCP" value="tcp" />
                <el-option label="WebSocket" value="ws" />
                <el-option label="gRPC" value="grpc" />
                <el-option label="HTTP/2" value="h2" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="传输层安全">
              <el-select v-model="formData.settings.security" @change="onSecurityChange" style="width: 100%">
                <el-option label="none" value="none" />
                <el-option label="TLS" value="tls" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>

        <!-- TLS 配置 -->
        <template v-if="formData.settings.security === 'tls'">
          <el-row :gutter="20">
            <el-col :span="18">
              <el-form-item label="SNI (服务器名称)">
                <el-input v-model="formData.settings.tls.serverName" placeholder="例如：example.com" />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item label="允许不安全连接">
                <el-switch v-model="formData.settings.tls.allowInsecure" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- WebSocket 配置 -->
        <template v-if="formData.settings.network === 'ws'">
          <el-row :gutter="20">
            <el-col :span="12">
              <el-form-item label="WebSocket 路径">
                <el-input v-model="formData.settings.ws.path" placeholder="例如：/ray" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="WebSocket Host">
                <el-input v-model="formData.settings.ws.host" placeholder="例如：example.com" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- gRPC 配置 -->
        <template v-if="formData.settings.network === 'grpc'">
          <el-row :gutter="20">
            <el-col :span="24">
              <el-form-item label="gRPC 服务名">
                <el-input v-model="formData.settings.grpc.serviceName" placeholder="例如：grpcService" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>

        <!-- HTTP/2 配置 -->
        <template v-if="formData.settings.network === 'h2'">
          <el-row :gutter="20">
            <el-col :span="12">
              <el-form-item label="HTTP/2 路径">
                <el-input v-model="formData.settings.h2.path" placeholder="例如：/h2" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="HTTP/2 Host">
                <el-input v-model="formData.settings.h2.host" placeholder="例如：example.com" />
              </el-form-item>
            </el-col>
          </el-row>
        </template>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="saveRule">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Plus,
  Edit,
  Delete,
  Upload,
  Download,
  VideoPlay,
  VideoPause,
  Connection,
  List,
  Document
} from '@element-plus/icons-vue'
import { MyService } from './bindings/xray-manager/index.js'
import { Events } from '@wailsio/runtime'

// 数据
const rules = ref([])
const selectedRules = ref([])
const autoStart = ref(false)
const logContent = ref('')
const dialogVisible = ref(false)
const dialogTitle = ref('添加规则')
const editingRuleId = ref(null)

// 表单数据
const formData = ref({
  alias: '',
  localType: 'socks',
  localPort: 1080,
  protocol: 'shadowsocks',
  serverAddr: '',
  serverPort: 443,
  settings: {
    network: 'tcp',
    security: 'none',
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
    socksVersion: 'socks5',
    socksUsername: '',
    socksPassword: '',
    tls: {
      serverName: '',
      allowInsecure: false
    },
    ws: {
      path: '',
      host: ''
    },
    grpc: {
      serviceName: ''
    },
    h2: {
      path: '',
      host: ''
    }
  }
})

// 页面加载
onMounted(async () => {
  // 监听后端事件
  Events.On('log', (message) => {
    addLog(message.data)
  })

  Events.On('ruleUpdated', (rule) => {
    updateRuleInList(rule.data)
  })

  Events.On('loadRules', () => {
    loadRules()
  })

  // 加载规则和自启状态
  await loadRules()
  await loadAutoStartStatus()
})

// 加载规则列表
async function loadRules() {
  try {
    rules.value = await MyService.GetRules()
  } catch (error) {
    addLog(`[错误] 加载规则失败: ${error}`)
    ElMessage.error('加载规则失败')
  }
}

// 加载开机自启状态
async function loadAutoStartStatus() {
  try {
    autoStart.value = await MyService.GetAutoStart()
  } catch (error) {
    addLog(`[错误] 加载开机自启状态失败: ${error}`)
  }
}

// 更新列表中的规则
function updateRuleInList(rule) {
  const index = rules.value.findIndex(r => r.id === rule.id)
  if (index !== -1) {
    rules.value[index] = rule
  }
}

// 处理选择变化
function handleSelectionChange(selection) {
  selectedRules.value = selection
}

// 处理启动复选框变化
async function handleEnableChange(row) {
  try {
    if (row.enabled) {
      await MyService.StartRule(row.id)
      ElMessage.success(`规则 ${row.alias} 已启动`)
    } else {
      await MyService.StopRule(row.id)
      ElMessage.success(`规则 ${row.alias} 已停止`)
    }
    await loadRules()
  } catch (error) {
    addLog(`[错误] ${row.enabled ? '启动' : '停止'}规则失败: ${error}`)
    ElMessage.error(`${row.enabled ? '启动' : '停止'}失败`)
    await loadRules()
  }
}

// 处理开机自启变化
async function handleAutoStartChange(value) {
  try {
    await MyService.SetAutoStart(value)
    ElMessage.success(`开机自启已${value ? '启用' : '禁用'}`)
  } catch (error) {
    addLog(`[错误] 设置开机自启失败: ${error}`)
    ElMessage.error('设置开机自启失败')
    autoStart.value = !value
  }
}

// 打开添加对话框
function openAddDialog() {
  editingRuleId.value = null
  dialogTitle.value = '添加规则'
  resetFormData()
  dialogVisible.value = true
}

// 编辑规则
function editRule(rule) {
  editingRuleId.value = rule.id
  dialogTitle.value = '编辑规则'

  // 填充表单数据
  formData.value = {
    alias: rule.alias || '',
    localType: rule.localType || 'socks',
    localPort: rule.localPort || 1080,
    protocol: rule.protocol || 'shadowsocks',
    serverAddr: rule.serverAddr || '',
    serverPort: rule.serverPort || 443,
    settings: {
      network: rule.settings?.network || 'tcp',
      security: rule.settings?.security || 'none',
      ssMethod: rule.settings?.ssMethod || 'aes-256-gcm',
      ssPassword: rule.settings?.ssPassword || '',
      vmessUserId: rule.settings?.vmessUserId || '',
      vmessAlterId: rule.settings?.vmessAlterId || 0,
      vmessSecurity: rule.settings?.vmessSecurity || 'auto',
      vlessUserId: rule.settings?.vlessUserId || '',
      vlessFlow: rule.settings?.vlessFlow || '',
      vlessEncryption: rule.settings?.vlessEncryption || 'none',
      trojanPassword: rule.settings?.trojanPassword || '',
      httpUsername: rule.settings?.httpUsername || '',
      httpPassword: rule.settings?.httpPassword || '',
      socksVersion: rule.settings?.socksVersion || 'socks5',
      socksUsername: rule.settings?.socksUsername || '',
      socksPassword: rule.settings?.socksPassword || '',
      tls: {
        serverName: rule.settings?.tls?.serverName || '',
        allowInsecure: rule.settings?.tls?.allowInsecure || false
      },
      ws: {
        path: rule.settings?.ws?.path || '',
        host: rule.settings?.ws?.headers?.Host || ''
      },
      grpc: {
        serviceName: rule.settings?.grpc?.serviceName || ''
      },
      h2: {
        path: rule.settings?.h2?.path || '',
        host: rule.settings?.h2?.host?.[0] || ''
      }
    }
  }

  dialogVisible.value = true
}

// 删除规则
async function deleteRule(row) {
  try {
    await ElMessageBox.confirm(
      `确定要删除规则 "${row.alias}" 吗？`,
      '确认删除',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    await MyService.DeleteRule(row.id)
    ElMessage.success('删除成功')
    await loadRules()
  } catch (error) {
    if (error === 'cancel') return
    addLog(`[错误] 删除规则失败: ${error}`)
    ElMessage.error('删除失败')
  }
}

// 启动选中的规则
async function startSelectedRules() {
  if (selectedRules.value.length === 0) {
    ElMessage.warning('请先选择要启动的规则')
    return
  }

  for (const rule of selectedRules.value) {
    try {
      await MyService.StartRule(rule.id)
    } catch (error) {
      addLog(`[错误] 启动规则失败: ${error}`)
    }
  }

  ElMessage.success(`已启动 ${selectedRules.value.length} 条规则`)
  await loadRules()
}

// 停止选中的规则
async function stopSelectedRules() {
  if (selectedRules.value.length === 0) {
    ElMessage.warning('请先选择要停止的规则')
    return
  }

  for (const rule of selectedRules.value) {
    try {
      await MyService.StopRule(rule.id)
    } catch (error) {
      addLog(`[错误] 停止规则失败: ${error}`)
    }
  }

  ElMessage.success(`已停止 ${selectedRules.value.length} 条规则`)
  await loadRules()
}

// 删除选中的规则
async function deleteSelectedRules() {
  if (selectedRules.value.length === 0) {
    ElMessage.warning('请先选择要删除的规则')
    return
  }

  try {
    await ElMessageBox.confirm(
      `确定要删除选中的 ${selectedRules.value.length} 条规则吗？`,
      '确认删除',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    for (const rule of selectedRules.value) {
      try {
        await MyService.DeleteRule(rule.id)
      } catch (error) {
        addLog(`[错误] 删除规则失败: ${error}`)
      }
    }

    ElMessage.success('删除成功')
    await loadRules()
  } catch (error) {
    if (error === 'cancel') return
  }
}

// 保存规则
async function saveRule() {
  // 验证输入
  if (!formData.value.alias) {
    ElMessage.warning('请输入别名')
    return
  }

  if (!formData.value.localPort || formData.value.localPort < 1 || formData.value.localPort > 65535) {
    ElMessage.warning('请输入有效的本地端口号(1-65535)')
    return
  }

  if (!formData.value.serverAddr) {
    ElMessage.warning('请输入服务器地址')
    return
  }

  if (!formData.value.serverPort || formData.value.serverPort < 1 || formData.value.serverPort > 65535) {
    ElMessage.warning('请输入有效的服务器端口号(1-65535)')
    return
  }

  // 协议特定验证
  if (formData.value.protocol === 'shadowsocks' && !formData.value.settings.ssPassword) {
    ElMessage.warning('请输入Shadowsocks密码')
    return
  }

  if (formData.value.protocol === 'vmess' && !formData.value.settings.vmessUserId) {
    ElMessage.warning('请输入VMess用户ID')
    return
  }

  if (formData.value.protocol === 'vless' && !formData.value.settings.vlessUserId) {
    ElMessage.warning('请输入VLESS用户ID')
    return
  }

  if (formData.value.protocol === 'trojan' && !formData.value.settings.trojanPassword) {
    ElMessage.warning('请输入Trojan密码')
    return
  }

  // 构建规则对象
  const settings = { ...formData.value.settings }

  // 处理 WebSocket Host
  if (settings.network === 'ws' && settings.ws.host) {
    settings.ws = {
      path: settings.ws.path,
      headers: { Host: settings.ws.host }
    }
  }

  // 处理 HTTP/2 Host
  if (settings.network === 'h2' && settings.h2.host) {
    settings.h2 = {
      path: settings.h2.path,
      host: [settings.h2.host]
    }
  }

  const rule = {
    alias: formData.value.alias,
    localType: formData.value.localType,
    localPort: formData.value.localPort,
    protocol: formData.value.protocol,
    serverAddr: formData.value.serverAddr,
    serverPort: formData.value.serverPort,
    settings: settings
  }

  try {
    if (editingRuleId.value) {
      await MyService.UpdateRule(editingRuleId.value, rule)
      ElMessage.success('规则更新成功')
    } else {
      await MyService.AddRule(rule)
      ElMessage.success('规则添加成功')
    }

    dialogVisible.value = false
    await loadRules()
  } catch (error) {
    addLog(`[错误] 保存规则失败: ${error}`)
    ElMessage.error(`保存失败: ${error}`)
  }
}

// 导入配置
async function importConfig() {
  try {
    await MyService.ImportConfig()
    await loadRules()
    addLog('[系统] 配置导入成功')
    ElMessage.success('配置导入成功')
  } catch (error) {
    if (error && error.toString().includes('用户取消')) {
      return
    }
    addLog(`[错误] 导入配置失败: ${error}`)
    ElMessage.error(`导入失败: ${error}`)
  }
}

// 导出配置
async function exportConfig() {
  try {
    const filePath = await MyService.ExportConfig()
    addLog(`[系统] 配置已导出到: ${filePath}`)
    ElMessage.success('配置导出成功')
  } catch (error) {
    if (error && error.toString().includes('用户取消')) {
      return
    }
    addLog(`[错误] 导出配置失败: ${error}`)
    ElMessage.error(`导出失败: ${error}`)
  }
}

// 添加日志
function addLog(message) {
  logContent.value += message + '\n'
  nextTick(() => {
    const textarea = document.querySelector('.log-textarea textarea')
    if (textarea) {
      textarea.scrollTop = textarea.scrollHeight
    }
  })
}

// 清空日志
function clearLog() {
  logContent.value = ''
}

// 重置表单数据
function resetFormData() {
  formData.value = {
    alias: '',
    localType: 'socks',
    localPort: 1080,
    protocol: 'shadowsocks',
    serverAddr: '',
    serverPort: 443,
    settings: {
      network: 'tcp',
      security: 'none',
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
      socksVersion: 'socks5',
      socksUsername: '',
      socksPassword: '',
      tls: {
        serverName: '',
        allowInsecure: false
      },
      ws: {
        path: '',
        host: ''
      },
      grpc: {
        serviceName: ''
      },
      h2: {
        path: '',
        host: ''
      }
    }
  }
}

// 协议变化
function onProtocolChange() {
  // 协议变化时可以进行一些默认值设置
}

// 传输协议变化
function onNetworkChange() {
  // 传输协议变化时可以进行一些默认值设置
}

// 安全类型变化
function onSecurityChange() {
  // 安全类型变化时可以进行一些默认值设置
}
</script>

<style scoped>
.app-container {
  height: 100vh;
  background: #f0f2f5;
}

.app-header {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  display: flex;
  align-items: center;
  padding: 0 24px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.app-header h1 {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 12px;
}

.app-main {
  padding: 16px;
  overflow: auto;
}

.table-card {
  margin-bottom: 16px;
}

.log-card {
  margin-bottom: 16px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.card-title {
  font-size: 16px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 8px;
}

.header-actions {
  display: flex;
  gap: 8px;
}

.batch-actions {
  display: flex;
  gap: 12px;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid #ebeef5;
  align-items: center;
}

.log-textarea {
  font-family: 'Consolas', 'Courier New', monospace;
  font-size: 13px;
}

:deep(.log-textarea .el-textarea__inner) {
  background-color: #1e1e1e;
  color: #d4d4d4;
  font-family: 'Consolas', 'Courier New', monospace;
}
</style>
