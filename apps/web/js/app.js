/* AILS Slurm Manager - Pure Real Slurm Data Driven Application */

let currentTheme = localStorage.getItem('slurm_theme') || 'dark';
let currentUserRole = null;          // 由服务端 JWT 决定（登录后下发），客户端不再作为信任源
let authToken = localStorage.getItem('ails_token') || null;
let perfMetricsChart = null;
let partitionChart = null;

let allNodesData = [];
let allJobsData = [];
let activeNodeFilter = 'ALL';
let activeJobFilter = 'ALL';

document.addEventListener('DOMContentLoaded', () => {
  initTheme();
  initGauges(0, 0, 0, 0);
  initPartitionChart([], []);
  initPerfMetricsChart();
  installFetchAuthWrapper(); // 注入 Bearer、剥离 X-User-Role、401 处理

  // 已登录（token 可解析）→ 进入应用；否则弹出登录层
  if (authToken) {
    const info = decodeToken(authToken);
    if (info && info.role) {
      currentUserRole = info.role;
      enterApp();
      return;
    }
    authToken = null;
    localStorage.removeItem('ails_token');
  }
  showLoginOverlay();
});

/* ============ 认证桥（过渡，React 迁移后移除）============ */
/* 角色由服务端 JWT claims 权威；header 的 <select> 仅切换 UI 视图用于对比预览，
   不影响服务端鉴权（member 把视图切到 admin 仍会被服务端 403）。 */

// 解码 JWT payload（仅取 role 用于 UI；签名由服务端校验）
function decodeToken(token) {
  try {
    const p = token.split('.')[1];
    return JSON.parse(atob(p.replace(/-/g, '+').replace(/_/g, '/')));
  } catch (e) { return null; }
}

function enterApp() {
  hideLoginOverlay();
  const roleSelect = document.getElementById('user-role-select');
  if (roleSelect) roleSelect.value = currentUserRole;
  applyUserRoleUI(currentUserRole);
  startPolling();
}

function startPolling() {
  fetchSlurmStatus();
  fetchSlurmNodes();
  fetchSlurmJobs();
  fetchSlurmPartitions();
  fetchContainerWorkspaces();
  fetchBillingUsage();
  setInterval(() => {
    fetchSlurmStatus();
    fetchSlurmNodes();
    fetchSlurmJobs();
    fetchSlurmPartitions();
    fetchContainerWorkspaces();
    fetchBillingUsage();
  }, 5000);
}

async function doLogin() {
  const u = (document.getElementById('login-username') || {}).value || '';
  const p = (document.getElementById('login-password') || {}).value || '';
  const err = document.getElementById('login-error');
  if (err) err.textContent = '';
  try {
    const resp = await fetch('/api/v1/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: u, password: p, orgSlug: 'hpc-lab' })
    });
    if (!resp.ok) {
      if (err) err.textContent = '用户名或密码错误';
      return;
    }
    const data = await resp.json();
    authToken = (data && data.token) || null;
    if (!authToken) { if (err) err.textContent = '登录失败：未返回令牌'; return; }
    localStorage.setItem('ails_token', authToken);
    currentUserRole = (data.user && data.user.role) || 'member';
    enterApp();
  } catch (e) {
    if (err) err.textContent = '登录请求失败：' + e.message;
  }
}

function logout() {
  authToken = null;
  localStorage.removeItem('ails_token');
  showLoginOverlay();
}

function showLoginOverlay() {
  const ov = document.getElementById('auth-overlay');
  if (ov) ov.style.display = 'flex';
}
function hideLoginOverlay() {
  const ov = document.getElementById('auth-overlay');
  if (ov) ov.style.display = 'none';
}

// 401：清 token、重弹登录（幂等，避免循环）
function handleSessionExpired() {
  if (authToken === null) return;
  authToken = null;
  localStorage.removeItem('ails_token');
  showLoginOverlay();
  showToast('会话已过期，请重新登录', 'warn');
}

// 全局 fetch 包装：自动注入 Authorization、剥离历史可伪造的 X-User-Role 头、401 处理。
// 这样无需逐个改 14 个 fetch 调用点与 7 处 X-User-Role 站点。
function installFetchAuthWrapper() {
  const _origFetch = window.fetch;
  window.fetch = function (input, init) {
    init = init || {};
    const headers = new Headers(init.headers || {});
    if (authToken && !headers.has('Authorization')) {
      headers.set('Authorization', 'Bearer ' + authToken);
    }
    headers.delete('X-User-Role'); // 历史可伪造头一律剥离
    init.headers = headers;
    const url = typeof input === 'string' ? input : ((input && input.url) || '');
    return _origFetch.call(this, input, init).then(resp => {
      if (resp.status === 401 && !url.includes('/auth/login')) handleSessionExpired();
      return resp;
    });
  };
}

/* 四级 RBAC 视角切换 —— 仅切换 UI 视图用于对比预览。
   不再写 localStorage、不作信任源；实际鉴权以服务端 JWT claims 为权威。 */
function switchUserRole(role) {
  currentUserRole = role;
  applyUserRoleUI(role);
  showToast(`已切换视图（仅预览，权限以服务端为准）：${getRoleDisplayName(role)}`, 'info');
  renderDedicatedNodesTable();
  renderDedicatedJobsTable();
}

function getRoleDisplayName(role) {
  switch (role) {
    case 'admin': return 'System Admin (超级管理员)';
    case 'ops_admin': return 'Operations Admin (运营管理员)';
    case 'tenant_admin': return 'Tenant Admin (租户管理员)';
    case 'member': return 'Tenant Member (普通租户)';
    default: return role;
  }
}

function applyUserRoleUI(role) {
  const displayEl = document.getElementById('user-display-name');
  const titleEl = document.getElementById('current-view-title');
  const sysAdminView = document.getElementById('view-system-admin');
  const memberView = document.getElementById('view-tenant-member');
  const tenantAdminView = document.getElementById('view-tenant-admin');
  const opsAdminView = document.getElementById('view-ops-admin');

  if (displayEl) displayEl.textContent = `${getRoleDisplayName(role)} ▾`;

  // Hide all role root views
  if (sysAdminView) sysAdminView.style.display = 'none';
  if (memberView) memberView.style.display = 'none';
  if (tenantAdminView) tenantAdminView.style.display = 'none';
  if (opsAdminView) opsAdminView.style.display = 'none';

  if (role === 'admin') {
    if (sysAdminView) sysAdminView.style.display = 'flex';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | System Admin Monitoring';
  } else if (role === 'member') {
    if (memberView) memberView.style.display = 'flex';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Personal HPC Workspace';
  } else if (role === 'tenant_admin') {
    if (memberView) memberView.style.display = 'flex';
    if (tenantAdminView) tenantAdminView.style.display = 'flex';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Tenant Org Management';
  } else if (role === 'ops_admin') {
    if (opsAdminView) opsAdminView.style.display = 'flex';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Executive Operations Dashboard';
    fetchOpsAnalyticsData();
  }
}

/* 主题无缝切换 */
function initTheme() {
  document.documentElement.setAttribute('data-theme', currentTheme);
  const themeBtn = document.getElementById('theme-btn');
  if (themeBtn) {
    themeBtn.addEventListener('click', () => {
      currentTheme = currentTheme === 'dark' ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', currentTheme);
      localStorage.setItem('slurm_theme', currentTheme);
      updateChartsTheme();
    });
  }
}

/* Login Node Portal 登录节点连接控件 */
function openLoginNodeModal() {
  const modal = document.getElementById('login-node-modal');
  if (modal) modal.style.display = 'flex';
}

function closeLoginNodeModal() {
  const modal = document.getElementById('login-node-modal');
  if (modal) modal.style.display = 'none';
}

function copySSHCommand() {
  const cmd = document.getElementById('ssh-command-text')?.textContent || 'ssh hpcuser@192.168.20.226';
  navigator.clipboard.writeText(cmd).then(() => {
    showToast('Copied SSH login command to clipboard!', 'success');
  }).catch(() => {
    showToast(`Command: ${cmd}`, 'info');
  });
}

function downloadSSHPrivateKey() {
  const dummyKey = `-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0SlurmHPCKeyForTenantAccess...\n-----END RSA PRIVATE KEY-----\n`;
  const blob = new Blob([dummyKey], { type: 'application/x-pem-file' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'slurm-hpcuser-id_rsa.pem';
  a.click();
  URL.revokeObjectURL(url);
  showToast('Downloaded Tenant SSH Private Key (slurm-hpcuser-id_rsa.pem)', 'success');
}
function stepInput(id, delta) {
  const el = document.getElementById(id);
  if (!el) return;
  const min = el.hasAttribute('min') ? parseFloat(el.getAttribute('min')) : -Infinity;
  const max = el.hasAttribute('max') ? parseFloat(el.getAttribute('max')) : Infinity;
  let val = (parseFloat(el.value) || 0) + delta;
  if (val < min) val = min;
  if (val > max) val = max;
  el.value = val;
}

function showToast(msg, type = 'info') {
  const container = document.getElementById('toast-container');
  if (!container) return;
  const toast = document.createElement('div');
  toast.className = `neu-toast ${type}`;
  toast.textContent = msg;
  container.appendChild(toast);
  setTimeout(() => {
    toast.remove();
  }, 3500);
}

function showConfirm(title, message, onOk) {
  const modal = document.getElementById('neu-confirm-modal');
  const titleEl = document.getElementById('confirm-modal-title');
  const msgEl = document.getElementById('confirm-modal-message');
  const okBtn = document.getElementById('confirm-modal-ok-btn');

  if (!modal || !titleEl || !msgEl || !okBtn) return;

  titleEl.textContent = title;
  msgEl.textContent = message;

  const newOkBtn = okBtn.cloneNode(true);
  okBtn.parentNode.replaceChild(newOkBtn, okBtn);

  newOkBtn.addEventListener('click', () => {
    closeConfirmModal();
    if (typeof onOk === 'function') onOk();
  });

  modal.style.display = 'flex';
}

function closeConfirmModal() {
  const modal = document.getElementById('neu-confirm-modal');
  if (modal) modal.style.display = 'none';
}

/* 绘制发光弧形 Gauge 仪表 */
function drawNeonGaugeArc(canvasId, valuePercent, colorStr) {
  const canvas = document.getElementById(canvasId);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const w = canvas.width = canvas.parentElement.clientWidth;
  const h = canvas.height = canvas.parentElement.clientHeight;

  ctx.clearRect(0, 0, w, h);
  
  const cx = w / 2;
  const cy = h - 4;
  const radius = Math.min(cx, cy) - 6;
  
  ctx.beginPath();
  ctx.arc(cx, cy, radius, Math.PI, 2 * Math.PI, false);
  ctx.lineWidth = 7;
  ctx.strokeStyle = currentTheme === 'dark' ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.06)';
  ctx.stroke();

  const safePercent = Math.min(100, Math.max(0, valuePercent));
  const endAngle = Math.PI + (safePercent / 100) * Math.PI;
  ctx.beginPath();
  ctx.arc(cx, cy, radius, Math.PI, endAngle, false);
  ctx.lineWidth = 7;
  ctx.strokeStyle = colorStr;
  ctx.shadowColor = colorStr;
  ctx.shadowBlur = 10;
  ctx.lineCap = 'round';
  ctx.stroke();
  ctx.shadowBlur = 0;
}

function initGauges(nodesPct = 100, jobsPct = 0, cpuPct = 0, memPct = 0) {
  const cyan = '#06B6D4';
  const emerald = '#10B981';

  drawNeonGaugeArc('gaugeNodes', nodesPct, emerald);
  drawNeonGaugeArc('gaugeJobs', jobsPct, cyan);
  drawNeonGaugeArc('gaugeCPU', cpuPct, cyan);
  drawNeonGaugeArc('gaugeMem', memPct, cyan);
}

/* Partition 真实饼图渲染 */
function initPartitionChart(labels = ['debug'], dataVals = [100]) {
  const canvas = document.getElementById('partitionChart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');

  if (partitionChart) {
    partitionChart.destroy();
  }

  partitionChart = new Chart(ctx, {
    type: 'doughnut',
    data: {
      labels: labels.length > 0 ? labels : ['default'],
      datasets: [{
        data: dataVals.length > 0 ? dataVals : [100],
        backgroundColor: ['#06B6D4', '#10B981', '#F43F5E', '#6366F1', '#F59E0B'],
        borderWidth: 0,
        hoverOffset: 4
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      cutout: '70%',
      plugins: {
        legend: {
          position: 'right',
          labels: {
            color: currentTheme === 'dark' ? '#94A3B8' : '#64748B',
            boxWidth: 10,
            font: { size: 10 }
          }
        }
      }
    }
  });
}

/* Performance Metrics Curve */
function initPerfMetricsChart() {
  const canvas = document.getElementById('perfMetricsChart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const isDark = currentTheme === 'dark';

  if (perfMetricsChart) return;

  perfMetricsChart = new Chart(ctx, {
    type: 'line',
    data: {
      labels: ['1m', '2m', '3m', '4m', '5m', 'Live'],
      datasets: [
        {
          label: 'CPU',
          data: [0, 0, 0, 0, 0, 0],
          borderColor: '#06B6D4',
          borderWidth: 2.5,
          tension: 0.4,
          pointRadius: 0
        },
        {
          label: 'Memory',
          data: [0, 0, 0, 0, 0, 0],
          borderColor: '#10B981',
          borderWidth: 2,
          tension: 0.4,
          pointRadius: 0
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { display: false } },
      scales: {
        x: { grid: { display: false }, ticks: { color: isDark ? '#64748B' : '#94A3B8', font: { size: 9 } } },
        y: { min: 0, max: 100, grid: { color: isDark ? 'rgba(255,255,255,0.03)' : 'rgba(0,0,0,0.03)' }, ticks: { color: isDark ? '#64748B' : '#94A3B8', font: { size: 9 } } }
      }
    }
  });
}

function updateChartsTheme() {
  if (partitionChart) {
    partitionChart.options.plugins.legend.labels.color = currentTheme === 'dark' ? '#94A3B8' : '#64748B';
    partitionChart.update();
  }
  if (perfMetricsChart) {
    const isDark = currentTheme === 'dark';
    perfMetricsChart.options.scales.x.ticks.color = isDark ? '#64748B' : '#94A3B8';
    perfMetricsChart.options.scales.y.ticks.color = isDark ? '#64748B' : '#94A3B8';
    perfMetricsChart.options.scales.y.grid.color = isDark ? 'rgba(255,255,255,0.03)' : 'rgba(0,0,0,0.03)';
    perfMetricsChart.update();
  }
}

/* 1. Slurm Controller 状态轮询 */
async function fetchSlurmStatus() {
  const led = document.getElementById('slurm-led');
  const val = document.getElementById('slurm-status-val');

  try {
    const res = await fetch('/api/v1/slurm/ping');
    if (res.ok) {
      const data = await res.json();
      if (data.pings && data.pings.length > 0 && data.pings[0].ping === 'UP') {
        if (led) led.style.backgroundColor = 'var(--accent-emerald)';
        if (val) {
          val.textContent = 'OPERATIONAL';
          val.style.color = 'var(--accent-emerald)';
        }
      }
    }
  } catch (e) {
    if (led) led.style.backgroundColor = 'var(--accent-rose)';
    if (val) {
      val.textContent = 'OFFLINE';
      val.style.color = 'var(--accent-rose)';
    }
  }
}

/* 2. 真实 Slurm 计算节点 (Compute Nodes) 数据计算与矩阵渲染 */
async function fetchSlurmNodes() {
  const matrix = document.getElementById('node-matrix-container');
  const nodesNum = document.getElementById('active-nodes-num');
  const nodesSub = document.getElementById('active-nodes-sub');
  const cpuNum = document.getElementById('cpu-load-num');
  const cpuSub = document.getElementById('cpu-load-sub');
  const memNum = document.getElementById('mem-load-num');
  const memSub = document.getElementById('mem-load-sub');

  try {
    const res = await fetch('/api/v1/slurm/nodes');
    if (res.ok) {
      const data = await res.json();
      allNodesData = data.nodes || [];

      if (matrix) {
        const total = allNodesData.length;
        let activeCount = 0;
        let totalCpus = 0, totalAllocCpus = 0;
        let totalMem = 0, totalAllocMem = 0;

        matrix.innerHTML = '';
        allNodesData.forEach(n => {
          const stateStr = (n.state || 'idle').toLowerCase();
          const isUp = !stateStr.includes('down') && !stateStr.includes('drain');
          if (isUp) activeCount++;
          const cpusCount = n.cpus || 64;
          totalCpus += cpusCount;
          totalAllocCpus += (n.alloc_cpus || 0);
          totalMem += (n.real_memory || 0);
          totalAllocMem += (n.alloc_memory || 0);

          let chipClass = 'idle';
          if (stateStr.includes('alloc') || stateStr.includes('busy')) chipClass = 'amber';
          if (!isUp) chipClass = 'rose';

          const chip = document.createElement('div');
          chip.className = `node-small-chip ${chipClass}`;
          chip.dataset.tooltip = `Node: ${n.name} (${n.state})`;

          if (currentUserRole === 'admin') {
            chip.onclick = function() {
              document.querySelectorAll('.node-small-chip').forEach(c => c.classList.remove('selected'));
              chip.classList.add('selected');
              openNodeModal(n.name, n.state, cpusCount, n.real_memory || 3000);
            };
          }

          chip.innerHTML = `
            <div style="display:flex; align-items:center; gap:5px;">
              <div class="dot"></div>
              <span style="font-size:11px; font-weight:800; color:var(--text-primary);">${n.name}</span>
            </div>
            <div style="font-size:9px; color:var(--text-dim); text-transform:uppercase;">${n.state}</div>
          `;
          matrix.appendChild(chip);
        });

        const activePct = total > 0 ? Math.round((activeCount / total) * 100) : 0;
        if (nodesNum) nodesNum.textContent = `${activeCount} / ${total}`;
        if (nodesSub) nodesSub.textContent = `+${activePct}% Operational`;

        // 真实利用率：Σalloc / Σtotal（数据来自 slurmrestd /nodes 的 alloc_cpus/alloc_memory）
        const cpuPct = totalCpus > 0 ? Math.round((totalAllocCpus / totalCpus) * 100) : 0;
        const memPct = totalMem > 0 ? Math.round((totalAllocMem / totalMem) * 100) : 0;
        if (cpuNum) cpuNum.textContent = `${cpuPct}%`;
        if (cpuSub) cpuSub.textContent = `${totalAllocCpus}/${totalCpus} cores allocated`;
        if (memNum) memNum.textContent = `${memPct}%`;
        if (memSub) memSub.textContent = `${totalAllocMem}/${totalMem} MB allocated`;

        initGauges(activePct, 0, cpuPct, memPct);

        if (perfMetricsChart) {
          const cpuSeries = perfMetricsChart.data.datasets[0].data;
          cpuSeries.shift();
          cpuSeries.push(cpuPct);
          if (perfMetricsChart.data.datasets[1]) {
            const memSeries = perfMetricsChart.data.datasets[1].data;
            memSeries.shift();
            memSeries.push(memPct);
          }
          perfMetricsChart.update();
        }
      }

      renderDedicatedNodesTable();
    }
  } catch (e) {
    console.error('Fetch nodes error:', e);
  }
}

/* 渲染 Nodes 专属全功能页面表格 (sinfo/scontrol) */
function renderDedicatedNodesTable() {
  const tbody = document.getElementById('dedicated-nodes-tbody');
  const summaryCount = document.getElementById('nodes-summary-count');
  if (!tbody) return;

  let filtered = allNodesData;
  if (activeNodeFilter !== 'ALL') {
    filtered = allNodesData.filter(n => {
      const st = (n.state || '').toUpperCase();
      if (activeNodeFilter === 'IDLE') return st.includes('IDLE');
      if (activeNodeFilter === 'ALLOCATED') return st.includes('ALLOC');
      if (activeNodeFilter === 'DRAIN') return st.includes('DRAIN') || st.includes('DOWN');
      return true;
    });
  }

  if (summaryCount) summaryCount.textContent = `Showing ${filtered.length} of ${allNodesData.length} Nodes`;

  if (filtered.length > 0) {
    tbody.innerHTML = '';
    filtered.forEach(n => {
      const tr = document.createElement('tr');
      const st = (n.state || 'idle').toUpperCase();
      let pillClass = 'running';
      if (st.includes('DRAIN') || st.includes('DOWN')) pillClass = 'cancelled';
      else if (st.includes('ALLOC')) pillClass = 'pending';

      const actionBtn = currentUserRole === 'admin' ? `
        <button class="neu-btn primary" style="font-size:9px; padding:3px 6px;" onclick="openNodeModal('${n.name}', '${n.state}', ${n.cpus || 64}, ${n.real_memory || 3000})">Manage Node</button>
      ` : `<span style="font-size:10px; color:var(--text-dim);">-</span>`;

      tr.innerHTML = `
        <td><strong>${n.name}</strong></td>
        <td><span class="status-pill ${pillClass}">${st}</span></td>
        <td>${n.cpus || 64} Cores</td>
        <td>${n.real_memory || 3000} MB</td>
        <td><span style="color:var(--accent-cyan); font-weight:700;">${n.partitions || 'debug'}</span></td>
        <td><span style="font-size:11px; color:var(--text-dim);">${n.reason || 'None'}</span></td>
        <td>${actionBtn}</td>
      `;
      tbody.appendChild(tr);
    });
  } else {
    tbody.innerHTML = `<tr><td colspan="7" style="text-align:center; color:var(--text-dim); padding: 20px;">No Nodes matching filter criteria</td></tr>`;
  }
}

function filterNodesView(filterType, btnEl) {
  activeNodeFilter = filterType;
  if (btnEl) {
    btnEl.parentElement.querySelectorAll('.filter-pill-btn').forEach(b => b.classList.remove('active'));
    btnEl.classList.add('active');
  }
  renderDedicatedNodesTable();
}

function openNodeModal(name, state, cpus, memory) {
  const modal = document.getElementById('node-control-modal');
  const nameEl = document.getElementById('node-modal-name');
  const stateEl = document.getElementById('node-modal-state');
  const specsEl = document.getElementById('node-modal-specs');
  const reasonInput = document.getElementById('node-modal-reason');
  const drainBtn = document.getElementById('node-modal-drain-btn');
  const resumeBtn = document.getElementById('node-modal-resume-btn');

  if (!modal) return;

  if (nameEl) nameEl.textContent = name;
  if (stateEl) stateEl.textContent = state.toUpperCase();
  if (specsEl) specsEl.textContent = `${cpus} Cores / ${memory} MB`;
  if (reasonInput) reasonInput.value = '';

  if (drainBtn) {
    drainBtn.onclick = function() {
      const reasonVal = (reasonInput?.value || '').trim() || 'Manual Maintenance Drain';
      closeNodeModal();
      toggleNodeState(name, 'DRAIN', reasonVal);
    };
  }
  if (resumeBtn) {
    resumeBtn.onclick = function() {
      closeNodeModal();
      toggleNodeState(name, 'RESUME', 'Node Resumed Operational');
    };
  }

  modal.style.display = 'flex';
}

function closeNodeModal() {
  const modal = document.getElementById('node-control-modal');
  if (modal) modal.style.display = 'none';
  document.querySelectorAll('.node-small-chip').forEach(c => c.classList.remove('selected'));
}

async function toggleNodeState(name, targetState, reasonStr = '') {
  showConfirm('Node State Change', `Confirm set node ${name} state to ${targetState}?`, async () => {
    try {
      const res = await fetch(`/api/v1/slurm/nodes/${name}/state`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-User-Role': currentUserRole
        },
        body: JSON.stringify({ state: targetState, reason: reasonStr })
      });
      const data = await res.json();
      if (res.ok) {
        showToast(`Node ${name} state updated to ${targetState}`, 'success');
        fetchSlurmNodes();
      } else {
        showToast(`Update failed: ${data.error || 'Permission denied'}`, 'error');
      }
    } catch (err) {
      showToast(`Network error: ${err.message}`, 'error');
    }
  });
}

/* 3. 真实 Slurm 作业 (Jobs) 动态轮询与正式状态解析 */
async function fetchSlurmJobs() {
  const tbody = document.getElementById('job-table-body');
  const jobsNum = document.getElementById('active-jobs-num');
  const jobsSub = document.getElementById('active-jobs-sub');
  const topJobsContainer = document.getElementById('top-jobs-container');
  if (!tbody) return;

  try {
    const res = await fetch('/api/v1/slurm/jobs');
    if (res.ok) {
      const data = await res.json();
      allJobsData = data.jobs || [];

      if (jobsNum) jobsNum.textContent = `${allJobsData.length}`;
      if (jobsSub) jobsSub.textContent = `${allJobsData.length} Active Queue`;

      if (allJobsData.length > 0) {
        tbody.innerHTML = '';
        if (topJobsContainer) topJobsContainer.innerHTML = '';

        allJobsData.forEach(job => {
          const rawState = (job.job_state || 'PENDING').toUpperCase();
          let pillClass = 'pending';
          let statusLabel = rawState;

          if (rawState.includes('RUNNING')) {
            pillClass = 'running';
            statusLabel = 'RUNNING';
          } else if (rawState.includes('COMPLETED')) {
            pillClass = 'completed';
            statusLabel = 'COMPLETED';
          } else if (rawState.includes('CANCELLED') || rawState.includes('FAILED')) {
            pillClass = 'cancelled';
            statusLabel = rawState.includes('CANCELLED') ? 'CANCELLED' : 'FAILED';
          } else if (rawState.includes('HELD')) {
            pillClass = 'held';
            statusLabel = 'HELD';
          } else if (rawState.includes('PENDING')) {
            pillClass = 'pending';
            statusLabel = 'PENDING';
          }

          const isTerminated = statusLabel === 'COMPLETED' || statusLabel === 'CANCELLED' || statusLabel === 'FAILED';
          
          let actionsHTML = `<span style="font-size:10px; color:var(--text-dim);">-</span>`;
          if (!isTerminated && (currentUserRole === 'admin' || currentUserRole === 'tenant_admin' || currentUserRole === 'member')) {
            actionsHTML = `
              <div class="action-btn-group">
                <button class="neu-btn" style="font-size:8px; padding:2px 4px;" data-tooltip="Hold Job" onclick="holdJob(${job.job_id})">Hold</button>
                <button class="neu-btn emerald" style="font-size:8px; padding:2px 4px;" data-tooltip="Requeue Job" onclick="requeueJob(${job.job_id})">Requeue</button>
                <button class="neu-btn rose" style="font-size:8px; padding:2px 4px;" data-tooltip="Cancel Job" onclick="cancelJob(${job.job_id})">Cancel</button>
              </div>
            `;
          }

          const tr = document.createElement('tr');
          tr.innerHTML = `
            <td><strong>#${job.job_id}</strong></td>
            <td>${job.name || 'Job'}</td>
            <td>${job.partition || 'debug'}</td>
            <td>${job.nodes || '1'}</td>
            <td><span class="status-pill ${pillClass}">${statusLabel}</span></td>
          `;
          tbody.appendChild(tr);

          // Populate Tenant Member View
          const memberTbody = document.getElementById('member-jobs-tbody');
          if (memberTbody) {
            if (memberTbody.querySelector('td[colspan]')) memberTbody.innerHTML = '';
            const mtr = document.createElement('tr');
            mtr.innerHTML = `
              <td><strong>#${job.job_id}</strong></td>
              <td>${job.name || 'Job'}</td>
              <td><span class="status-pill ${pillClass}">${statusLabel}</span></td>
              <td>
                <button class="neu-btn rose" style="font-size:8px; padding:2px 4px;" onclick="cancelJob(${job.job_id})">Cancel</button>
              </td>
            `;
            memberTbody.appendChild(mtr);
          }

          if (topJobsContainer && statusLabel === 'RUNNING') {
            const item = document.createElement('div');
            item.className = 'top-job-item';
            item.innerHTML = `
              <div class="top-job-row"><span>Job #${job.job_id} (${job.name})</span><strong>RUNNING</strong></div>
              <div class="progress-track"><div class="progress-fill" style="width:100%;"></div></div>
            `;
            topJobsContainer.appendChild(item);
          }
        });

        if (topJobsContainer && topJobsContainer.children.length === 0) {
          topJobsContainer.innerHTML = `<div style="color:var(--text-dim); font-size:11px; text-align:center; padding: 20px;">No Heavy Jobs Running</div>`;
        }
      } else {
        tbody.innerHTML = `<tr><td colspan="6" style="text-align:center; color:var(--text-dim); padding: 20px;">No Active Jobs in Queue</td></tr>`;
        if (topJobsContainer) {
          topJobsContainer.innerHTML = `<div style="color:var(--text-dim); font-size:11px; text-align:center; padding: 20px;">No Heavy Jobs Running</div>`;
        }
      }

      renderDedicatedJobsTable();
    }
  } catch (e) {
    console.error('Fetch jobs error:', e);
  }
}

/* 渲染 Jobs 专属全功能页面表格 (squeue/sbatch) */
function renderDedicatedJobsTable() {
  const tbody = document.getElementById('dedicated-jobs-tbody');
  if (!tbody) return;

  let filtered = allJobsData;
  if (activeJobFilter !== 'ALL') {
    filtered = allJobsData.filter(j => {
      const st = (j.job_state || '').toUpperCase();
      return st.includes(activeJobFilter);
    });
  }

  if (filtered.length > 0) {
    tbody.innerHTML = '';
    filtered.forEach(j => {
      const rawState = (j.job_state || 'PENDING').toUpperCase();
      let pillClass = 'pending';
      if (rawState.includes('RUNNING')) pillClass = 'running';
      else if (rawState.includes('COMPLETED')) pillClass = 'completed';
      else if (rawState.includes('CANCELLED') || rawState.includes('FAILED')) pillClass = 'cancelled';
      else if (rawState.includes('HELD')) pillClass = 'held';

      const isTerminated = rawState.includes('COMPLETED') || rawState.includes('CANCELLED') || rawState.includes('FAILED');
      const submitTimeStr = j.submit_time ? new Date(j.submit_time * 1000).toLocaleTimeString() : 'Recent';
      
      const actionsHTML = isTerminated ? `<span style="font-size:10px; color:var(--text-dim);">-</span>` : `
        <div class="action-btn-group">
          <button class="neu-btn" style="font-size:9px; padding:3px 6px;" onclick="holdJob(${j.job_id})">Hold</button>
          <button class="neu-btn emerald" style="font-size:9px; padding:3px 6px;" onclick="requeueJob(${j.job_id})">Requeue</button>
          <button class="neu-btn rose" style="font-size:9px; padding:3px 6px;" onclick="cancelJob(${j.job_id})">Cancel</button>
        </div>
      `;

      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td><strong>#${j.job_id}</strong></td>
        <td>${j.name || 'Job'}</td>
        <td><span style="color:var(--accent-cyan); font-weight:700;">${j.partition || 'debug'}</span></td>
        <td>${j.nodes || 1} Nodes</td>
        <td><span style="font-size:11px; color:var(--text-dim);">${submitTimeStr}</span></td>
        <td><span class="status-pill ${pillClass}">${rawState}</span></td>
        <td>${actionsHTML}</td>
      `;
      tbody.appendChild(tr);
    });
  } else {
    tbody.innerHTML = `<tr><td colspan="7" style="text-align:center; color:var(--text-dim); padding: 20px;">No Jobs matching filter criteria</td></tr>`;
  }
}

function filterJobsView(filterType, btnEl) {
  activeJobFilter = filterType;
  if (btnEl) {
    btnEl.parentElement.querySelectorAll('.filter-pill-btn').forEach(b => b.classList.remove('active'));
    btnEl.classList.add('active');
  }
  renderDedicatedJobsTable();
}

function loadJobScriptTemplate(type) {
  const textarea = document.getElementById('job-script-input');
  if (!textarea) return;

  if (type === 'bash') {
    textarea.value = `#!/bin/bash\n#SBATCH --job-name=basic_shell\n#SBATCH --partition=debug\n#SBATCH --nodes=1\n\necho "Running Basic Shell Task on Host: $(hostname)"\nsrun sleep 10\necho "Job Complete"`;
  } else if (type === 'mpi') {
    textarea.value = `#!/bin/bash\n#SBATCH --job-name=openmpi_calc\n#SBATCH --partition=debug\n#SBATCH --nodes=2\n#SBATCH --ntasks=4\n\nmodule load openmpi/4.1.1\necho "Executing Parallel MPI Compute Across Cluster Nodes"\nmpirun -np 4 hostname`;
  } else if (type === 'python') {
    textarea.value = `#!/bin/bash\n#SBATCH --job-name=python_model_train\n#SBATCH --partition=debug\n#SBATCH --nodes=1\n#SBATCH --cpus-per-task=4\n\nsrun python3 -c "import time; print('Initializing PyTorch Compute...'); time.sleep(5); print('Training Complete!')"`;
  }
  showToast(`Loaded sbatch template: ${type.toUpperCase()}`, 'info');
}

/* 4. 真实 Slurm 分区 (Partitions) 轮询与渲染 */
async function fetchSlurmPartitions() {
  try {
    const res = await fetch('/api/v1/slurm/partitions');
    if (res.ok) {
      const data = await res.json();
      if (data.partitions && data.partitions.length > 0) {
        const labels = data.partitions.map(p => p.name || 'debug');
        const vals = data.partitions.map(p => p.total_nodes || 1);
        initPartitionChart(labels, vals);
      }
    }
  } catch (e) {
    console.error('Fetch partitions error:', e);
  }
}

/* 5. 作业提交与控制 Modal & API 交互 */
function openSubmitModal() {
  const modal = document.getElementById('submit-job-modal');
  const errBox = document.getElementById('job-submit-error-box');
  if (errBox) errBox.style.display = 'none';
  if (modal) modal.style.display = 'flex';
}

function closeSubmitModal() {
  const modal = document.getElementById('submit-job-modal');
  const errBox = document.getElementById('job-submit-error-box');
  if (errBox) errBox.style.display = 'none';
  if (modal) modal.style.display = 'none';
}

async function handleJobSubmit(e) {
  e.preventDefault();
  const name = (document.getElementById('job-name-input').value || '').trim();
  const partition = (document.getElementById('job-partition-input').value || 'debug').trim();
  const nodes = parseInt(document.getElementById('job-nodes-input').value || '1', 10);
  const tasks = parseInt(document.getElementById('job-tasks-input').value || '1', 10);
  const timeLimitVal = document.getElementById('job-timelimit-input').value;
  const timeLimit = parseInt(timeLimitVal || '3600', 10);
  const script = document.getElementById('job-script-input').value;
  const errBox = document.getElementById('job-submit-error-box');

  if (errBox) errBox.style.display = 'none';

  if (!name) {
    const msg = 'Please specify a valid Job Name.';
    if (errBox) { errBox.textContent = msg; errBox.style.display = 'flex'; }
    showToast(msg, 'error');
    return;
  }

  if (isNaN(timeLimit) || timeLimit <= 0) {
    const msg = 'Time Limit must be a positive number of seconds.';
    if (errBox) { errBox.textContent = msg; errBox.style.display = 'flex'; }
    showToast(msg, 'error');
    return;
  }

  try {
    const res = await fetch('/api/v1/slurm/jobs/submit', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-User-Role': currentUserRole
      },
      body: JSON.stringify({
        name: name,
        partition: partition,
        nodes: nodes,
        tasks: tasks,
        time_limit: String(timeLimit),
        script: script
      })
    });
    const data = await res.json();
    if (res.ok) {
      showToast(`Job submitted successfully! Job ID: #${data.job_id}`, 'success');
      closeSubmitModal();
      fetchSlurmJobs();
    } else {
      const errMsg = `Submission Failed: ${data.error || data.message || 'Unknown SlurmREST error'}`;
      if (errBox) {
        errBox.textContent = errMsg;
        errBox.style.display = 'flex';
      }
      showToast(errMsg, 'error');
    }
  } catch (err) {
    const errMsg = `Network Error: ${err.message}`;
    if (errBox) {
      errBox.textContent = errMsg;
      errBox.style.display = 'flex';
    }
    showToast(errMsg, 'error');
  }
}

function cancelJob(jobId) {
  showConfirm('Cancel Job', `Are you sure you want to terminate Job #${jobId}?`, async () => {
    try {
      const res = await fetch(`/api/v1/slurm/jobs/${jobId}/cancel`, {
        method: 'POST',
        headers: { 'X-User-Role': currentUserRole }
      });
      const data = await res.json();
      if (res.ok) {
        showToast(`Job #${jobId} cancelled`, 'success');
        fetchSlurmJobs();
      } else {
        showToast(`Cancel failed: ${data.error || 'Unknown error'}`, 'error');
      }
    } catch (err) {
      showToast(`Network error: ${err.message}`, 'error');
    }
  });
}

function holdJob(jobId) {
  showConfirm('Hold Job', `Are you sure you want to hold Job #${jobId}?`, async () => {
    try {
      const res = await fetch(`/api/v1/slurm/jobs/${jobId}/hold`, {
        method: 'POST',
        headers: { 'X-User-Role': currentUserRole }
      });
      const data = await res.json();
      if (res.ok) {
        showToast(`Job #${jobId} held`, 'success');
        fetchSlurmJobs();
      } else {
        showToast(`Hold failed: ${data.error || 'Unknown error'}`, 'error');
      }
    } catch (err) {
      showToast(`Network error: ${err.message}`, 'error');
    }
  });
}

function requeueJob(jobId) {
  showConfirm('Requeue Job', `Are you sure you want to requeue Job #${jobId}?`, async () => {
    try {
      const res = await fetch(`/api/v1/slurm/jobs/${jobId}/requeue`, {
        method: 'POST',
        headers: { 'X-User-Role': currentUserRole }
      });
      const data = await res.json();
      if (res.ok) {
        showToast(`Job #${jobId} requeued`, 'success');
        fetchSlurmJobs();
      } else {
        showToast(`Requeue failed: ${data.error || 'Unknown error'}`, 'error');
      }
    } catch (err) {
      showToast(`Network error: ${err.message}`, 'error');
    }
  });
}

/* 6. Billing & Quota Accounting */
let billingChart = null;

async function fetchBillingUsage(user = 'hpcuser', project = 'default') {
  try {
    const res = await fetch(`/api/v1/slurm/billing/usage?user=${user}&project=${project}`);
    if (res.ok) {
      const data = await res.json();
      const cpuEl = document.getElementById('billing-cpu-hours');
      const memEl = document.getElementById('billing-mem-hours');
      const costEl = document.getElementById('billing-total-cost');

      if (cpuEl) cpuEl.textContent = `${(data.total_cpu_hours || 0).toFixed(2)} hrs`;
      if (memEl) memEl.textContent = `${(data.total_memory_gb_hours || 0).toFixed(2)} GB·h`;
      
      const cost = ((data.total_cpu_hours || 0) * 0.5) + ((data.total_memory_gb_hours || 0) * 0.1);
      if (costEl) costEl.textContent = `¥${cost.toFixed(2)}`;

      updateBillingChart([data.total_cpu_hours || 0, data.total_memory_gb_hours || 0]);
    }
  } catch (e) {
    console.error('Fetch billing usage error:', e);
  }
}

function updateBillingChart(dataSeries) {
  const canvas = document.getElementById('billingChart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');

  if (billingChart) {
    billingChart.destroy();
  }

  billingChart = new Chart(ctx, {
    type: 'bar',
    data: {
      labels: ['CPU Cores Hours', 'Memory (GB·h)'],
      datasets: [{
        label: 'Resource Consumption',
        data: dataSeries,
        backgroundColor: ['#06B6D4', '#10B981'],
        borderRadius: 4
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { display: false } },
      scales: {
        x: { ticks: { color: currentTheme === 'dark' ? '#94A3B8' : '#64748B' } },
        y: { ticks: { color: currentTheme === 'dark' ? '#94A3B8' : '#64748B' } }
      }
    }
  });
}

async function exportBilling(format = 'json') {
  if (format === 'chart') {
    if (!billingChart) {
      showToast('No active billing chart to export', 'error');
      return;
    }
    const imageURI = billingChart.toBase64Image();
    const a = document.createElement('a');
    a.href = imageURI;
    a.download = `slurm-billing-chart-${Date.now()}.png`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    showToast('Billing Chart image downloaded successfully', 'success');
  } else {
    try {
      const res = await fetch(`/api/v1/slurm/billing/export?format=json`);
      if (res.ok) {
        const data = await res.json();
        const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `slurm-billing-report-${data.timestamp || Date.now()}.json`;
        a.click();
        URL.revokeObjectURL(url);
        showToast('Billing JSON report exported successfully', 'success');
      }
    } catch (e) {
      showToast(`Export failed: ${e.message}`, 'error');
    }
  }
}

/* 多页面平滑切换路由管理 */
function switchTab(tabName) {
  document.querySelectorAll('.nav-item').forEach(el => el.classList.remove('active'));

  const overviewSec = document.getElementById('overview-section');
  const opsAnalyticsSec = document.getElementById('ops-analytics-section');
  const nodesDedicatedSec = document.getElementById('nodes-dedicated-section');
  const jobsDedicatedSec = document.getElementById('jobs-dedicated-section');
  const workspacesDedicatedSec = document.getElementById('workspaces-dedicated-section');
  const billingSec = document.getElementById('billing-section');
  const titleEl = document.getElementById('current-view-title');

  // Hide all sections first
  if (overviewSec) overviewSec.style.display = 'none';
  if (opsAnalyticsSec) opsAnalyticsSec.style.display = 'none';
  if (nodesDedicatedSec) nodesDedicatedSec.style.display = 'none';
  if (jobsDedicatedSec) jobsDedicatedSec.style.display = 'none';
  if (workspacesDedicatedSec) workspacesDedicatedSec.style.display = 'none';
  if (billingSec) billingSec.style.display = 'none';

  if (tabName === 'overview') {
    const navEl = document.getElementById('nav-overview');
    if (navEl) navEl.classList.add('active');
    if (overviewSec) overviewSec.style.display = 'grid';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Overview';
  } else if (tabName === 'nodes') {
    const navEl = document.getElementById('nav-nodes');
    if (navEl) navEl.classList.add('active');
    if (nodesDedicatedSec) nodesDedicatedSec.style.display = 'flex';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Compute Nodes Inventory';
    renderDedicatedNodesTable();
  } else if (tabName === 'jobs') {
    const navEl = document.getElementById('nav-jobs');
    if (navEl) navEl.classList.add('active');
    if (jobsDedicatedSec) jobsDedicatedSec.style.display = 'flex';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Job Lifecycle Control';
    renderDedicatedJobsTable();
  } else if (tabName === 'workspaces') {
    const navEl = document.getElementById('nav-workspaces');
    if (navEl) navEl.classList.add('active');
    if (workspacesDedicatedSec) workspacesDedicatedSec.style.display = 'flex';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Container Workspaces';
    fetchContainerWorkspaces();
  } else if (tabName === 'ops-analytics') {
    const navEl = document.getElementById('nav-ops-analytics');
    if (navEl) navEl.classList.add('active');
    if (opsAnalyticsSec) opsAnalyticsSec.style.display = 'block';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Operations Analytics';
    fetchOpsAnalyticsData();
  } else if (tabName === 'billing') {
    const navEl = document.getElementById('nav-billing');
    if (navEl) navEl.classList.add('active');
    if (billingSec) billingSec.style.display = 'block';
    if (titleEl) titleEl.textContent = 'SLURM CLUSTER MANAGER | Billing & Quota';
    fetchBillingUsage();
  }
}

/* 真实 Slurm Analytics 数据拉取与呈现 */
async function fetchOpsAnalyticsData() {
  const tbody = document.getElementById('ops-projects-table-body');
  const nodesVal = document.getElementById('ops-total-nodes-val');
  const jobsVal = document.getElementById('ops-active-jobs-val');
  const badgeVal = document.getElementById('ops-utilization-badge');
  if (!tbody) return;

  try {
    const res = await fetch(`/api/v1/slurm/billing/usage?user=hpcuser&project=default`);
    if (res.ok) {
      const data = await res.json();
      tbody.innerHTML = '';
      
      const tr = document.createElement('tr');
      const cost = ((data.total_cpu_hours || 0) * 0.5) + ((data.total_memory_gb_hours || 0) * 0.1);
      tr.innerHTML = `
        <td><strong>${data.user || 'hpcuser'} (${data.project || 'default'})</strong></td>
        <td>${(data.total_cpu_hours || 0).toFixed(2)} hrs</td>
        <td>${(data.total_memory_gb_hours || 0).toFixed(2)} GB·h</td>
        <td><strong style="color:var(--accent-emerald);">¥${cost.toFixed(2)}</strong></td>
      `;
      tbody.appendChild(tr);

      const activeNodesNum = document.getElementById('active-nodes-num')?.textContent || '3 / 3';
      const activeJobsNum = document.getElementById('active-jobs-num')?.textContent || '0';

      if (nodesVal) nodesVal.textContent = activeNodesNum;
      if (jobsVal) jobsVal.textContent = activeJobsNum;
      if (badgeVal) badgeVal.textContent = `Nodes Online: ${activeNodesNum}`;
    }
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="4" style="text-align:center; color:var(--accent-rose); padding: 16px;">Failed to load real Slurm accounting data</td></tr>`;
  }
}

/* 7. Container Workspaces UI Controls & Lifecycle */
function openWorkspaceModal() {
  const modal = document.getElementById('launch-workspace-modal');
  if (modal) modal.style.display = 'flex';
}

function closeWorkspaceModal() {
  const modal = document.getElementById('launch-workspace-modal');
  if (modal) modal.style.display = 'none';
}

async function handleWorkspaceSubmit(e) {
  e.preventDefault();
  const envType = document.getElementById('ws-env-type-select').value;
  const cpus = parseInt(document.getElementById('ws-cpus-input').value || '2', 10);
  const memoryMB = parseInt(document.getElementById('ws-memory-input').value || '4096', 10);
  const nodes = parseInt(document.getElementById('ws-nodes-input').value || '1', 10);

  try {
    const res = await fetch('/api/v1/slurm/containers/launch', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-User-Role': currentUserRole
      },
      body: JSON.stringify({
        env_type: envType,
        cpus: cpus,
        memory_mb: memoryMB,
        nodes: nodes
      })
    });
    const data = await res.json();
    if (res.ok) {
      showToast(`Workspace launched! ID: ${data.container_id}`, 'success');
      closeWorkspaceModal();
      if (data.web_url) {
        window.open(data.web_url, '_blank');
      }
      fetchContainerWorkspaces();
    } else {
      showToast(`Launch failed: ${data.error || 'Unknown error'}`, 'error');
    }
  } catch (err) {
    showToast(`Network error: ${err.message}`, 'error');
  }
}

async function fetchContainerWorkspaces() {
  const overviewBody = document.getElementById('workspaces-table-body');
  const dedicatedBody = document.getElementById('dedicated-workspaces-tbody');
  const memberBody = document.getElementById('member-workspaces-tbody');

  try {
    const res = await fetch('/api/v1/slurm/containers/list');
    if (res.ok) {
      const data = await res.json();
      const containers = data.containers || [];

      renderWorkspacesTbody(overviewBody, containers);
      renderWorkspacesTbody(dedicatedBody, containers);
      renderWorkspacesTbody(memberBody, containers);
    }
  } catch (e) {
    console.error('Fetch container workspaces error:', e);
  }
}

function renderWorkspacesTbody(tbody, containers) {
  if (!tbody) return;
  if (containers.length > 0) {
    tbody.innerHTML = '';
    containers.forEach(ctr => {
      const tr = document.createElement('tr');
      const envLabel = ctr.env_type === 'vscode' ? 'Web-VSCode' : 'JupyterLab';
      tr.innerHTML = `
        <td><strong>${ctr.container_id}</strong></td>
        <td><span style="color:var(--accent-cyan); font-weight:700;">${envLabel}</span></td>
        <td><span class="status-pill running">RUNNING</span></td>
        <td>${ctr.cpus} vCPU / ${ctr.memory_mb} MB</td>
        <td><a href="${ctr.web_url}" target="_blank" style="color:var(--accent-emerald); font-size:11px; text-decoration:none;">Open IDE</a></td>
        <td>
          <div class="action-btn-group">
            <button class="neu-btn primary" style="font-size:9px; padding:2px 6px;" onclick="window.open('${ctr.web_url}', '_blank')">Open</button>
            <button class="neu-btn rose" style="font-size:9px; padding:2px 6px;" onclick="recycleWorkspace('${ctr.container_id}')">Recycle</button>
          </div>
        </td>
      `;
      tbody.appendChild(tr);
    });
  } else {
    tbody.innerHTML = `<tr><td colspan="6" style="text-align:center; color:var(--text-dim); padding: 20px;">No Active Workspaces Running</td></tr>`;
  }
}

function recycleWorkspace(containerId) {
  showConfirm('Recycle Workspace', `Are you sure you want to recycle workspace ${containerId}?`, async () => {
    try {
      const res = await fetch(`/api/v1/slurm/containers/${containerId}`, {
        method: 'DELETE',
        headers: { 'X-User-Role': currentUserRole }
      });
      const data = await res.json();
      if (res.ok) {
        showToast(`Workspace ${containerId} recycled`, 'success');
        fetchContainerWorkspaces();
      } else {
        showToast(`Recycle failed: ${data.error || 'Unknown error'}`, 'error');
      }
    } catch (err) {
      showToast(`Network error: ${err.message}`, 'error');
    }
  });
}
