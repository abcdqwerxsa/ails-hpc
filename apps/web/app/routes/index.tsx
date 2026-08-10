import { createFileRoute } from '@tanstack/react-router';
import { useState, useEffect } from 'react';

export const Route = createFileRoute('/')({
  component: DashboardPage,
});

interface HpcJobItem {
  metadata: {
    name: string;
    namespace: string;
    creationTimestamp: string;
  };
  spec: {
    jobType?: string;
    slots: number;
    storageSize?: string;
  };
  status?: {
    phase: string;
    coreHours?: number;
    executionDuration?: string;
  };
}

function DashboardPage() {
  const [jobs, setJobs] = useState<HpcJobItem[]>([]);
  const [queues, setQueues] = useState<any[]>([]);

  const fetchDashboardData = async () => {
    try {
      const [resJobs, resQueues] = await Promise.all([
        fetch('http://192.168.20.226:8090/api/v1/hpcjobs'),
        fetch('http://192.168.20.226:8090/api/v1/queues'),
      ]);

      if (resJobs.ok) {
        const data = await resJobs.json();
        setJobs(data.jobs || []);
      }
      if (resQueues.ok) {
        const data = await resQueues.json();
        setQueues(data.queues || []);
      }
    } catch (e) {
      console.error(e);
    }
  };

  useEffect(() => {
    fetchDashboardData();
    const timer = setInterval(fetchDashboardData, 3000);
    return () => clearInterval(timer);
  }, []);

  const totalJobs = jobs.length;
  const runningJobs = jobs.filter((j) => j.status?.phase === 'Running').length;
  const succeededJobs = jobs.filter((j) => j.status?.phase === 'Succeeded').length;
  const totalSlotsAllocated = jobs
    .filter((j) => j.status?.phase === 'Running')
    .reduce((acc, curr) => acc + (curr.spec.slots || 0), 0);
  const totalCoreHours = jobs.reduce((acc, curr) => acc + (curr.status?.coreHours || 0), 0);

  return (
    <div>
      <div className="header-bar">
        <div className="header-title">
          <h1>云原生 HPC 算力集群调度控制台</h1>
          <p>控制面节点: 192.168.20.226 (Ready) | 混合引擎: Kueue v0.19.0 + MPI-Operator v0.8.2</p>
        </div>
      </div>

      {/* 算力指标卡片 */}
      <div className="grid-stats">
        <div className="stat-card">
          <span className="stat-label">已提交总作业数量</span>
          <span className="stat-value">{totalJobs}</span>
          <span className="stat-subtext">MPI & Standard Batch 批处理</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">实时计算中作业 (Running)</span>
          <span className="stat-value" style={{ color: 'var(--accent-emerald)' }}>
            {runningJobs}
          </span>
          <span className="stat-subtext">占用 {totalSlotsAllocated} CPU 并行 Slots</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">累积消耗算力核时</span>
          <span className="stat-value" style={{ color: 'var(--accent-primary)' }}>
            {totalCoreHours.toFixed(4)}
          </span>
          <span className="stat-subtext">Core-Hours (准确结算)</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">算力队列压量 (Kueue)</span>
          <span className="stat-value">{queues.length > 0 ? queues[0].pendingWorkloads || 0 : 0}</span>
          <span className="stat-subtext">LocalQueue: user-queue</span>
        </div>
      </div>

      {/* 实时排队大盘与作业近况 */}
      <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: '1.25rem', marginBottom: '2rem' }}>
        <div className="table-card">
          <div className="table-header">
            <h2>最新运行作业监控</h2>
          </div>
          <table className="custom-table">
            <thead>
              <tr>
                <th>作业标识</th>
                <th>类型</th>
                <th>Slots</th>
                <th>存储挂载</th>
                <th>运行耗时</th>
                <th>状态</th>
              </tr>
            </thead>
            <tbody>
              {jobs.slice(0, 5).map((job) => (
                <tr key={job.metadata.name}>
                  <td style={{ fontWeight: 600 }}>{job.metadata.name}</td>
                  <td>
                    <span className="badge" style={{ background: 'var(--bg-card-hover)', border: '1px solid var(--border-color)', fontSize: '0.7rem' }}>
                      {(job.spec.jobType || 'mpi').toUpperCase()}
                    </span>
                  </td>
                  <td className="font-mono">{job.spec.slots}</td>
                  <td className="font-mono" style={{ fontSize: '0.8rem' }}>{job.spec.storageSize || '无'}</td>
                  <td className="font-mono" style={{ fontSize: '0.8rem' }}>{job.status?.executionDuration || '-'}</td>
                  <td>
                    <span
                      className={`badge ${
                        job.status?.phase === 'Running'
                          ? 'badge-running'
                          : job.status?.phase === 'Succeeded'
                          ? 'badge-succeeded'
                          : job.status?.phase === 'Failed'
                          ? 'badge-failed'
                          : 'badge-pending'
                      }`}
                    >
                      {job.status?.phase || 'Pending'}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* 算力池队列配额可视化面板 */}
        <div className="table-card" style={{ padding: '1.25rem' }}>
          <h2 style={{ fontSize: '1rem', fontWeight: 600, marginBottom: '1rem' }}>ClusterQueue 资源池使用率</h2>
          <div style={{ marginBottom: '1.25rem' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.85rem', marginBottom: '0.35rem' }}>
              <span>CPU 物理核心配额 (ClusterQueue)</span>
              <span className="font-mono" style={{ fontWeight: 600 }}>{totalSlotsAllocated} / 32 Cores</span>
            </div>
            <div style={{ height: '8px', background: 'var(--bg-card-hover)', borderRadius: '4px', overflow: 'hidden' }}>
              <div style={{ width: `${Math.min((totalSlotsAllocated / 32) * 100, 100)}%`, height: '100%', background: 'var(--accent-primary)', transition: 'width 0.3s ease' }} />
            </div>
          </div>

          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.85rem', marginBottom: '0.35rem' }}>
              <span>Local-Path 共享存储配额</span>
              <span className="font-mono" style={{ fontWeight: 600 }}>18Gi / 100Gi</span>
            </div>
            <div style={{ height: '8px', background: 'var(--bg-card-hover)', borderRadius: '4px', overflow: 'hidden' }}>
              <div style={{ width: `18%`, height: '100%', background: 'var(--accent-emerald)', transition: 'width 0.3s ease' }} />
            </div>
          </div>

          <div style={{ marginTop: '1.5rem', paddingTop: '1rem', borderTop: '1px solid var(--border-color)', fontSize: '0.8rem', color: 'var(--text-muted)' }}>
            <div>集群就绪节点: 1 Node (8 Core Intel i7 / NVIDIA A1000)</div>
            <div style={{ marginTop: '0.25rem' }}>算力状态: 健康 (100% 可用)</div>
          </div>
        </div>
      </div>
    </div>
  );
}
