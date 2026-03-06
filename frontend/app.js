// 全局变量
let rules = [];
let editingRuleId = null;
let sortColumn = null; // 当前排序列
let sortDirection = 'asc'; // 排序方向: asc, desc
let searchKeyword = ''; // 节点搜索关键字
let statusFilter = 'all'; // 状态过滤: all, running, stopped (Feature 1)
let dragSrcRow = null; // 拖拽源行 (Feature 2)
import { MyService } from "./bindings/xray-manager/index.js";
import { Events } from '@wailsio/runtime';
import * as Extended from "./app-extended.js";



// 页面加载完成后初始化
window.addEventListener('DOMContentLoaded', () => {
    initializeApp();
});

// 导出给其他模块使用
window.loadRules = loadRules;
window.filterRulesByGroup = filterRulesByGroup;

// 初始化应用
async function initializeApp() {
    // 初始化深色模式 (Feature 3)
    initTheme();

    // 监听后端事件
    listenToBackendEvents();
    // 绑定事件监听器
    bindEventListeners();

    // 加载规则
    await loadRules();

    // 加载开机自启状态
    await loadAutoStartStatus();

    // 加载分组和订阅
    await Extended.loadGroups();

    // 设置日志过滤
    Extended.setupLogFiltering();

    // 加载系统代理状态 (Feature 5)
    await loadSysProxyStatus();
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
    document.getElementById('clearLogBtn').addEventListener('click', () => {
        clearLog();
        Extended.clearBackendLogs();
    });
    document.getElementById('importConfigBtn').addEventListener('click', importConfig);
    document.getElementById('exportConfigBtn').addEventListener('click', exportConfig);
    document.getElementById('testSelectedSpeedBtn').addEventListener('click', testSelectedRulesSpeed);
    document.getElementById('exportSpeedReportBtn').addEventListener('click', exportSpeedReport);
    document.getElementById('batchImportBtn').addEventListener('click', openBatchImportDialog);

    // 新功能按钮
    document.getElementById('addGroupBtn').addEventListener('click', Extended.openAddGroupDialog);
    document.getElementById('manageSubscriptionsBtn').addEventListener('click', Extended.openSubscriptionDialog);
    document.getElementById('addSubscriptionBtn').addEventListener('click', Extended.openAddSubscriptionDialog);

    // 负载均衡和链式代理 (Feature 7, 8)
    document.getElementById('addLoadBalanceBtn').addEventListener('click', openLBDialog);
    document.getElementById('addChainProxyBtn').addEventListener('click', openChainDialog);

    // 深色模式切换 (Feature 3)
    document.getElementById('themeToggleBtn').addEventListener('click', toggleTheme);

    // 系统代理 (Feature 5)
    document.getElementById('enableSysProxyBtn').addEventListener('click', enableSysProxy);
    document.getElementById('disableSysProxyBtn').addEventListener('click', disableSysProxy);

    // 状态过滤 (Feature 1)
    document.querySelectorAll('.filter-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            statusFilter = btn.getAttribute('data-filter');
            renderRulesTable();
        });
    });

    // 表格排序
    document.querySelectorAll('th.sortable').forEach(th => {
        th.addEventListener('click', () => {
            const column = th.getAttribute('data-sort');
            setSortColumn(column);
        });
    });

    // 节点搜索
    const searchInput = document.getElementById('nodeSearchInput');
    if (searchInput) {
        searchInput.addEventListener('input', (e) => {
            searchKeyword = e.target.value.trim();
            renderRulesTable();
        });
    }
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

    let filteredRules = rules;

    // 应用分组过滤
    if (Extended.currentGroupFilter !== null && Extended.currentGroupFilter !== undefined) {
        filteredRules = filteredRules.filter(rule => rule.groupId === Extended.currentGroupFilter);
    }

    // 应用状态过滤 (Feature 1)
    if (statusFilter === 'running') {
        filteredRules = filteredRules.filter(rule => rule.enabled);
    } else if (statusFilter === 'stopped') {
        filteredRules = filteredRules.filter(rule => !rule.enabled);
    }

    // 应用搜索过滤
    if (searchKeyword) {
        const keyword = searchKeyword.toLowerCase();
        filteredRules = filteredRules.filter(rule => {
            return (
                (rule.alias && rule.alias.toLowerCase().includes(keyword)) ||
                (rule.serverAddr && rule.serverAddr.toLowerCase().includes(keyword)) ||
                (rule.protocol && rule.protocol.toLowerCase().includes(keyword)) ||
                (rule.groupName && rule.groupName.toLowerCase().includes(keyword)) ||
                (rule.localPort && String(rule.localPort).includes(keyword))
            );
        });
    }

    // 应用排序
    const sortedRules = sortColumn ? sortRules([...filteredRules], sortColumn, sortDirection) : filteredRules;

    sortedRules.forEach(rule => {
        const row = createRuleRow(rule);
        tbody.appendChild(row);
    });
}

// 排序规则
function sortRules(rulesToSort, column, direction) {
    return rulesToSort.sort((a, b) => {
        let valueA = a[column] || 0;
        let valueB = b[column] || 0;

        // 对于数值类型
        if (typeof valueA === 'number' && typeof valueB === 'number') {
            return direction === 'asc' ? valueA - valueB : valueB - valueA;
        }

        // 对于字符串类型
        const strA = String(valueA).toLowerCase();
        const strB = String(valueB).toLowerCase();
        if (direction === 'asc') {
            return strA.localeCompare(strB);
        } else {
            return strB.localeCompare(strA);
        }
    });
}

// 设置排序
function setSortColumn(column) {
    // 如果点击的是当前排序列，切换方向
    if (sortColumn === column) {
        sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
        sortColumn = column;
        sortDirection = 'asc';
    }

    // 更新表头样式
    document.querySelectorAll('th.sortable').forEach(th => {
        th.removeAttribute('data-sort-direction');
    });

    const currentHeader = document.querySelector(`th.sortable[data-sort="${column}"]`);
    if (currentHeader) {
        currentHeader.setAttribute('data-sort-direction', sortDirection);
    }

    // 重新渲染表格
    renderRulesTable();
}

// 创建规则行
function createRuleRow(rule) {
    const row = document.createElement('tr');
    row.dataset.ruleId = rule.id;
    row.draggable = true; // Feature 2: 启用拖拽

    // 格式化延迟显示
    let latencyText = '-';
    if (rule.testStatus === 'testing') {
        latencyText = '测试中...';
    } else if (rule.latency > 0) {
        latencyText = `${rule.latency}ms`;
        if (rule.latency < 100) {
            latencyText = `<span class="speed-good">${latencyText}</span>`;
        } else if (rule.latency < 300) {
            latencyText = `<span class="speed-medium">${latencyText}</span>`;
        } else {
            latencyText = `<span class="speed-bad">${latencyText}</span>`;
        }
    }

    // 格式化速度显示
    let speedText = '-';
    if (rule.downloadSpeed > 0) {
        speedText = `${rule.downloadSpeed.toFixed(2)}MB/s`;
    }

    row.innerHTML = `
        <td>
            <input type="checkbox" class="row-checkbox">
        </td>
        <td>
            ${escapeHtml(rule.alias)}
            ${rule.groupName ? `<br><small class="group-tag">${escapeHtml(rule.groupName)}</small>` : ''}
        </td>
        <td>${escapeHtml(rule.protocol || '-')}</td>
        <td style="text-align: left; font-size: 11px;">${escapeHtml(rule.serverAddr || '-')}</td>
        <td>${rule.serverPort || '-'}</td>
        <td>${escapeHtml(rule.localType)}</td>
        <td>${rule.localPort}</td>
        <td>${latencyText}</td>
        <td>${speedText}</td>
        <td style="text-align: right;">${escapeHtml(rule.realIp || '-')}</td>
        <td>
            <input type="checkbox" class="enable-checkbox" ${rule.enabled ? 'checked' : ''}>
        </td>
        <td>
            <button class="btn-edit">编辑</button>
            <button class="btn-test" title="测速">测速</button>
            <button class="btn-sysproxy" title="设为系统代理" style="padding:4px 6px;margin:0 2px;border:none;background:#9b59b6;color:white;border-radius:3px;cursor:pointer;font-size:11px;">代理</button>
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
    // 绑定测速事件
    const testBtn = row.querySelector('.btn-test');
    testBtn.addEventListener('click', () => Extended.testRuleSpeed(rule.id));

    // 绑定系统代理事件 (Feature 5)
    const sysProxyBtn = row.querySelector('.btn-sysproxy');
    sysProxyBtn.addEventListener('click', () => setRuleAsSystemProxy(rule.id));

    // 拖拽排序事件 (Feature 2)
    row.addEventListener('dragstart', handleDragStart);
    row.addEventListener('dragover', handleDragOver);
    row.addEventListener('dragenter', handleDragEnter);
    row.addEventListener('dragleave', handleDragLeave);
    row.addEventListener('drop', handleDrop);
    row.addEventListener('dragend', handleDragEnd);

    return row;
}

// ==================== 拖拽排序 (Feature 2) ====================

function handleDragStart(e) {
    dragSrcRow = this;
    this.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', this.dataset.ruleId);
}

function handleDragOver(e) {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
}

function handleDragEnter(e) {
    e.preventDefault();
    this.classList.add('drag-over');
}

function handleDragLeave(e) {
    this.classList.remove('drag-over');
}

function handleDrop(e) {
    e.preventDefault();
    this.classList.remove('drag-over');

    if (dragSrcRow === this) return;

    const srcId = dragSrcRow.dataset.ruleId;
    const destId = this.dataset.ruleId;

    // 在 rules 数组中交换位置
    const srcIndex = rules.findIndex(r => r.id === srcId);
    const destIndex = rules.findIndex(r => r.id === destId);

    if (srcIndex === -1 || destIndex === -1) return;

    // 移除源元素并插入到目标位置
    const [moved] = rules.splice(srcIndex, 1);
    rules.splice(destIndex, 0, moved);

    // 重新渲染表格
    renderRulesTable();

    // 保存排序到后端
    const orderedIDs = rules.map(r => r.id);
    MyService.SaveRuleOrder(orderedIDs).catch(err => {
        addLog(`[错误] 保存排序失败: ${err}`);
    });
}

function handleDragEnd(e) {
    this.classList.remove('dragging');
    document.querySelectorAll('.drag-over').forEach(el => el.classList.remove('drag-over'));
    dragSrcRow = null;
}

// ==================== 深色模式 (Feature 3) ====================

function initTheme() {
    const savedTheme = localStorage.getItem('xray-manager-theme') || 'light';
    document.documentElement.setAttribute('data-theme', savedTheme);
    updateThemeIcon(savedTheme);
}

function toggleTheme() {
    const current = document.documentElement.getAttribute('data-theme') || 'light';
    const newTheme = current === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', newTheme);
    localStorage.setItem('xray-manager-theme', newTheme);
    updateThemeIcon(newTheme);
}

function updateThemeIcon(theme) {
    const btn = document.getElementById('themeToggleBtn');
    if (btn) {
        btn.textContent = theme === 'dark' ? '☾' : '☀';
        btn.title = theme === 'dark' ? '切换浅色模式' : '切换深色模式';
    }
}

// ==================== 批量导入 (Feature 4) ====================

function openBatchImportDialog() {
    document.getElementById('batchImportText').value = '';
    document.getElementById('batchImportDialog').style.display = 'flex';
}

window.closeBatchImportDialog = function() {
    document.getElementById('batchImportDialog').style.display = 'none';
};

window.pasteFromClipboard = async function() {
    try {
        const text = await navigator.clipboard.readText();
        document.getElementById('batchImportText').value = text;
    } catch (error) {
        addLog(`[错误] 读取剪贴板失败: ${error}`);
        alert('读取剪贴板失败，请手动粘贴');
    }
};

window.doBatchImport = async function() {
    const text = document.getElementById('batchImportText').value.trim();
    if (!text) {
        alert('请输入分享链接');
        return;
    }

    try {
        const count = await MyService.ImportShareLinks(text);
        closeBatchImportDialog();
        await loadRules();
        addLog(`[导入] 成功导入 ${count} 个节点`);
        alert(`成功导入 ${count} 个节点`);
    } catch (error) {
        addLog(`[错误] 批量导入失败: ${error}`);
        alert(`导入失败: ${error}`);
    }
};

// ==================== 系统代理 (Feature 5) ====================

async function loadSysProxyStatus() {
    try {
        const enabled = await MyService.GetSystemProxyStatus();
        document.getElementById('enableSysProxyBtn').style.display = enabled ? 'none' : '';
        document.getElementById('disableSysProxyBtn').style.display = enabled ? '' : 'none';
    } catch (error) {
        // 忽略
    }
}

async function setRuleAsSystemProxy(ruleId) {
    try {
        await MyService.EnableSystemProxy(ruleId);
        addLog('[系统代理] 设置成功');
        await loadSysProxyStatus();
    } catch (error) {
        addLog(`[错误] 设置系统代理失败: ${error}`);
        alert(`设置系统代理失败: ${error}`);
    }
}

async function enableSysProxy() {
    const selectedIds = getSelectedRuleIds();
    if (selectedIds.length !== 1) {
        alert('请选中一个节点设置为系统代理');
        return;
    }
    await setRuleAsSystemProxy(selectedIds[0]);
}

async function disableSysProxy() {
    try {
        await MyService.DisableSystemProxy();
        addLog('[系统代理] 已取消');
        await loadSysProxyStatus();
    } catch (error) {
        addLog(`[错误] 取消系统代理失败: ${error}`);
        alert(`取消系统代理失败: ${error}`);
    }
}

// ==================== 选中节点测速 (Feature 6) ====================

async function testSelectedRulesSpeed() {
    const selectedIds = getSelectedRuleIds();
    if (selectedIds.length === 0) {
        alert('请先选择要测速的节点');
        return;
    }

    if (!confirm(`确定要测速选中的 ${selectedIds.length} 个节点吗？`)) {
        return;
    }

    try {
        await MyService.TestSelectedRulesSpeed(selectedIds);
        addLog(`[测速] 开始测速 ${selectedIds.length} 个节点...`);
    } catch (error) {
        addLog(`[错误] 测速失败: ${error}`);
    }
}

// ==================== 负载均衡 (Feature 7) ====================

function openLBDialog() {
    document.getElementById('lbAlias').value = '';
    document.getElementById('lbLocalType').value = 'socks';
    document.getElementById('lbLocalPort').value = '';

    // 生成节点选择列表
    const list = document.getElementById('lbNodeList');
    list.innerHTML = '';
    rules.forEach(rule => {
        const item = document.createElement('div');
        item.className = 'node-select-item';
        item.innerHTML = `<input type="checkbox" value="${rule.id}"> <span>${escapeHtml(rule.alias)} (${escapeHtml(rule.protocol)} - ${escapeHtml(rule.serverAddr)})</span>`;
        list.appendChild(item);
    });

    document.getElementById('loadBalanceDialog').style.display = 'flex';
}

window.closeLBDialog = function() {
    document.getElementById('loadBalanceDialog').style.display = 'none';
};

window.saveLB = async function() {
    const alias = document.getElementById('lbAlias').value.trim();
    const localType = document.getElementById('lbLocalType').value;
    const localPort = parseInt(document.getElementById('lbLocalPort').value);

    if (!alias) { alert('请输入别名'); return; }
    if (!localPort || localPort < 1 || localPort > 65535) { alert('请输入有效端口'); return; }

    const selectedNodes = [];
    document.querySelectorAll('#lbNodeList input[type="checkbox"]:checked').forEach(cb => {
        selectedNodes.push(cb.value);
    });

    if (selectedNodes.length === 0) { alert('请至少选择一个子节点'); return; }

    try {
        await MyService.AddLoadBalancer({
            alias: alias,
            localType: localType,
            localPort: localPort,
            nodeIds: selectedNodes,
        });
        closeLBDialog();
        addLog(`[负载均衡] 添加成功: ${alias}`);
        alert('负载均衡节点添加成功');
    } catch (error) {
        addLog(`[错误] 添加负载均衡失败: ${error}`);
        alert(`添加失败: ${error}`);
    }
};

// ==================== 链式代理 (Feature 8, 9) ====================

let chainSelectedNodeIDs = [];

function openChainDialog() {
    document.getElementById('chainAlias').value = '';
    document.getElementById('chainLocalType').value = 'socks';
    document.getElementById('chainLocalPort').value = '';
    chainSelectedNodeIDs = [];

    // 生成节点选择列表（包含普通节点和负载均衡节点）
    const list = document.getElementById('chainNodeList');
    list.innerHTML = '';

    // 普通节点
    rules.forEach(rule => {
        const item = document.createElement('div');
        item.className = 'node-select-item';
        item.innerHTML = `<button class="btn-small" onclick="addToChain('${rule.id}', '${escapeHtml(rule.alias)}', 'rule')">+ 添加</button> <span>${escapeHtml(rule.alias)} (${escapeHtml(rule.protocol)})</span>`;
        list.appendChild(item);
    });

    // 分隔线
    const sep = document.createElement('div');
    sep.innerHTML = '<hr style="margin:8px 0"><strong style="font-size:12px;">负载均衡节点：</strong>';
    list.appendChild(sep);

    // 加载负载均衡节点
    MyService.GetLoadBalancers().then(lbs => {
        if (lbs && lbs.length > 0) {
            lbs.forEach(lb => {
                const item = document.createElement('div');
                item.className = 'node-select-item';
                item.innerHTML = `<button class="btn-small" onclick="addToChain('${lb.id}', '${escapeHtml(lb.alias)}', 'lb')">+ 添加</button> <span>[LB] ${escapeHtml(lb.alias)}</span>`;
                list.appendChild(item);
            });
        } else {
            const noLB = document.createElement('div');
            noLB.innerHTML = '<span style="color:#999;font-size:12px;">暂无负载均衡节点</span>';
            list.appendChild(noLB);
        }
    });

    renderChainSelected();
    document.getElementById('chainProxyDialog').style.display = 'flex';
}

window.closeChainDialog = function() {
    document.getElementById('chainProxyDialog').style.display = 'none';
};

window.addToChain = function(nodeId, name, type) {
    chainSelectedNodeIDs.push({ id: nodeId, name: name, type: type });
    renderChainSelected();
};

window.removeFromChain = function(index) {
    chainSelectedNodeIDs.splice(index, 1);
    renderChainSelected();
};

function renderChainSelected() {
    const container = document.getElementById('chainSelectedNodes');
    container.innerHTML = '';

    if (chainSelectedNodeIDs.length === 0) {
        container.innerHTML = '<span style="color:#999;font-size:12px;">请从上方列表添加节点</span>';
        return;
    }

    chainSelectedNodeIDs.forEach((node, index) => {
        const item = document.createElement('span');
        item.className = 'chain-node-item';
        const prefix = node.type === 'lb' ? '[LB] ' : '';
        item.innerHTML = `${prefix}${escapeHtml(node.name)} <span class="chain-remove" onclick="removeFromChain(${index})">×</span>`;
        container.appendChild(item);

        if (index < chainSelectedNodeIDs.length - 1) {
            const arrow = document.createElement('span');
            arrow.className = 'chain-arrow';
            arrow.textContent = ' → ';
            arrow.style.color = '#999';
            container.appendChild(arrow);
        }
    });
}

window.saveChain = async function() {
    const alias = document.getElementById('chainAlias').value.trim();
    const localType = document.getElementById('chainLocalType').value;
    const localPort = parseInt(document.getElementById('chainLocalPort').value);

    if (!alias) { alert('请输入别名'); return; }
    if (!localPort || localPort < 1 || localPort > 65535) { alert('请输入有效端口'); return; }
    if (chainSelectedNodeIDs.length < 2) { alert('链式代理至少需要2个节点'); return; }

    try {
        await MyService.AddChainProxy({
            alias: alias,
            localType: localType,
            localPort: localPort,
            chainNodes: chainSelectedNodeIDs.map(n => n.id),
        });
        closeChainDialog();
        addLog(`[链式代理] 添加成功: ${alias}`);
        alert('链式代理添加成功');
    } catch (error) {
        addLog(`[错误] 添加链式代理失败: ${error}`);
        alert(`添加失败: ${error}`);
    }
};

// 按分组过滤规则
function filterRulesByGroup(groupId) {
    // 直接调用 renderRulesTable，它会应用分组过滤
    // currentGroupFilter 已在 app-extended.js 中被设置
    renderRulesTable();
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

// 填充分组选择器
function populateGroupSelector() {
    const groupSelect = document.getElementById('ruleGroupId');
    groupSelect.innerHTML = '<option value="">无分组</option>';

    // 只显示手动创建的分组
    const manualGroups = Extended.groups.filter(g => g.source === 'manual');
    manualGroups.forEach(group => {
        const option = document.createElement('option');
        option.value = group.id;
        option.textContent = group.name;
        groupSelect.appendChild(option);
    });
}

// 打开添加规则对话框
function openAddRuleDialog() {
    editingRuleId = null;
    document.getElementById('dialogTitle').textContent = '添加规则';

    // 设置端口验证
    Extended.setupPortValidation();

    // 填充分组选择器
    populateGroupSelector();

    // 清空表单
    document.getElementById('ruleAlias').value = '';
    document.getElementById('ruleGroupId').value = '';
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

    // 设置端口验证
    Extended.setupPortValidation();

    // 填充分组选择器
    populateGroupSelector();

    // 填充基本信息
    document.getElementById('ruleAlias').value = rule.alias || '';
    document.getElementById('ruleGroupId').value = rule.groupId || '';
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

    // 获取所选分组
    const groupId = document.getElementById('ruleGroupId').value;

    const rule = {
        alias: alias,
        localType: localType,
        localPort: localPort,
        protocol: protocol,
        serverAddr: serverAddr,
        serverPort: serverPort,
        settings: settings,
        groupId: groupId || '',
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

    // 批量启动优化：分批并行处理
    const batchSize = 10; // 每批启动10个节点
    const delayBetweenBatches = 500; // 批次间延迟500ms

    addLog(`[批量启动] 开始启动 ${selectedIds.length} 个节点（每批 ${batchSize} 个）...`);

    let successCount = 0;
    let failCount = 0;

    // 分批处理
    for (let i = 0; i < selectedIds.length; i += batchSize) {
        const batch = selectedIds.slice(i, i + batchSize);
        const batchNum = Math.floor(i / batchSize) + 1;
        const totalBatches = Math.ceil(selectedIds.length / batchSize);

        addLog(`[批量启动] 处理第 ${batchNum}/${totalBatches} 批...`);

        // 并行启动当前批次的所有节点
        const results = await Promise.allSettled(
            batch.map(ruleId => MyService.StartRule(ruleId))
        );

        // 统计结果
        results.forEach((result, index) => {
            if (result.status === 'fulfilled') {
                successCount++;
            } else {
                failCount++;
                const ruleId = batch[index];
                addLog(`[错误] 节点 ${ruleId} 启动失败: ${result.reason}`);
            }
        });

        // 批次间延迟（最后一批不需要延迟）
        if (i + batchSize < selectedIds.length) {
            await new Promise(resolve => setTimeout(resolve, delayBetweenBatches));
        }
    }

    addLog(`[批量启动] 完成！成功: ${successCount}, 失败: ${failCount}`);
    await loadRules();
}

// 停止选中的规则
async function stopSelectedRules() {
    const selectedIds = getSelectedRuleIds();

    if (selectedIds.length === 0) {
        alert('请先选择要停止的规则');
        return;
    }

    // 批量停止优化：分批并行处理
    const batchSize = 10; // 每批停止10个节点
    const delayBetweenBatches = 500; // 批次间延迟500ms

    addLog(`[批量停止] 开始停止 ${selectedIds.length} 个节点（每批 ${batchSize} 个）...`);

    let successCount = 0;
    let failCount = 0;

    // 分批处理
    for (let i = 0; i < selectedIds.length; i += batchSize) {
        const batch = selectedIds.slice(i, i + batchSize);
        const batchNum = Math.floor(i / batchSize) + 1;
        const totalBatches = Math.ceil(selectedIds.length / batchSize);

        addLog(`[批量停止] 处理第 ${batchNum}/${totalBatches} 批...`);

        // 并行停止当前批次的所有节点
        const results = await Promise.allSettled(
            batch.map(ruleId => MyService.StopRule(ruleId))
        );

        // 统计结果
        results.forEach((result, index) => {
            if (result.status === 'fulfilled') {
                successCount++;
            } else {
                failCount++;
                const ruleId = batch[index];
                addLog(`[错误] 节点 ${ruleId} 停止失败: ${result.reason}`);
            }
        });

        // 批次间延迟（最后一批不需要延迟）
        if (i + batchSize < selectedIds.length) {
            await new Promise(resolve => setTimeout(resolve, delayBetweenBatches));
        }
    }

    addLog(`[批量停止] 完成！成功: ${successCount}, 失败: ${failCount}`);
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

// 导出测速报告
function exportSpeedReport() {
    // 过滤出有测速数据的规则
    const testedRules = rules.filter(rule => rule.latency > 0 || rule.downloadSpeed > 0);

    if (testedRules.length === 0) {
        alert('没有可导出的测速数据，请先进行测速');
        return;
    }

    // 生成CSV内容
    const headers = ['别名', '分组', '协议', '服务器地址', '服务器端口', '延迟(ms)', '速度(MB/s)', '测试时间', '状态'];
    const csvRows = [headers.join(',')];

    testedRules.forEach(rule => {
        const row = [
            escapeCSV(rule.alias || '-'),
            escapeCSV(rule.groupName || '无分组'),
            escapeCSV(rule.protocol || '-'),
            escapeCSV(rule.serverAddr || '-'),
            rule.serverPort || '-',
            rule.latency || 0,
            rule.downloadSpeed ? rule.downloadSpeed.toFixed(2) : 0,
            escapeCSV(rule.lastTestTime || '-'),
            escapeCSV(getSpeedStatus(rule.latency))
        ];
        csvRows.push(row.join(','));
    });

    const csvContent = csvRows.join('\n');

    // 添加BOM以支持中文
    const BOM = '\uFEFF';
    const blob = new Blob([BOM + csvContent], { type: 'text/csv;charset=utf-8;' });

    // 生成文件名（包含当前时间）
    const now = new Date();
    const timestamp = now.toISOString().replace(/[:.]/g, '-').slice(0, -5);
    const filename = `测速报告_${timestamp}.csv`;

    // 触发下载
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = filename;
    link.click();

    addLog(`[系统] 测速报告已导出: ${filename}`);
}

// CSV转义辅助函数
function escapeCSV(str) {
    if (str === null || str === undefined) return '';
    const s = String(str);
    // 如果包含逗号、引号或换行符，需要用引号包裹并转义引号
    if (s.includes(',') || s.includes('"') || s.includes('\n')) {
        return '"' + s.replace(/"/g, '""') + '"';
    }
    return s;
}

// 根据延迟获取状态描述
function getSpeedStatus(latency) {
    if (!latency || latency === 0) return '未测试';
    if (latency < 100) return '优秀';
    if (latency < 300) return '一般';
    return '较差';
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
        await Extended.loadGroups(); // 重新加载分组列表
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
window.toggleTheme = toggleTheme;