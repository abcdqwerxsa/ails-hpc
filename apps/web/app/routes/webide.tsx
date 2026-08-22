import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type ChangeEvent, type ReactNode } from 'react';
import { can } from '../services/auth';
import { slurm, ideFullURL, type ContainerInstance, type QOSInfo } from '../services/slurm';
import { Select } from '../components/select';

export const Route = createFileRoute('/webide')({ component: WebIDEPage });

function QOSBadge({ qos }: { qos?: string }) {
  const name = (qos || 'normal').toLowerCase();
  let bg = 'rgba(6, 182, 212, 0.12)';
  let color = 'var(--accent-cyan,#06b6d4)';
  let border = 'rgba(6, 182, 212, 0.25)';

  if (name.includes('vip') || name.includes('high') || name.includes('prio')) {
    bg = 'rgba(245, 158, 11, 0.15)';
    color = '#f59e0b';
    border = 'rgba(245, 158, 11, 0.3)';
  } else if (name.includes('debug') || name.includes('test')) {
    bg = 'rgba(16, 185, 129, 0.15)';
    color = '#34d399';
    border = 'rgba(16, 185, 129, 0.3)';
  } else if (name.includes('gpu')) {
    bg = 'rgba(168, 85, 247, 0.15)';
    color = '#c084fc';
    border = 'rgba(168, 85, 247, 0.3)';
  }

  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.3rem',
        padding: '0.12rem 0.45rem',
        borderRadius: 5,
        fontSize: '0.72rem',
        fontFamily: "'JetBrains Mono', monospace",
        fontWeight: 700,
        background: bg,
        color,
        border: `1px solid ${border}`,
      }}
    >
      <span style={{ width: 5, height: 5, borderRadius: '50%', background: color }} />
      {qos || 'normal'}
    </span>
  );
}

function WebIDEPage() {
  const [sessions, setSessions] = useState<ContainerInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [envType, setEnvType] = useState<'jupyter' | 'vscode'>('jupyter');
  const [envPreset, setEnvPreset] = useState('base');
  const [gpus, setGpus] = useState('0');
  const [cpus, setCpus] = useState('2');
  const [memMb, setMemMb] = useState('4096');
  const [durationMin, setDurationMin] = useState('120');
  const [selectedQos, setSelectedQos] = useState('normal');
  const [reservation, setReservation] = useState('');
  const [availableQosList, setAvailableQosList] = useState<QOSInfo[]>([]);
  const [launching, setLaunching] = useState(false);
  const [acting, setActing] = useState('');

  const refresh = useCallback(async () => {
    try {
      const r = await slurm.listContainers();
      setSessions(r.containers || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载会话失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [refresh]);

  useEffect(() => {
    slurm
      .getAvailableQOS()
      .then((r) => {
        const list =
          r.allowedQos && r.allowedQos.length > 0
            ? r.allowedQos
            : r.allowedQOS && r.allowedQOS.length > 0
            ? r.allowedQOS
            : r.availableQos && r.availableQos.length > 0
            ? r.availableQos
            : [{ name: 'normal', description: '标准调度策略' }];
        setAvailableQosList(list);
        const def = r.defaultQos || r.defaultQOS || list[0]?.name || 'normal';
        setSelectedQos(def);
      })
      .catch(() => {
        setAvailableQosList([{ name: 'normal', description: '标准调度策略' }]);
      });
  }, []);

  const launch = async () => {
    setLaunching(true);
    setError('');
    setInfo('');
    try {
      const r = await slurm.launchContainer({
        env_type: envType,
        env_preset: envPreset,
        gpus: Number(gpus) || 0,
        cpus: Number(cpus) || 2,
        memory_mb: Number(memMb) || 4096,
        time_limit_min: Number(durationMin) || 120,
        qos: selectedQos?.trim() || undefined,
        reservation: reservation.trim() || undefined,
      });
      const gpuTag = Number(gpus) > 0 ? ` [${gpus} GPU]` : '';
      const resvTag = reservation.trim() ? ` · 预约: ${reservation.trim()}` : '';
      setInfo(`已启动 ${envType}${gpuTag} 会话（作业 #${r.allocated?.job_id ?? '-'} · QOS: ${selectedQos}${resvTag}）。等状态变 RUNNING 后点"打开 IDE"。`);
      setReservation('');
      await refresh();
    } catch (e: any) {
      setError(`启动失败：${e?.message || e}`);
    } finally {
      setLaunching(false);
    }
  };


  const extend = async (id: string) => {
    const v = prompt('延长会话多少分钟？（1-720）', '60');
    const m = Number(v);
    if (!v || !m || m < 1 || m > 720) return;
    setActing(id + ':extend');
    setError(''); setInfo('');
    try {
      await slurm.extendSession(id, m);
      setInfo(`会话已延长 ${m} 分钟`);
    } catch (e: any) {
      setError(`续期失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  const recycle = async (id: string) => {
    setActing(id);
    setError('');
    setInfo('');
    try {
      const r = await slurm.recycleContainer(id);
      setInfo(r.message);
      await refresh();
    } catch (e: any) {
      setError(`回收失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  return (
    <div>
      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      <div
        style={{
          background: 'var(--bg-card,#1b1e28)',
          border: '1px solid var(--border-color,#2a2f3a)',
          borderRadius: 12,
          padding: '1.25rem',
          marginBottom: '1.5rem',
          display: 'flex',
          gap: '0.75rem',
          alignItems: 'end',
          flexWrap: 'wrap',
          boxShadow: 'var(--shadow-card)',
          transition: 'box-shadow .3s ease',
        }}
      >
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          编辑器
          <Select
            value={envType}
            onChange={(v) => setEnvType(v as 'jupyter' | 'vscode')}
            options={[
              { value: 'jupyter', label: 'JupyterLab' },
              { value: 'vscode', label: 'VS Code (code-server)' },
            ]}
          />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          运行环境
          <Select
            width={160}
            value={envPreset}
            onChange={setEnvPreset}
            options={[
              { value: 'base', label: '基础 Python 3.10' },
              { value: 'pytorch', label: 'PyTorch AI 环境' },
              { value: 'custom', label: '自建持久化 venv' },
            ]}
          />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          GPU 资源
          <Select
            width={140}
            value={gpus}
            onChange={setGpus}
            options={[
              { value: '0', label: '无 GPU (CPU)' },
              { value: '1', label: '1× GPU (加速卡)' },
            ]}
          />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          CPU 核数
          <input className="form-control" type="number" min="1" max="8" value={cpus}
            onChange={(e) => setCpus(e.target.value)} style={{ width: 80 }} />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          内存 MB
          <input className="form-control" type="number" min="512" max="16384" step="256" value={memMb}
            onChange={(e) => setMemMb(e.target.value)} style={{ width: 100 }} />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          时长（分钟）
          <Select
            width={140}
            value={durationMin}
            onChange={setDurationMin}
            options={[
              { value: '30', label: '30' },
              { value: '60', label: '60' },
              { value: '120', label: '120（默认）' },
              { value: '240', label: '240' },
              { value: '480', label: '480' },
              { value: '720', label: '720（上限）' },
            ]}
          />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          QOS 策略
          <Select
            width={160}
            value={selectedQos}
            onChange={setSelectedQos}
            options={availableQosList.map((q) => ({
              value: q.name,
              label: `${q.name}${q.description ? ` (${q.description})` : ''}`,
            }))}
          />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          资源预约 (可选)
          <input
            className="form-control"
            value={reservation}
            onChange={(e) => setReservation(e.target.value)}
            placeholder="如 vip-gpu"
            style={{ width: 120 }}
          />
        </label>
        {can('ide:manage') && (
          <button className="btn-primary" onClick={launch} disabled={launching} style={{ padding: '0.5rem 1.5rem' }}>
            {launching ? '启动中…' : '启动 IDE 会话'}
          </button>
        )}


        {/* QOS 策略提示卡 */}
        {(() => {
          const curQos = availableQosList.find((q) => q.name === selectedQos);
          if (!curQos) return null;
          return (
            <div
              style={{
                width: '100%',
                marginTop: '0.5rem',
                padding: '0.5rem 0.75rem',
                borderRadius: 8,
                background: 'rgba(255,255,255,0.02)',
                border: '1px solid var(--border-color,#2a2f3a)',
                display: 'flex',
                gap: '1rem',
                flexWrap: 'wrap',
                alignItems: 'center',
                fontSize: '0.76rem',
                color: 'var(--text-muted,#94a3b8)',
              }}
            >
              <div style={{ color: 'var(--text-main,#f1f5f9)', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '0.35rem' }}>
                <span>调度策略：</span>
                <QOSBadge qos={curQos.name} />
              </div>
              {curQos.priority && (
                <span>🚀 优先级: <strong style={{ color: 'var(--accent-cyan,#06b6d4)' }}>{curQos.priority}</strong></span>
              )}
              {(curQos.max_wall || curQos.max_wall_duration) && (
                <span>⏱️ 最大时长: <strong>{curQos.max_wall_duration || curQos.max_wall}</strong></span>
              )}
              {(curQos.max_tres_per_user || curQos.max_tres) && (
                <span>⚡ 配额限制: <strong>{curQos.max_tres_per_user || curQos.max_tres}</strong></span>
              )}
              {curQos.description && (
                <span style={{ fontStyle: 'italic', color: 'var(--text-dim,#64748b)' }}>({curQos.description})</span>
              )}
            </div>
          );
        })()}
      </div>

      <h3 style={{ margin: '0 0 0.75rem', fontSize: '1.05rem' }}>活跃会话</h3>
      {loading ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>加载中…</div>
      ) : sessions.length === 0 ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>当前无活跃会话，点上方"启动"创建。</div>
      ) : (
        <div style={{ display: 'grid', gap: '0.75rem' }}>
          {sessions.map((s) => {
            const running = s.status === 'RUNNING';
            const hasGpu = (s.gpus ?? 0) > 0;
            const idleMin = s.idle_minutes ?? 0;
            return (
              <div
                key={s.container_id}
                style={{
                  background: 'var(--bg-card,#1b1e28)',
                  border: '1px solid var(--border-color,#2a2f3a)',
                  borderRadius: 10,
                  padding: '1rem',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  gap: '1rem',
                  flexWrap: 'wrap',
                  boxShadow: 'var(--shadow-card)',
                  transition: 'box-shadow .3s ease',
                }}
              >
                <div style={{ display: 'grid', gap: '0.25rem', fontSize: '0.88rem' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
                    <strong>{s.env_type === 'vscode' ? 'VS Code' : 'JupyterLab'}</strong>
                    <QOSBadge qos={s.qos || 'normal'} />
                    {s.env_preset && s.env_preset !== 'base' && (
                      <span style={{ fontSize: '0.75rem', padding: '0.1rem 0.4rem', borderRadius: 4, background: 'rgba(99,102,241,0.15)', color: '#818cf8' }}>
                        {s.env_preset}
                      </span>
                    )}
                    {hasGpu ? (
                      <span style={{ fontSize: '0.75rem', padding: '0.1rem 0.4rem', borderRadius: 4, background: 'rgba(16,185,129,0.15)', color: '#34d399', fontWeight: 600 }}>
                        ⚡ {s.gpus} GPU
                      </span>
                    ) : (
                      <span style={{ fontSize: '0.75rem', padding: '0.1rem 0.4rem', borderRadius: 4, background: 'rgba(148,163,184,0.15)', color: '#94a3b8' }}>
                        CPU
                      </span>
                    )}
                    <span style={{ color: 'var(--text-muted,#888)' }}>· 作业 #{s.job_id} · {s.node || '...'}</span>
                  </div>
                  <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.8rem', display: 'flex', gap: '0.8rem', flexWrap: 'wrap' }}>
                    <span>CPU {s.cpus} · 内存 {s.memory_mb}MB · 节点 {s.nodes}</span>
                    {running && (
                      <span style={{ color: idleMin >= 45 ? '#f59e0b' : 'var(--text-muted,#94a3b8)' }}>
                        {idleMin === 0 ? '刚刚活跃' : `已空闲 ${idleMin} 分钟`}
                      </span>
                    )}
                  </div>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
                  <span style={{ padding: '0.2rem 0.6rem', borderRadius: 6, fontSize: '0.72rem', fontWeight: 700, color: '#fff', background: running ? '#10b981' : '#f59e0b' }}>
                    {s.status}
                  </span>
                  {can('ide:manage') && (
                    <button
                      className="btn-primary"
                      disabled={!running}
                      onClick={() => window.open(ideFullURL(s.web_url), '_blank')}
                      style={{ padding: '0.35rem 1rem' }}
                    >
                      打开 IDE
                    </button>
                  )}
                  {can('ide:manage') && (
                    <>
                      <button
                        className="neu-btn"
                        onClick={() => extend(s.container_id)}
                        disabled={!!acting || !running}
                      >
                        续期
                      </button>
                      <button
                        onClick={() => recycle(s.container_id)}
                        disabled={!!acting}
                        style={{
                          padding: '0.4rem 0.9rem',
                          borderRadius: 8,
                          border: 'none',
                          background: 'var(--card-bg)',
                          boxShadow: 'var(--shadow-btn)',
                          color: 'var(--accent-rose)',
                          cursor: 'pointer',
                          transition: 'box-shadow .2s ease',
                        }}
                      >
                        回收
                      </button>
                    </>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
      <div style={{ marginTop: '1rem', fontSize: '0.78rem', color: 'var(--text-muted,#888)', lineHeight: '1.5' }}>
        💡 <strong>使用提示与防浪费机制</strong>：<br />
        1. 启动后约 10–30s 进入 RUNNING；VS Code 与 Jupyter 均支持在网页内流畅交互。<br />
        2. <strong>空闲自动回收（Idle Auto-Reclaim）</strong>：会话在连续无活跃超过 60 分钟后会自动释放，避免昂贵算力与 GPU 机时闲置浪费；如需长时间运行建议改用批量批处理作业（Jobs 页面提交）。
      </div>
    </div>
  );
}

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}
