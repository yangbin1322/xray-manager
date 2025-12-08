// 全局变量
let rules = [];
let editingRuleId = null;
import { MyService } from "./bindings/xray-manager/index.js";
import { Events } from '@wailsio/runtime';



// 页面加载完成后初始化
window.addEventListener('DOMContentLoaded', () => {
    initializeApp();
});

// 初始化应用
async function initializeApp() {
    // 监听后端事件
    listenToBackendEvents();
    // 绑定事件监听器
    bindEventListeners();

    // 加载规则
    await loadRules();

    // 加载开机自启状态
    await loadAutoStartStatus();
}

// 绑定事件监听器
function bindEventListeners() {
    // 全选复选框
    document.getElementById('selectAll').addEventListener('change', handleSelectAll);

    // 开机自启复选框
    document.getElementById('autoStartCheckbox').addEventListener('change', handleAutoStartChange);

    // 按钮事件
    document.getElementById('addRuleBtn').addEventListener('click', openAddRuleDialog);
    document.getElementById('startSelectedBtn').addEventListener('click', startSelectedRules);
    document.getElementById('stopSelectedBtn').addEventListener('click', stopSelectedRules);
    document.getElementById('deleteSelectedBtn').addEventListener('click', deleteSelectedRules);
    document.getElementById('clearLogBtn').addEventListener('click', clearLog);
    document.getElementById('importConfigBtn').addEventListener('click', importConfig);
    document.getElementById('exportConfigBtn').addEventListener('click', exportConfig);
}

// 监听后端事件
function listenToBackendEvents() {
    // 监听日志事件
    Events.On('log', (message) => {
        addLog(message.data);
    });

    // 监听规则更新事件
    Events.On('ruleUpdated', (rule) => {
        updateRuleInTable(rule.data);
    });
    // 监听规则更新事件
    Events.On('loadRules', (rule) => {
        loadRules();
    });

}

// 加载规则列表
async function loadRules() {
    try {
        rules = await MyService.GetRules();
        renderRulesTable();
    } catch (error) {
        addLog(`[错误] 加载规则失败: ${error}`);
    }
}

// 加载开机自启状态
async function loadAutoStartStatus() {
    try {
        const autoStart = await MyService.GetAutoStart();
        document.getElementById('autoStartCheckbox').checked = autoStart;
    } catch (error) {
        addLog(`[错误] 加载开机自启状态失败: ${error}`);
    }
}

// 渲染规则表格
function renderRulesTable() {
    const tbody = document.getElementById('rulesTableBody');
    tbody.innerHTML = '';

    rules.forEach(rule => {
        const row = createRuleRow(rule);
        tbody.appendChild(row);
    });
}

// 创建规则行
function createRuleRow(rule) {
    const row = document.createElement('tr');
    row.dataset.ruleId = rule.id;

    row.innerHTML = `
        <td>
            <input type="checkbox" class="row-checkbox">
        </td>
        <td>${escapeHtml(rule.alias)}</td>
        <td>${escapeHtml(rule.protocol || '-')}</td>
        <td style="text-align: left; font-size: 11px;">${escapeHtml(rule.serverAddr || '-')}</td>
        <td>${rule.serverPort || '-'}</td>
        <td>${escapeHtml(rule.localType)}</td>
        <td>${rule.localPort}</td>
        <td style="text-align: right;">${escapeHtml(rule.realIp || '-')}</td>
        <td>
            <input type="checkbox" class="enable-checkbox" ${rule.enabled ? 'checked' : ''}>
        </td>
        <td>
            <button class="btn-edit">编辑</button>
            <button class="btn-delete">删除</button>
        </td>
    `;

    // 绑定启动复选框事件
    const enableCheckbox = row.querySelector('.enable-checkbox');
    enableCheckbox.addEventListener('change', () => handleEnableChange(rule.id, enableCheckbox.checked));

    // 绑定行复选框事件
    const rowCheckbox = row.querySelector('.row-checkbox');
    rowCheckbox.addEventListener('change', updateSelectAllCheckbox);

    //绑定行编辑事件
    const editBtn = row.querySelector('.btn-edit');
    editBtn.addEventListener('click', () => editRule(rule.id));
    //绑定行删除事件
    const deleteBtn = row.querySelector('.btn-delete');
    deleteBtn.addEventListener('click', () => deleteRule(rule.id));

    return row;
}

// 更新表格中的规则
function updateRuleInTable(rule) {
    const index = rules.findIndex(r => r.id === rule.id);
    if (index !== -1) {
        rules[index] = rule;
        const row = document.querySelector(`tr[data-rule-id="${rule.id}"]`);
        if (row) {
            const newRow = createRuleRow(rule);
            row.replaceWith(newRow);
        }
    }
}

// 处理全选
function handleSelectAll(event) {
    const checkboxes = document.querySelectorAll('.row-checkbox');
    checkboxes.forEach(checkbox => {
        checkbox.checked = event.target.checked;
    });
}

// 更新全选复选框状态
function updateSelectAllCheckbox() {
    const checkboxes = document.querySelectorAll('.row-checkbox');
    const checkedCount = Array.from(checkboxes).filter(cb => cb.checked).length;
    const selectAllCheckbox = document.getElementById('selectAll');

    selectAllCheckbox.checked = checkedCount === checkboxes.length && checkboxes.length > 0;
    selectAllCheckbox.indeterminate = checkedCount > 0 && checkedCount < checkboxes.length;
}

// 处理启动复选框变化
async function handleEnableChange(ruleId, enabled) {
    try {
        if (enabled) {
            await MyService.StartRule(ruleId);
        } else {
            await MyService.StopRule(ruleId);
        }
        await loadRules();
    } catch (error) {
        addLog(`[错误] ${enabled ? '启动' : '停止'}规则失败: ${error}`);
        await loadRules(); // 重新加载以恢复状态
    }
}

// 处理开机自启变化
async function handleAutoStartChange(event) {
    try {
        await MyService.SetAutoStart(event.target.checked);
    } catch (error) {
        addLog(`[错误] 设置开机自启失败: ${error}`);
        event.target.checked = !event.target.checked; // 恢复状态
    }
}

// 协议变化时切换配置面板
function onProtocolChange() {
    const protocol = document.getElementById('ruleProtocol').value;

    // 隐藏所有协议配置
    document.querySelectorAll('.protocol-settings').forEach(el => {
        el.style.display = 'none';
    });

    // 显示对应协议配置
    if (protocol === 'shadowsocks') {
        document.getElementById('shadowsocksSettings').style.display = 'block';
    } else if (protocol === 'vmess') {
        document.getElementById('vmessSettings').style.display = 'block';
    } else if (protocol === 'vless') {
        document.getElementById('vlessSettings').style.display = 'block';
    } else if (protocol === 'trojan') {
        document.getElementById('trojanSettings').style.display = 'block';
    } else if (protocol === 'http') {
        document.getElementById('httpSettings').style.display = 'block';
    } else if (protocol === 'socks') {
        document.getElementById('socksSettings').style.display = 'block';
    }
}

// 传输协议变化时切换配置面板
function onNetworkChange() {
    const network = document.getElementById('network').value;

    // 隐藏所有传输层配置
    document.getElementById('wsSettings').style.display = 'none';
    document.getElementById('grpcSettings').style.display = 'none';
    document.getElementById('h2Settings').style.display = 'none';

    // 显示对应传输层配置
    if (network === 'ws') {
        document.getElementById('wsSettings').style.display = 'block';
    } else if (network === 'grpc') {
        document.getElementById('grpcSettings').style.display = 'block';
    } else if (network === 'h2') {
        document.getElementById('h2Settings').style.display = 'block';
    }
}

// 安全类型变化时切换TLS配置
function onSecurityChange() {
    const security = document.getElementById('security').value;
    document.getElementById('tlsSettings').style.display = security === 'tls' ? 'block' : 'none';
}

// 打开添加规则对话框
function openAddRuleDialog() {
    editingRuleId = null;
    document.getElementById('dialogTitle').textContent = '添加规则';

    // 清空表单
    document.getElementById('ruleAlias').value = '';
    document.getElementById('ruleLocalType').value = 'socks5';
    document.getElementById('ruleLocalPort').value = '';
    document.getElementById('ruleProtocol').value = 'shadowsocks';
    document.getElementById('ruleServerAddr').value = '';
    document.getElementById('ruleServerPort').value = '';

    // 清空协议配置
    document.getElementById('ssMethod').value = 'aes-256-gcm';
    document.getElementById('ssPassword').value = '';
    document.getElementById('vmessUserId').value = '';
    document.getElementById('vmessAlterId').value = '0';
    document.getElementById('vmessSecurity').value = 'auto';
    document.getElementById('vlessUserId').value = '';
    document.getElementById('vlessFlow').value = '';
    document.getElementById('vlessEncryption').value = 'none';
    document.getElementById('trojanPassword').value = '';
    document.getElementById('httpUsername').value = '';
    document.getElementById('httpPassword').value = '';
    document.getElementById('socksVersion').value = 'socks5';
    document.getElementById('socksUsername').value = '';
    document.getElementById('socksPassword').value = '';

    // 清空传输层配置
    document.getElementById('network').value = 'tcp';
    document.getElementById('security').value = 'none';
    document.getElementById('tlsServerName').value = '';
    document.getElementById('tlsAllowInsecure').checked = false;
    document.getElementById('wsPath').value = '';
    document.getElementById('wsHost').value = '';
    document.getElementById('grpcServiceName').value = '';
    document.getElementById('h2Path').value = '';
    document.getElementById('h2Host').value = '';

    // 触发协议和传输层变化
    onProtocolChange();
    onNetworkChange();
    onSecurityChange();

    document.getElementById('ruleDialog').style.display = 'flex';
}

// 打开编辑规则对话框
function editRule(ruleId) {
    const rule = rules.find(r => r.id === ruleId);
    if (!rule) {
        addLog(`[错误] 未找到规则: ${ruleId}`);
        return;
    }

    editingRuleId = ruleId;
    document.getElementById('dialogTitle').textContent = '编辑规则';

    // 填充基本信息
    document.getElementById('ruleAlias').value = rule.alias || '';
    document.getElementById('ruleLocalType').value = rule.localType || 'socks5';
    document.getElementById('ruleLocalPort').value = rule.localPort || '';
    document.getElementById('ruleProtocol').value = rule.protocol || 'shadowsocks';
    document.getElementById('ruleServerAddr').value = rule.serverAddr || '';
    document.getElementById('ruleServerPort').value = rule.serverPort || '';

    // 填充协议配置
    const settings = rule.settings || {};

    // Shadowsocks
    document.getElementById('ssMethod').value = settings.ssMethod || 'aes-256-gcm';
    document.getElementById('ssPassword').value = settings.ssPassword || '';

    // VMess
    document.getElementById('vmessUserId').value = settings.vmessUserId || '';
    document.getElementById('vmessAlterId').value = settings.vmessAlterId || 0;
    document.getElementById('vmessSecurity').value = settings.vmessSecurity || 'auto';

    // VLESS
    document.getElementById('vlessUserId').value = settings.vlessUserId || '';
    document.getElementById('vlessFlow').value = settings.vlessFlow || '';
    document.getElementById('vlessEncryption').value = settings.vlessEncryption || 'none';

    // Trojan
    document.getElementById('trojanPassword').value = settings.trojanPassword || '';

    // HTTP
    document.getElementById('httpUsername').value = settings.httpUsername || '';
    document.getElementById('httpPassword').value = settings.httpPassword || '';

    // SOCKS
    document.getElementById('socksVersion').value = settings.socksVersion || 'socks5';
    document.getElementById('socksUsername').value = settings.socksUsername || '';
    document.getElementById('socksPassword').value = settings.socksPassword || '';

    // 填充传输层配置
    document.getElementById('network').value = settings.network || 'tcp';
    document.getElementById('security').value = settings.security || 'none';

    // TLS
    if (settings.tls) {
        document.getElementById('tlsServerName').value = settings.tls.serverName || '';
        document.getElementById('tlsAllowInsecure').checked = settings.tls.allowInsecure || false;
    }

    // WebSocket
    if (settings.ws) {
        document.getElementById('wsPath').value = settings.ws.path || '';
        document.getElementById('wsHost').value = (settings.ws.headers && settings.ws.headers.Host) || '';
    }

    // gRPC
    if (settings.grpc) {
        document.getElementById('grpcServiceName').value = settings.grpc.serviceName || '';
    }

    // HTTP/2
    if (settings.h2) {
        document.getElementById('h2Path').value = settings.h2.path || '';
        document.getElementById('h2Host').value = (settings.h2.host && settings.h2.host[0]) || '';
    }

    // 触发协议和传输层变化
    onProtocolChange();
    onNetworkChange();
    onSecurityChange();

    document.getElementById('ruleDialog').style.display = 'flex';
}

// 关闭规则对话框
function closeRuleDialog() {
    document.getElementById('ruleDialog').style.display = 'none';
    editingRuleId = null;
}

// 保存规则
async function saveRule() {
    const alias = document.getElementById('ruleAlias').value.trim();
    const localType = document.getElementById('ruleLocalType').value;
    const localPort = parseInt(document.getElementById('ruleLocalPort').value);
    const protocol = document.getElementById('ruleProtocol').value;
    const serverAddr = document.getElementById('ruleServerAddr').value.trim();
    const serverPort = parseInt(document.getElementById('ruleServerPort').value);

    // 验证输入
    if (!alias) {
        alert('请输入别名');
        return;
    }

    if (!localPort || localPort < 1 || localPort > 65535) {
        alert('请输入有效的本地端口号(1-65535)');
        return;
    }

    if (!serverAddr) {
        alert('请输入服务器地址');
        return;
    }

    if (!serverPort || serverPort < 1 || serverPort > 65535) {
        alert('请输入有效的服务器端口号(1-65535)');
        return;
    }

    // 构建设置对象
    const settings = {
        network: document.getElementById('network').value,
        security: document.getElementById('security').value,
    };

    // 根据协议添加配置
    if (protocol === 'shadowsocks') {
        settings.ssMethod = document.getElementById('ssMethod').value;
        settings.ssPassword = document.getElementById('ssPassword').value;
        if (!settings.ssPassword) {
            alert('请输入Shadowsocks密码');
            return;
        }
    } else if (protocol === 'vmess') {
        settings.vmessUserId = document.getElementById('vmessUserId').value.trim();
        settings.vmessAlterId = parseInt(document.getElementById('vmessAlterId').value) || 0;
        settings.vmessSecurity = document.getElementById('vmessSecurity').value;
        if (!settings.vmessUserId) {
            alert('请输入VMess用户ID');
            return;
        }
    } else if (protocol === 'vless') {
        settings.vlessUserId = document.getElementById('vlessUserId').value.trim();
        settings.vlessFlow = document.getElementById('vlessFlow').value.trim();
        settings.vlessEncryption = document.getElementById('vlessEncryption').value;
        if (!settings.vlessUserId) {
            alert('请输入VLESS用户ID');
            return;
        }
    } else if (protocol === 'trojan') {
        settings.trojanPassword = document.getElementById('trojanPassword').value;
        if (!settings.trojanPassword) {
            alert('请输入Trojan密码');
            return;
        }
    } else if (protocol === 'http') {
        settings.httpUsername = document.getElementById('httpUsername').value.trim();
        settings.httpPassword = document.getElementById('httpPassword').value.trim();
        // HTTP 代理用户名和密码是可选的，不需要验证
    } else if (protocol === 'socks') {
        settings.socksVersion = document.getElementById('socksVersion').value;
        settings.socksUsername = document.getElementById('socksUsername').value.trim();
        settings.socksPassword = document.getElementById('socksPassword').value.trim();
        // SOCKS 代理用户名和密码是可选的，不需要验证
    }

    // TLS配置
    if (settings.security === 'tls') {
        settings.tls = {
            serverName: document.getElementById('tlsServerName').value.trim(),
            allowInsecure: document.getElementById('tlsAllowInsecure').checked,
        };
    }

    // WebSocket配置
    if (settings.network === 'ws') {
        const wsHost = document.getElementById('wsHost').value.trim();
        settings.ws = {
            path: document.getElementById('wsPath').value.trim(),
        };
        if (wsHost) {
            settings.ws.headers = { Host: wsHost };
        }
    }

    // gRPC配置
    if (settings.network === 'grpc') {
        settings.grpc = {
            serviceName: document.getElementById('grpcServiceName').value.trim(),
        };
    }

    // HTTP/2配置
    if (settings.network === 'h2') {
        const h2Host = document.getElementById('h2Host').value.trim();
        settings.h2 = {
            path: document.getElementById('h2Path').value.trim(),
        };
        if (h2Host) {
            settings.h2.host = [h2Host];
        }
    }

    const rule = {
        alias: alias,
        localType: localType,
        localPort: localPort,
        protocol: protocol,
        serverAddr: serverAddr,
        serverPort: serverPort,
        settings: settings,
    };

    try {
        if (editingRuleId) {
            // 更新规则
            await MyService.UpdateRule(editingRuleId, rule);
        } else {
            // 添加规则
            await MyService.AddRule(rule);
        }

        closeRuleDialog();
        await loadRules();
    } catch (error) {
        addLog(`[错误] 保存规则失败: ${error}`);
        alert(`保存失败: ${error}`);
    }
}

// 删除规则
async function deleteRule(ruleId) {
    if (!confirm('确定要删除这条规则吗?')) {
        return;
    }

    try {
        await MyService.DeleteRule(ruleId);
        await loadRules();
    } catch (error) {
        addLog(`[错误] 删除规则失败: ${error}`);
        alert(`删除失败: ${error}`);
    }
}

// 获取选中的规则 ID
function getSelectedRuleIds() {
    const selectedIds = [];
    const checkboxes = document.querySelectorAll('.row-checkbox:checked');

    checkboxes.forEach(checkbox => {
        const row = checkbox.closest('tr');
        const ruleId = row.dataset.ruleId;
        selectedIds.push(ruleId);
    });

    return selectedIds;
}

// 启动选中的规则
async function startSelectedRules() {
    const selectedIds = getSelectedRuleIds();

    if (selectedIds.length === 0) {
        alert('请先选择要启动的规则');
        return;
    }

    for (const ruleId of selectedIds) {
        try {
            await MyService.StartRule(ruleId);
        } catch (error) {
            addLog(`[错误] 启动规则失败: ${error}`);
        }
    }

    await loadRules();
}

// 停止选中的规则
async function stopSelectedRules() {
    const selectedIds = getSelectedRuleIds();

    if (selectedIds.length === 0) {
        alert('请先选择要停止的规则');
        return;
    }

    for (const ruleId of selectedIds) {
        try {
            await MyService.StopRule(ruleId);
        } catch (error) {
            addLog(`[错误] 停止规则失败: ${error}`);
        }
    }

    await loadRules();
}

// 删除选中的规则
async function deleteSelectedRules() {
    const selectedIds = getSelectedRuleIds();

    if (selectedIds.length === 0) {
        alert('请先选择要删除的规则');
        return;
    }

    if (!confirm(`确定要删除选中的 ${selectedIds.length} 条规则吗?`)) {
        return;
    }

    for (const ruleId of selectedIds) {
        try {
            await MyService.DeleteRule(ruleId);
        } catch (error) {
            addLog(`[错误] 删除规则失败: ${error}`);
        }
    }

    await loadRules();
}

// 添加日志
function addLog(message) {
    const logTextarea = document.getElementById('logTextarea');
    logTextarea.value += message + '\n';
    logTextarea.scrollTop = logTextarea.scrollHeight;
}

// 清空日志
function clearLog() {
    document.getElementById('logTextarea').value = '';
}

// 导出配置
async function exportConfig() {
    try {
        const filePath = await MyService.ExportConfig();
        addLog(`[系统] 配置已导出到: ${filePath}`);
    } catch (error) {
        if (error && error.toString().includes('用户取消')) {
            // 用户取消操作，不显示错误
            return;
        }
        addLog(`[错误] 导出配置失败: ${error}`);
        alert(`导出失败: ${error}`);
    }
}

// 导入配置
async function importConfig() {
    try {
        await MyService.ImportConfig();
        await loadRules(); // 重新加载规则列表
        addLog('[系统] 配置导入成功');
    } catch (error) {
        if (error && error.toString().includes('用户取消')) {
            // 用户取消操作，不显示错误
            return;
        }
        addLog(`[错误] 导入配置失败: ${error}`);
        alert(`导入失败: ${error}`);
    }
}

// HTML 转义
function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
// 将需要在HTML中调用的函数暴露到全局作用域
window.closeRuleDialog = closeRuleDialog;
window.saveRule = saveRule;
window.onProtocolChange = onProtocolChange;
window.onNetworkChange = onNetworkChange;
window.onSecurityChange = onSecurityChange;