// 新功能扩展文件
import { MyService } from "./bindings/xray-manager/index.js";
import { Events } from '@wailsio/runtime';

// 全局变量
let currentGroupFilter = null; // 当前选中的分组
let allLogs = []; // 所有日志
let subscriptions = []; // 订阅列表
let groups = []; // 分组列表

// ==================== 分组管理 ====================

// 加载分组列表
export async function loadGroups() {
    try {
        groups = await MyService.GetGroups();
        renderGroupsList();
    } catch (error) {
        console.error('加载分组失败:', error);
    }
}

// 渲染分组列表
function renderGroupsList() {
    const groupsList = document.getElementById('groupsList');
    groupsList.innerHTML = '';

    // 添加"所有节点"选项
    const allItem = createGroupItem({
        id: null,
        name: '所有节点',
        description: '显示所有节点'
    }, true);
    groupsList.appendChild(allItem);

    // 添加各个分组
    groups.forEach(group => {
        const item = createGroupItem(group, false);
        groupsList.appendChild(item);
    });
}

// 创建分组列表项
function createGroupItem(group, isAll) {
    const div = document.createElement('div');
    div.className = 'group-item';
    if (currentGroupFilter === group.id || (isAll && currentGroupFilter === null)) {
        div.classList.add('active');
    }

    div.innerHTML = `
        <div class="group-info" data-group-id="${group.id || ''}">
            <span class="group-name">${escapeHtml(group.name)}</span>
            ${group.source === 'subscription' ? '<span class="group-badge">订阅</span>' : ''}
        </div>
        ${!isAll ? `
        <div class="group-actions">
            <button class="btn-icon-small btn-start-group" data-group-id="${group.id}" title="启动">▶</button>
            <button class="btn-icon-small btn-stop-group" data-group-id="${group.id}" title="停止">■</button>
            ${group.source === 'manual' ? `
            <button class="btn-icon btn-delete-group" data-group-id="${group.id}" title="删除">×</button>
            ` : ''}
        </div>
        ` : ''}
    `;

    // 点击分组切换过滤
    div.querySelector('.group-info').addEventListener('click', () => {
        currentGroupFilter = group.id;
        renderGroupsList();
        window.filterRulesByGroup && window.filterRulesByGroup(group.id);
    });

    // 启动分组按钮
    if (!isAll) {
        const startBtn = div.querySelector('.btn-start-group');
        if (startBtn) {
            startBtn.addEventListener('click', (e) => {
                e.stopPropagation();
                startGroupRules(group.id, group.name);
            });
        }

        // 停止分组按钮
        const stopBtn = div.querySelector('.btn-stop-group');
        if (stopBtn) {
            stopBtn.addEventListener('click', (e) => {
                e.stopPropagation();
                stopGroupRules(group.id, group.name);
            });
        }

        // 删除分组按钮
        if (group.source === 'manual') {
            const deleteBtn = div.querySelector('.btn-delete-group');
            if (deleteBtn) {
                deleteBtn.addEventListener('click', (e) => {
                    e.stopPropagation();
                    deleteGroup(group.id);
                });
            }
        }
    }

    return div;
}

// 打开添加分组对话框
export function openAddGroupDialog() {
    document.getElementById('groupName').value = '';
    document.getElementById('groupDescription').value = '';
    document.getElementById('addGroupDialog').style.display = 'flex';
}

// 关闭添加分组对话框
window.closeAddGroupDialog = function() {
    document.getElementById('addGroupDialog').style.display = 'none';
};

// 保存分组
window.saveGroup = async function() {
    const name = document.getElementById('groupName').value.trim();
    const description = document.getElementById('groupDescription').value.trim();

    if (!name) {
        alert('请输入分组名称');
        return;
    }

    try {
        await MyService.CreateGroup(name, description);
        closeAddGroupDialog();
        await loadGroups();
        addLog('[系统] 分组创建成功');
    } catch (error) {
        alert(`创建分组失败: ${error}`);
    }
};

// 删除分组
async function deleteGroup(groupId) {
    if (!confirm('确定要删除这个分组吗？')) {
        return;
    }

    try {
        await MyService.DeleteGroup(groupId);
        await loadGroups();
        addLog('[系统] 分组已删除');
    } catch (error) {
        alert(`删除分组失败: ${error}`);
    }
}

// 启动分组所有节点
async function startGroupRules(groupId, groupName) {
    if (!confirm(`确定要启动分组"${groupName}"的所有节点吗？`)) {
        return;
    }

    try {
        addLog(`[分组] 正在启动分组"${groupName}"的所有节点...`);
        await MyService.StartAllRulesInGroup(groupId);
        window.loadRules && await window.loadRules();
        addLog(`[分组] 分组"${groupName}"的所有节点已启动`);
    } catch (error) {
        alert(`启动分组失败: ${error}`);
        addLog(`[错误] 启动分组失败: ${error}`);
    }
}

// 停止分组所有节点
async function stopGroupRules(groupId, groupName) {
    if (!confirm(`确定要停止分组"${groupName}"的所有节点吗？`)) {
        return;
    }

    try {
        addLog(`[分组] 正在停止分组"${groupName}"的所有节点...`);
        await MyService.StopAllRulesInGroup(groupId);
        window.loadRules && await window.loadRules();
        addLog(`[分组] 分组"${groupName}"的所有节点已停止`);
    } catch (error) {
        alert(`停止分组失败: ${error}`);
        addLog(`[错误] 停止分组失败: ${error}`);
    }
}

// ==================== 订阅管理 ====================

// 打开订阅管理对话框
export async function openSubscriptionDialog() {
    document.getElementById('subscriptionDialog').style.display = 'flex';
    await loadSubscriptions();
}

// 关闭订阅管理对话框
window.closeSubscriptionDialog = function() {
    document.getElementById('subscriptionDialog').style.display = 'none';
};

// 加载订阅列表
async function loadSubscriptions() {
    try {
        subscriptions = await MyService.GetSubscriptions();
        renderSubscriptionsTable();
    } catch (error) {
        console.error('加载订阅失败:', error);
    }
}

// 渲染订阅表格
function renderSubscriptionsTable() {
    const tbody = document.getElementById('subscriptionsTableBody');
    tbody.innerHTML = '';

    if (subscriptions.length === 0) {
        tbody.innerHTML = '<tr><td colspan="7" style="text-align:center">暂无订阅</td></tr>';
        return;
    }

    subscriptions.forEach(sub => {
        const row = document.createElement('tr');
        row.innerHTML = `
            <td>${escapeHtml(sub.name)}</td>
            <td>${sub.nodeCount || 0}</td>
            <td>${sub.type || '-'}</td>
            <td style="font-size: 11px;">${sub.lastUpdate || '-'}</td>
            <td style="font-size: 11px;">${sub.nextUpdate || '-'}</td>
            <td>${sub.autoUpdate ? '✓' : '×'}</td>
            <td>
                <button class="btn-small" onclick="updateSubscription('${sub.id}')">更新</button>
                <button class="btn-small btn-danger" onclick="deleteSubscription('${sub.id}')">删除</button>
            </td>
        `;
        tbody.appendChild(row);
    });
}

// 打开添加订阅对话框
export function openAddSubscriptionDialog() {
    document.getElementById('subName').value = '';
    document.getElementById('subURL').value = '';
    document.getElementById('subAutoUpdate').checked = true;
    document.getElementById('subUpdateInterval').value = '6';
    document.getElementById('addSubscriptionDialog').style.display = 'flex';
}

// 关闭添加订阅对话框
window.closeAddSubscriptionDialog = function() {
    document.getElementById('addSubscriptionDialog').style.display = 'none';
};

// 保存订阅
window.saveSubscription = async function() {
    const name = document.getElementById('subName').value.trim();
    const url = document.getElementById('subURL').value.trim();
    const autoUpdate = document.getElementById('subAutoUpdate').checked;
    const updateInterval = parseInt(document.getElementById('subUpdateInterval').value);

    if (!name) {
        alert('请输入订阅名称');
        return;
    }

    if (!url) {
        alert('请输入订阅地址');
        return;
    }

    if (!updateInterval || updateInterval < 1) {
        alert('请输入有效的更新间隔');
        return;
    }

    try {
        addLog(`[订阅] 正在添加订阅: ${name}...`);
        await MyService.AddSubscription(name, url, autoUpdate, updateInterval);
        closeAddSubscriptionDialog();
        await loadSubscriptions();
        await loadGroups();
        window.loadRules && await window.loadRules();
        addLog('[订阅] 订阅添加成功');
    } catch (error) {
        alert(`添加订阅失败: ${error}`);
        addLog(`[错误] 添加订阅失败: ${error}`);
    }
};

// 更新订阅
window.updateSubscription = async function(subId) {
    if (!confirm('确定要更新这个订阅吗？')) {
        return;
    }

    try {
        addLog(`[订阅] 正在更新订阅...`);
        await MyService.UpdateSubscriptionByID(subId);
        await loadSubscriptions();
        window.loadRules && await window.loadRules();
        addLog('[订阅] 订阅更新成功');
    } catch (error) {
        alert(`更新订阅失败: ${error}`);
        addLog(`[错误] 更新订阅失败: ${error}`);
    }
};

// 删除订阅
window.deleteSubscription = async function(subId) {
    if (!confirm('确定要删除这个订阅吗？这将同时删除该订阅的所有节点！')) {
        return;
    }

    try {
        await MyService.DeleteSubscription(subId);
        await loadSubscriptions();
        await loadGroups();
        window.loadRules && await window.loadRules();
        addLog('[订阅] 订阅已删除');
    } catch (error) {
        alert(`删除订阅失败: ${error}`);
        addLog(`[错误] 删除订阅失败: ${error}`);
    }
};

// ==================== 测速功能 ====================

// 测试单个规则速度
export async function testRuleSpeed(ruleId) {
    try {
        await MyService.TestRuleSpeed(ruleId);
        addLog(`[测速] 正在测试节点速度...`);
    } catch (error) {
        addLog(`[错误] 测速失败: ${error}`);
    }
}

// 测试所有规则速度
export async function testAllRulesSpeed() {
    if (!confirm('确定要测试所有节点的速度吗？这可能需要较长时间。')) {
        return;
    }

    try {
        await MyService.TestAllRulesSpeed();
        addLog(`[测速] 开始批量测速，请耐心等待...`);
    } catch (error) {
        addLog(`[错误] 批量测速失败: ${error}`);
    }
}

// 监听测速结果事件
Events.On('speedTestResult', (result) => {
    const data = result.data;
    if (data.success) {
        addLog(`[测速] ${data.ruleId} - 延迟: ${data.latency}ms, 速度: ${data.downloadSpeed.toFixed(2)}MB/s`);
    } else {
        addLog(`[测速] ${data.ruleId} - 失败: ${data.error}`);
    }
});

Events.On('allSpeedTestComplete', () => {
    addLog('[测速] 批量测速完成');
});

// ==================== 端口检测 ====================

// 检查端口是否可用
export async function checkPortAvailable(port) {
    try {
        return await MyService.CheckPortAvailable(port);
    } catch (error) {
        console.error('检查端口失败:', error);
        return false;
    }
}

// 推荐可用端口
export async function recommendPort() {
    try {
        return await MyService.RecommendPort();
    } catch (error) {
        console.error('推荐端口失败:', error);
        return 10808;
    }
}

// 监听端口输入变化
export function setupPortValidation() {
    const portInput = document.getElementById('ruleLocalPort');
    const portStatus = document.getElementById('portStatus');
    const recommendBtn = document.getElementById('recommendPortBtn');

    if (portInput) {
        portInput.addEventListener('blur', async () => {
            const port = parseInt(portInput.value);
            if (port && port >= 1 && port <= 65535) {
                const available = await checkPortAvailable(port);
                if (available) {
                    portStatus.textContent = '✓ 可用';
                    portStatus.className = 'port-status available';
                } else {
                    portStatus.textContent = '× 已占用';
                    portStatus.className = 'port-status unavailable';
                }
            } else {
                portStatus.textContent = '';
            }
        });
    }

    if (recommendBtn) {
        recommendBtn.addEventListener('click', async () => {
            const port = await recommendPort();
            portInput.value = port;
            portStatus.textContent = '✓ 可用';
            portStatus.className = 'port-status available';
        });
    }
}

// ==================== 日志过滤 ====================

// 监听日志条目事件
Events.On('logEntry', (entry) => {
    const data = entry.data;
    allLogs.push(data);
    // 限制日志数量
    if (allLogs.length > 1000) {
        allLogs.shift();
    }
    updateLogDisplay();
});

// 更新日志显示
function updateLogDisplay() {
    const searchKeyword = document.getElementById('logSearchInput').value.toLowerCase();
    const levelFilter = document.getElementById('logLevelFilter').value;

    let filteredLogs = allLogs;

    // 级别过滤
    if (levelFilter !== 'ALL') {
        filteredLogs = filteredLogs.filter(log => log.level === levelFilter);
    }

    // 关键字搜索
    if (searchKeyword) {
        filteredLogs = filteredLogs.filter(log =>
            (log.message && log.message.toLowerCase().includes(searchKeyword)) ||
            (log.source && log.source.toLowerCase().includes(searchKeyword))
        );
    }

    // 显示日志
    const logTextarea = document.getElementById('logTextarea');
    logTextarea.value = filteredLogs.map(log => log.message || '').join('\n');
    logTextarea.scrollTop = logTextarea.scrollHeight;
}

// 设置日志过滤
export function setupLogFiltering() {
    const searchInput = document.getElementById('logSearchInput');
    const levelFilter = document.getElementById('logLevelFilter');

    if (searchInput) {
        searchInput.addEventListener('input', updateLogDisplay);
    }

    if (levelFilter) {
        levelFilter.addEventListener('change', updateLogDisplay);
    }
}

// 清空后端日志
export async function clearBackendLogs() {
    try {
        await MyService.ClearLogs();
        allLogs = [];
        updateLogDisplay();
    } catch (error) {
        console.error('清空日志失败:', error);
    }
}

// ==================== 辅助函数 ====================

function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function addLog(message) {
    const logTextarea = document.getElementById('logTextarea');
    logTextarea.value += message + '\n';
    logTextarea.scrollTop = logTextarea.scrollHeight;
}
document.querySelectorAll("th").forEach(th => {
    // 创建拖动条
    const handle = document.createElement("div");
    handle.className = "resize-handle";
    th.appendChild(handle);

    let startX, startWidth;

    handle.addEventListener("mousedown", function (e) {
        startX = e.pageX;
        startWidth = th.offsetWidth;

        document.addEventListener("mousemove", resizeColumn);
        document.addEventListener("mouseup", stopResize);
    });

    function resizeColumn(e) {
        const newWidth = startWidth + (e.pageX - startX);
        th.style.width = newWidth + "px";
    }

    function stopResize() {
        document.removeEventListener("mousemove", resizeColumn);
        document.removeEventListener("mouseup", stopResize);
    }
});

// 导出需要在 app.js 中使用的函数
export {
    currentGroupFilter,
    allLogs,
    subscriptions,
    groups
};
