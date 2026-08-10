import { createFileRoute } from '@tanstack/react-router';
import { useState, useEffect } from 'react';

export const Route = createFileRoute('/jobs')({
  component: JobsPage,
});

interface HpcJobItem {
  metadata: {
    name: string;
    namespace: string;
    creationTimestamp: string;
  };
  spec: {
    jobType?: string;
    image: string;
    slots: number;
    command: string[];
    queue: string;
    storageSize?: string;
    priorityClassName?: string;
  };
  status?: {
    phase: string;
    coreHours?: number;
    executionDuration?: string;
  };
}

function JobsPage() {
  const [jobs, setJobs] = useState<HpcJobItem[]>([]);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [logModalPod, setLogModalPod] = useState<string | null>(null);
  const [logs, setLogs] = useState<string[]>([]);

  // Form State
  const [name, setName] = useState('');
  const [jobType, setJobType] = useState('mpi');
  const [image, setImage] = useState('quay.io/nilpo1/mpich-ubuntu:v0.8.2');
  const [slots, setSlots] = useState(4);
  const [storageSize, setStorageSize] = useState('5Gi');
  const [command, setCommand] = useState('mpirun -np 4 /opt/mpich-3.3.2/examples/cpi');

  const fetchJobs = async () => {
    try {
      const token = localStorage.getItem('ails_token') || '';
      const res = await fetch('http://192.168.20.226:8090/api/v1/hpcjobs', {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (res.ok) {
        const data = await res.json();
        setJobs(data.jobs || []);
      }
    } catch (e) {
      console.error(e);
    }
  };

  useEffect(() => {
    fetchJobs();
    const timer = setInterval(fetchJobs, 3000);
    return () => clearInterval(timer);
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const token = localStorage.getItem('ails_token') || '';
      const res = await fetch('http://192.168.20.226:8090/api/v1/hpcjobs', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          name,
          jobType,
          image,
          slots: Number(slots),
          storageSize,
          command: command.split(' '),
        }),
      });

      if (res.ok) {
        setIsModalOpen(false);
        setName('');
        fetchJobs();
      } else {
        const data = await res.json();
        alert('提交拒绝: ' + (data.error || '权限不足'));
      }
    } catch (err) {
      alert('提交作业异常: ' + err);
    }
  };

  const openLogViewer = (jobName: string, type?: string) => {
    const launcherPod = type === 'batch' ? `${jobName}` : `${jobName}-launcher`;
    const token = localStorage.getItem('ails_token') || '';
    setLogModalPod(launcherPod);
    setLogs(['[系统日志] 正在建立与 Pod ' + launcherPod + ' 的 WebSocket 实时日志长连接...']);

    const ws = new WebSocket(`ws://192.168.20.226:8090/ws/logs?podName=${launcherPod}&namespace=default&token=${token}`);
    ws.onmessage = (event) => {
      setLogs((prev) => [...prev, event.data]);
    };
    ws.onerror = () => {
      setLogs((prev) => [...prev, '[连接错误] 无法建立 WebSocket 连接或 Pod 正在初始化。']);
    };
  };

  return (
    <div>
      <div className="header-bar">
        <div className="header-title">
          <h1>HPC 计算作业管理 (JWT/RBAC 已保护)</h1>
          <p>多租户安全上下文准入控制、 MPI/Batch 计算模型、 Local-Path PVC 挂载与核时计量</p>
        </div>
        <div style={{ display: 'flex', gap: '0.75rem' }}>
          <button className="btn-primary" style={{ background: 'var(--bg-card-hover)', color: 'var(--text-main)', border: '1px solid var(--border-color)' }} onClick={fetchJobs}>
            刷新数据
          </button>
          <button className="btn-primary" onClick={() => setIsModalOpen(true)}>
            + 提交 HPC 作业
          </button>
        </div>
      </div>

      <div className="table-card">
        <table className="custom-table">
          <thead>
            <tr>
              <th>作业标识</th>
              <th>计算模型 (Type)</th>
              <th>容器镜像 (Image)</th>
              <th>并行进程数</th>
              <th>持久化存储 (PVC)</th>
              <th>耗时 / 算力核时</th>
              <th>运行状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {jobs.length === 0 ? (
              <tr>
                <td colSpan={8} style={{ textAlign: 'center', color: 'var(--text-muted)' }}>
                  暂无提交的 HPC 作业，点击上方按钮发起并行计算。
                </td>
              </tr>
            ) : (
              jobs.map((job) => {
                const specStorage = job.spec?.storageSize;
                const coreHours = job.status?.coreHours;
                const duration = job.status?.executionDuration;
                const type = job.spec?.jobType || 'mpi';

                return (
                  <tr key={job.metadata.name}>
                    <td style={{ fontWeight: 600 }}>{job.metadata.name}</td>
                    <td>
                      <span className="badge" style={{ background: type === 'batch' ? 'rgba(147, 51, 234, 0.1)' : 'rgba(59, 130, 246, 0.1)', color: type === 'batch' ? '#9333ea' : '#3b82f6', border: '1px solid var(--border-color)' }}>
                        {type.toUpperCase()}
                      </span>
                    </td>
                    <td className="font-mono" style={{ fontSize: '0.8rem', color: 'var(--text-muted)' }}>{job.spec?.image || '-'}</td>
                    <td className="font-mono">{job.spec?.slots || 1} Slots</td>
                    <td className="font-mono" style={{ fontSize: '0.85rem' }}>
                      {specStorage ? `${job.metadata.name}-pvc (${specStorage})` : '无存储'}
                    </td>
                    <td className="font-mono" style={{ fontSize: '0.85rem', color: coreHours ? 'var(--accent-emerald)' : 'var(--text-muted)' }}>
                      {coreHours ? `${coreHours.toFixed(4)} 核时 (${duration})` : '计算中...'}
                    </td>
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
                    <td>
                      <button
                        style={{
                          background: 'transparent',
                          border: '1px solid var(--border-color)',
                          color: 'var(--accent-primary)',
                          padding: '0.35rem 0.75rem',
                          borderRadius: '6px',
                          cursor: 'pointer',
                          fontSize: '0.85rem',
                          fontWeight: 600
                        }}
                        onClick={() => openLogViewer(job.metadata.name, type)}
                      >
                        查看日志
                      </button>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      {/* 新建 Modal */}
      {isModalOpen && (
        <div className="modal-backdrop" onClick={() => setIsModalOpen(false)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <h2 style={{ marginBottom: '1.5rem', fontWeight: 600 }}>提交分布式 HPC 并行作业</h2>
            <form onSubmit={handleSubmit}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
                <div className="form-group">
                  <label>作业唯一名称 (Job Name)</label>
                  <input
                    className="form-control"
                    placeholder="例如: batch-task-demo"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    required
                  />
                </div>
                <div className="form-group">
                  <label>计算模型 (Job Type)</label>
                  <select
                    className="form-control"
                    value={jobType}
                    onChange={(e) => setJobType(e.target.value)}
                  >
                    <option value="mpi">MPI 分布式并行作业</option>
                    <option value="batch">Standard Batch 批处理作业</option>
                  </select>
                </div>
              </div>

              <div className="form-group">
                <label>容器镜像 (Container Image)</label>
                <input
                  className="form-control font-mono"
                  value={image}
                  onChange={(e) => setImage(e.target.value)}
                  required
                />
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
                <div className="form-group">
                  <label>并行进程/CPU配额 (Slots)</label>
                  <input
                    type="number"
                    className="form-control font-mono"
                    value={slots}
                    onChange={(e) => setSlots(Number(e.target.value))}
                    required
                  />
                </div>
                <div className="form-group">
                  <label>挂载持久化存储 (PVC 大小)</label>
                  <input
                    className="form-control font-mono"
                    placeholder="如 5Gi, 10Gi 或留空"
                    value={storageSize}
                    onChange={(e) => setStorageSize(e.target.value)}
                  />
                </div>
              </div>
              <div className="form-group">
                <label>启动执行指令 (Execution Command)</label>
                <input
                  className="form-control font-mono"
                  value={command}
                  onChange={(e) => setCommand(e.target.value)}
                  required
                />
              </div>
              <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem', marginTop: '1.5rem' }}>
                <button
                  type="button"
                  style={{
                    background: 'transparent',
                    border: '1px solid var(--border-color)',
                    color: 'var(--text-main)',
                    padding: '0.6rem 1.25rem',
                    borderRadius: '6px',
                    cursor: 'pointer',
                  }}
                  onClick={() => setIsModalOpen(false)}
                >
                  取消
                </button>
                <button type="submit" className="btn-primary">
                  提交并启动计算
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* 日志 Modal */}
      {logModalPod && (
        <div className="modal-backdrop" onClick={() => setLogModalPod(null)}>
          <div className="modal-card" style={{ maxWidth: '720px' }} onClick={(e) => e.stopPropagation()}>
            <h2 style={{ marginBottom: '1rem', fontWeight: 600, fontSize: '1.1rem' }}>Pod 实况输出日志: {logModalPod}</h2>
            <div className="terminal-box">
              {logs.map((l, idx) => (
                <div key={idx}>{l}</div>
              ))}
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: '1.25rem' }}>
              <button className="btn-primary" onClick={() => setLogModalPod(null)}>
                关闭视窗
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
