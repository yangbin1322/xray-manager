// 全局变量
let rules = [];
let editingRuleId = null;

// 页面加载完成后初始化
window.addEventListener('DOMContentLoaded', () => {
    initializeApp();
});

// 初始化应用
async function initializeApp() {
    // 加载规则
    await loadRules();

    // 加载开机自启状态
    await loadAutoStartStatus();

    // 绑定事件监听器
    bindEventListeners();

    // 监听后端事件
    listenToBackendEvents();

    addLog('[系统] 应用初始化完成');
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
}

// 监听后端事件
function listenToBackendEvents() {
    // 监听日志事件
    window.runtime.EventsOn('log', (message) => {
        addLog(message);
    });

    // 监听规则更新事件
    window.runtime.EventsOn('ruleUpdated', (rule) => {
        updateRuleInTable(rule);
    });
}

// 加载规则列表
async function loadRules() {
    try {
        rules = await window.go.main.App.GetRules();
        renderRulesTable();
    } catch (error) {
        addLog(`[错误] 加载规则失败: ${error}`);
    }
}

// 加载开机自启状态
async function loadAutoStartStatus() {
    try {
        const autoStart = await window.go.main.App.GetAutoStart();
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
        <td style="text-align: left; font-size: 11px;">${escapeHtml(rule.proxyInfo)}</td>
        <td>${escapeHtml(rule.localType)}</td>
        <td>${rule.localPort}</td>
        <td style="text-align: right;">${escapeHtml(rule.realIp || '-')}</td>
        <td>
            <input type="checkbox" ${rule.useIpProxy ? 'checked' : ''} disabled>
        </td>
        <td>
            <input type="checkbox" class="enable-checkbox" ${rule.enabled ? 'checked' : ''}>
        </td>
        <td>
            <button class="btn-edit" onclick="editRule('${rule.id}')">编辑</button>
            <button class="btn-delete" onclick="deleteRule('${rule.id}')">删除</button>
        </td>
    `;

    // 绑定启动复选框事件
    const enableCheckbox = row.querySelector('.enable-checkbox');
    enableCheckbox.addEventListener('change', () => handleEnableChange(rule.id, enableCheckbox.checked));

    // 绑定行复选框事件
    const rowCheckbox = row.querySelector('.row-checkbox');
    rowCheckbox.addEventListener('change', updateSelectAllCheckbox);

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
            await window.go.main.App.StartRule(ruleId);
        } else {
            await window.go.main.App.StopRule(ruleId);
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
        await window.go.main.App.SetAutoStart(event.target.checked);
    } catch (error) {
        addLog(`[错误] 设置开机自启失败: ${error}`);
        event.target.checked = !event.target.checked; // 恢复状态
    }
}

// 打开添加规则对话框
function openAddRuleDialog() {
    editingRuleId = null;
    document.getElementById('dialogTitle').textContent = '添加规则';
    document.getElementById('ruleAlias').value = '';
    document.getElementById('ruleProxyInfo').value = '';
    document.getElementById('ruleLocalType').value = 'auto';
    document.getElementById('ruleLocalPort').value = '';
    document.getElementById('ruleUseIpProxy').checked = false;
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
    document.getElementById('ruleAlias').value = rule.alias;
    document.getElementById('ruleProxyInfo').value = rule.proxyInfo;
    document.getElementById('ruleLocalType').value = rule.localType;
    document.getElementById('ruleLocalPort').value = rule.localPort;
    document.getElementById('ruleUseIpProxy').checked = rule.useIpProxy;
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
    const proxyInfo = document.getElementById('ruleProxyInfo').value.trim();
    const localType = document.getElementById('ruleLocalType').value;
    const localPort = parseInt(document.getElementById('ruleLocalPort').value);
    const useIpProxy = document.getElementById('ruleUseIpProxy').checked;

    // 验证输入
    if (!alias) {
        alert('请输入别名');
        return;
    }

    if (!localPort || localPort < 1 || localPort > 65535) {
        alert('请输入有效的端口号(1-65535)');
        return;
    }

    const rule = {
        alias: alias,
        proxyInfo: proxyInfo,
        localType: localType,
        localPort: localPort,
        useIpProxy: useIpProxy
    };

    try {
        if (editingRuleId) {
            // 更新规则
            await window.go.main.App.UpdateRule(editingRuleId, rule);
        } else {
            // 添加规则
            await window.go.main.App.AddRule(rule);
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
        await window.go.main.App.DeleteRule(ruleId);
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
            await window.go.main.App.StartRule(ruleId);
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
            await window.go.main.App.StopRule(ruleId);
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
            await window.go.main.App.DeleteRule(ruleId);
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

// HTML 转义
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
