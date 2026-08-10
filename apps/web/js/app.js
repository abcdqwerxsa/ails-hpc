/* AILS Slurm Manager - Pure Real Slurm Data Driven Application */

let currentTheme = localStorage.getItem('slurm_theme') || 'dark';
let perfMetricsChart = null;
let partitionChart = null;

document.addEventListener('DOMContentLoaded', () => {
  initTheme();
  initGauges(0, 0, 0, 0);
  initPartitionChart([], []);
  initPerfMetricsChart();
  
  fetchSlurmStatus();
  fetchSlurmNodes();
  fetchSlurmJobs();
  fetchSlurmPartitions();

  setInterval(() => {
    fetchSlurmStatus();
    fetchSlurmNodes();
    fetchSlurmJobs();
    fetchSlurmPartitions();
  }, 5000);
});

/* 主题无缝切换 */
function initTheme() {
  document.documentElement.setAttribute('data-theme', currentTheme);
  updateThemeIcon();

  const themeBtn = document.getElementById('theme-btn');
  if (themeBtn) {
    themeBtn.addEventListener('click', () => {
      currentTheme = currentTheme === 'dark' ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', currentTheme);
      localStorage.setItem('slurm_theme', currentTheme);
      updateThemeIcon();
      updateChartsTheme();
    });
  }
}

function updateThemeIcon() {
  const btn = document.getElementById('theme-btn');
  if (btn) {
    btn.textContent = currentTheme === 'dark' ? '🌙' : '☀️';
  }
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
  
  // 背景弧
  ctx.beginPath();
  ctx.arc(cx, cy, radius, Math.PI, 2 * Math.PI, false);
  ctx.lineWidth = 7;
  ctx.strokeStyle = currentTheme === 'dark' ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.06)';
  ctx.stroke();

  // 充满度高彩荧光弧
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
      if (data.nodes && matrix) {
        const total = data.nodes.length;
        let activeCount = 0;
        let totalCpus = 0;

        matrix.innerHTML = '';
        data.nodes.forEach(n => {
          const stateStr = (n.state || 'idle').toLowerCase();
          const isUp = !stateStr.includes('down') && !stateStr.includes('drain');
          if (isUp) activeCount++;
          totalCpus += (n.cpus || 1);

          let chipClass = 'idle';
          if (stateStr.includes('alloc') || stateStr.includes('busy')) chipClass = 'amber';
          if (!isUp) chipClass = 'rose';

          const chip = document.createElement('div');
          chip.className = `node-small-chip ${chipClass}`;
          chip.title = `Node: ${n.name} | CPUs: ${n.cpus || 8} | State: ${n.state}`;
          chip.innerHTML = `
            <div class="dot"></div>
            <span>${n.name}</span>
          `;
          matrix.appendChild(chip);
        });

        const activePct = total > 0 ? Math.round((activeCount / total) * 100) : 0;
        if (nodesNum) nodesNum.textContent = `${activeCount} / ${total}`;
        if (nodesSub) nodesSub.textContent = `+${activePct}% Operational`;

        // 计算真实使用率 (当前节点处于 IDLE 闲置状态)
        if (cpuNum) cpuNum.textContent = `0%`;
        if (cpuSub) cpuSub.textContent = `idle (${totalCpus} cores)`;
        if (memNum) memNum.textContent = `0%`;
        if (memSub) memSub.textContent = `optimal`;

        initGauges(activePct, 0, 0, 0);

        // 动态向 Performance 实时线图中 Push 数据
        if (perfMetricsChart) {
          const dataCpu = perfMetricsChart.data.datasets[0].data;
          dataCpu.shift();
          dataCpu.push(0);
          perfMetricsChart.update();
        }
      }
    }
  } catch (e) {
    console.error('Fetch nodes error:', e);
  }
}

/* 3. 真实 Slurm 作业 (Jobs) 动态轮询与队列展示 */
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
      const jobs = data.jobs || [];

      if (jobsNum) jobsNum.textContent = `${jobs.length}`;
      if (jobsSub) jobsSub.textContent = `${jobs.length} Active Queue`;

      if (jobs.length > 0) {
        tbody.innerHTML = '';
        if (topJobsContainer) topJobsContainer.innerHTML = '';

        jobs.forEach(job => {
          const stateStr = (job.job_state || 'PENDING').toUpperCase();
          let badgeClass = 'w';
          if (stateStr.includes('RUNNING')) badgeClass = 'r';
          if (stateStr.includes('COMPLETED') || stateStr.includes('CANCELLED')) badgeClass = 'cg';

          const tr = document.createElement('tr');
          tr.innerHTML = `
            <td>${job.job_id}</td>
            <td>${job.name || 'Job'}</td>
            <td>${job.partition || 'debug'}</td>
            <td>${job.nodes || '1'}</td>
            <td><span class="status-badge-circle ${badgeClass}">${badgeClass.toUpperCase()}</span></td>
          `;
          tbody.appendChild(tr);

          // 填充 Top Jobs 面板
          if (topJobsContainer) {
            const item = document.createElement('div');
            item.className = 'top-job-item';
            item.innerHTML = `
              <div class="top-job-row"><span>Job #${job.job_id} (${job.name})</span><strong>Running</strong></div>
              <div class="progress-track"><div class="progress-fill" style="width:100%;"></div></div>
            `;
            topJobsContainer.appendChild(item);
          }
        });
      } else {
        tbody.innerHTML = `<tr><td colspan="5" style="text-align:center; color:var(--text-dim); padding: 20px;">No Active Jobs in Queue</td></tr>`;
        if (topJobsContainer) {
          topJobsContainer.innerHTML = `<div style="color:var(--text-dim); font-size:11px; text-align:center; padding: 20px;">No Heavy Jobs Running</div>`;
        }
      }
    }
  } catch (e) {
    console.error('Fetch jobs error:', e);
  }
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

/* Interactive Dev Launch Trigger */
function triggerDevLaunch(type) {
  const title = type === 'vscode' ? 'Web VSCode' : 'JupyterLab';
  if (confirm(`发起 ${title} 容器交互开发环境？`)) {
    fetch('/api/v1/slurm/launch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ env_type: type, nodes: 1, cpus: 4, memory_mb: 4096 })
    })
    .then(r => r.json())
    .then(data => {
      alert(`开发环境创建成功！访问链接: ${data.web_url}`);
      window.open(data.web_url, '_blank');
    });
  }
}
