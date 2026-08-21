// 调度管理页的预约与 QOS 面板（2026-08-19 IA 重组自 admin.tsx 迁出，逻辑不变，
// 状态自持）。分区面板仍在 routes/scheduler.tsx 内（原 partitions.tsx 整体迁入）。
import { useEffect, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react';
import { slurm, type QOSInfo, type CreateQOSRequest, type UpdateQOSRequest, type Reservation, type TenantInfo } from '../services/slurm';
import { Select } from './select';
import { Field, MiniBtn, Notice, StatusBadge, cardStyle, emptyStyle, mono, th, td } from './panel_ui';

export function ReservationsPanel() {
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [reservations, setReservations] = useState<Reservation[]>([]);
  const [resvForm, setResvForm] = useState({ name: '', durationMinutes: '30', users: '' });

  const loadResv = async () => {
    try {
      const r = await slurm.listReservations();
      setReservations(r.reservations || []);
    } catch (e: any) {
      setError(`预约读取失败：${e?.message || e}`);
    }
  };
  const submitResv = async () => {
    setError(''); setInfo('');
    try {
      await slurm.createReservation({ name: resvForm.name.trim(), durationMinutes: Number(resvForm.durationMinutes) || 30, users: resvForm.users.trim() || undefined });
      setInfo(`预约 ${resvForm.name.trim()} 已创建`);
      setResvForm({ name: '', durationMinutes: '30', users: '' });
      await loadResv();
    } catch (e: any) {
      setError(`创建预约失败：${e?.message || e}`);
    }
  };
  const delResv = async (name: string) => {
    setError(''); setInfo('');
    try {
      await slurm.deleteReservation(name);
      setInfo(`预约 ${name} 已删除`);
      await loadResv();
    } catch (e: any) {
      setError(`删除预约失败：${e?.message || e}`);
    }
  };
  useEffect(() => { loadResv(); }, []);

  return (
    <div style={{ ...cardStyle, marginTop: '1.5rem', display: 'block' }}>
      <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.75rem' }}>预约管理</div>
      {error && <div style={{ padding: '0.5rem 0.7rem', background: 'rgba(239,68,68,.1)', color: 'var(--accent-rose)', borderRadius: 6, fontSize: '0.8rem', marginBottom: '0.8rem' }}>{error}</div>}
      {info && <div style={{ padding: '0.5rem 0.7rem', background: 'rgba(16,185,129,.1)', color: '#10b981', borderRadius: 6, fontSize: '0.8rem', marginBottom: '0.8rem' }}>{info}</div>}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(160px,1fr))', gap: '0.75rem', marginBottom: '0.75rem' }}>
        <Field label="预约名">
          <input className="form-control" value={resvForm.name} onChange={(e) => setResvForm({ ...resvForm, name: e.target.value })} placeholder="maint-window" />
        </Field>
        <Field label="时长(分钟)">
          <input className="form-control" value={resvForm.durationMinutes} onChange={(e) => setResvForm({ ...resvForm, durationMinutes: e.target.value })} />
        </Field>
        <Field label="用户(可选,逗号分隔)">
          <input className="form-control" value={resvForm.users} onChange={(e) => setResvForm({ ...resvForm, users: e.target.value })} placeholder="留空=全租户" />
        </Field>
        <div style={{ display: 'flex', alignItems: 'end', gap: '0.5rem' }}>
          <button className="btn-primary" type="button" onClick={submitResv} style={{ padding: '0.45rem 1.2rem' }}>创建预约</button>
          <MiniBtn onClick={loadResv}>刷新</MiniBtn>
        </div>
      </div>
      {reservations.length === 0 ? (
        <div style={emptyStyle}>暂无预约</div>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
            <thead>
              <tr style={{ textAlign: 'left' }}>
                <th style={th}>名称</th><th style={th}>开始</th><th style={th}>结束</th>
                <th style={th}>节点</th><th style={th}>用户</th><th style={th}>状态</th><th style={th}>操作</th>
              </tr>
            </thead>
            <tbody>
              {reservations.map((r, i) => (
                <tr key={r.name} style={{ borderBottom: i === reservations.length - 1 ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                  <td style={{ ...td, ...mono, fontWeight: 700 }}>{r.name}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.8rem' }}>{r.start_time || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.8rem' }}>{r.end_time || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.8rem' }}>{r.nodes || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.8rem' }}>{r.users || 'ALL'}</td>
                  <td style={td}><StatusBadge status={r.state || ''} /></td>
                  <td style={td}>
                    <MiniBtn onClick={() => delResv(r.name)}>删除</MiniBtn>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// 格式化 TRES 字符串（如 "gres/gpu=4,cpu=32,mem=64G"）为结构化彩色徽章
export function formatTRES(tres?: string) {
  if (!tres || tres === '-1' || tres === 'NONE' || tres === '') {
    return <span style={{ color: 'var(--text-muted,#94a3b8)' }}>—</span>;
  }
  const parts = tres.split(',').map((p) => p.trim()).filter(Boolean);
  if (parts.length === 0) return <span style={{ color: 'var(--text-muted,#94a3b8)' }}>—</span>;

  return (
    <div style={{ display: 'flex', gap: '0.35rem', flexWrap: 'wrap', alignItems: 'center' }}>
      {parts.map((p, idx) => {
        const [k, v] = p.split('=');
        if (!k) return null;
        const key = k.toLowerCase();
        let bg = 'rgba(148, 163, 184, 0.12)';
        let color = 'var(--text-main,#f1f5f9)';
        let label = p;

        if (key.includes('gpu')) {
          bg = 'rgba(168, 85, 247, 0.16)';
          color = '#c084fc';
          label = `${v ? `${v} GPU` : p}`;
        } else if (key === 'cpu') {
          bg = 'rgba(6, 182, 212, 0.16)';
          color = '#22d3ee';
          label = `${v ? `${v} CPU` : p}`;
        } else if (key === 'mem') {
          bg = 'rgba(16, 185, 129, 0.16)';
          color = '#34d399';
          label = `${v ? `${v} 内存` : p}`;
        } else if (key === 'node') {
          bg = 'rgba(245, 158, 11, 0.16)';
          color = '#fbbf24';
          label = `${v ? `${v} 节点` : p}`;
        }

        return (
          <span
            key={idx}
            style={{
              fontSize: '0.72rem',
              padding: '0.12rem 0.45rem',
              borderRadius: 5,
              background: bg,
              color,
              fontFamily: "'JetBrains Mono', monospace",
              fontWeight: 600,
              whiteSpace: 'nowrap',
            }}
          >
            {label}
          </span>
        );
      })}
    </div>
  );
}

// 格式化时长
export function formatWall(wall?: string) {
  if (!wall || wall === '-1' || wall === 'UNLIMITED' || wall === '') {
    return <span style={{ color: 'var(--text-muted,#94a3b8)' }}>无限制</span>;
  }
  return (
    <span
      style={{
        fontFamily: "'JetBrains Mono', monospace",
        fontSize: '0.8rem',
        fontWeight: 600,
        color: 'var(--text-main,#f1f5f9)',
      }}
    >
      {wall}
    </span>
  );
}

export function QOSPanel() {
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [tenants, setTenants] = useState<TenantInfo[]>([]);
  const [qosList, setQosList] = useState<QOSInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchQ, setSearchQ] = useState('');

  // 弹窗状态
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingQos, setEditingQos] = useState<QOSInfo | null>(null);
  const [deletingQos, setDeletingQos] = useState<QOSInfo | null>(null);
  const [showBindModal, setShowBindModal] = useState(false);

  const loadQos = async () => {
    setLoading(true);
    try {
      const r = await slurm.listQOS();
      setQosList(r.qos || []);
      setError('');
    } catch (e: any) {
      setError(`QOS 读取失败：${e?.message || e}`);
    } finally {
      setLoading(false);
    }
  };

  const loadTenants = async () => {
    try {
      const r = await slurm.listTenants();
      setTenants(r.tenants || []);
    } catch (e: any) {
      // 静默处理租户加载
    }
  };

  useEffect(() => {
    loadQos();
    loadTenants();
  }, []);

  // 过滤后的 QOS 列表
  const filteredQos = qosList.filter((q) => {
    const term = searchQ.trim().toLowerCase();
    if (!term) return true;
    return (
      q.name.toLowerCase().includes(term) ||
      (q.description || '').toLowerCase().includes(term) ||
      (q.priority || '').includes(term)
    );
  });

  // 统计指标计算
  const totalCount = qosList.length;
  const highPrioCount = qosList.filter((q) => Number(q.priority) > 0).length;
  const gpuConstrainedCount = qosList.filter(
    (q) => (q.grp_tres || q.grpTRES || '').includes('gpu') || (q.max_tres_per_user || q.maxTRESPerUser || '').includes('gpu')
  ).length;
  const hasNormal = qosList.some((q) => q.name === 'normal');

  return (
    <div style={{ ...cardStyle, marginTop: '1.5rem', display: 'block' }}>
      {/* 顶部标题与描述 */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '0.75rem', marginBottom: '1rem' }}>
        <div>
          <div style={{ fontSize: '1.1rem', fontWeight: 700, color: 'var(--text-main,#f1f5f9)' }}>
            QOS 策略治理 (Quality of Service)
          </div>
          <div style={{ fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)', marginTop: '0.2rem' }}>
            配置 Slurm 服务质量策略，支持 GPU/CPU TRES 共享与单人上限、并发作业限制、最长运行时长及优先级调度加权。
          </div>
        </div>
        <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
          <button
            type="button"
            className="neu-btn"
            onClick={() => setShowBindModal(true)}
            style={{ fontSize: '0.8rem', padding: '0.35rem 0.8rem' }}
          >
            租户 QOS 绑定
          </button>
          <button
            type="button"
            className="btn-primary"
            onClick={() => setShowCreateModal(true)}
            style={{ fontSize: '0.82rem', padding: '0.4rem 1rem' }}
          >
            + 新建 QOS 策略
          </button>
          <MiniBtn onClick={loadQos} disabled={loading}>
            {loading ? '刷新中' : '刷新'}
          </MiniBtn>
        </div>
      </div>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.12)">{info}</Notice>}

      {/* 4 个指标概览卡片 */}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
          gap: '0.85rem',
          marginBottom: '1.25rem',
        }}
      >
        <div style={{ background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 10, padding: '0.9rem 1rem' }}>
          <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)', marginBottom: '0.3rem' }}>策略总数</div>
          <div style={{ fontSize: '1.35rem', fontWeight: 700, fontFamily: "'JetBrains Mono', monospace" }}>{loading ? '—' : totalCount}</div>
        </div>
        <div style={{ background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 10, padding: '0.9rem 1rem' }}>
          <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)', marginBottom: '0.3rem' }}>高优先级策略 (P &gt; 0)</div>
          <div style={{ fontSize: '1.35rem', fontWeight: 700, fontFamily: "'JetBrains Mono', monospace", color: 'var(--accent-cyan,#06B6D4)' }}>
            {loading ? '—' : highPrioCount}
          </div>
        </div>
        <div style={{ background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 10, padding: '0.9rem 1rem' }}>
          <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)', marginBottom: '0.3rem' }}>GPU 约束策略</div>
          <div style={{ fontSize: '1.35rem', fontWeight: 700, fontFamily: "'JetBrains Mono', monospace", color: 'var(--accent-violet,#A855F7)' }}>
            {loading ? '—' : gpuConstrainedCount}
          </div>
        </div>
        <div style={{ background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 10, padding: '0.9rem 1rem' }}>
          <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)', marginBottom: '0.3rem' }}>标准基准策略</div>
          <div style={{ fontSize: '1.1rem', fontWeight: 700, fontFamily: "'JetBrains Mono', monospace", color: hasNormal ? '#10b981' : '#f59e0b' }}>
            {loading ? '—' : hasNormal ? 'normal (生效中)' : '未初始化'}
          </div>
        </div>
      </div>

      {/* 搜索工具栏 */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
        <input
          className="form-control form-control-sm"
          value={searchQ}
          onChange={(e) => setSearchQ(e.target.value)}
          placeholder="搜索 QOS 名称 / 描述 / 优先级…"
          style={{ maxWidth: 280, width: '100%' }}
        />
        <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)' }}>
          显示 {filteredQos.length} / {qosList.length} 条策略
        </div>
      </div>

      {/* 9 列核心 QOS 列表表格 */}
      {loading ? (
        <div style={emptyStyle}>加载 QOS 策略中…</div>
      ) : filteredQos.length === 0 ? (
        <div style={emptyStyle}>暂无匹配的 QOS 策略</div>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.875rem' }}>
            <thead>
              <tr style={{ textAlign: 'left' }}>
                <th style={th}>QOS 名称</th>
                <th style={{ ...th, textAlign: 'right' }}>调度优先级</th>
                <th style={th}>租户共享上限 (GrpTRES)</th>
                <th style={th}>单用户上限 (MaxTRES/人)</th>
                <th style={th}>单人并发作业</th>
                <th style={th}>单人排队作业</th>
                <th style={th}>单作业最长时长</th>
                <th style={th}>策略描述</th>
                <th style={{ ...th, textAlign: 'center' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {filteredQos.map((q, i) => {
                const isLast = i === filteredQos.length - 1;
                const isNormal = q.name === 'normal';
                const prioNum = Number(q.priority || '0');
                const grpTresVal = q.grp_tres || q.grpTRES;
                const maxTresVal = q.max_tres_per_user || q.maxTRESPerUser || q.max_tres || q.maxTRES;
                const maxJobsVal = q.max_jobs_per_user || q.maxJobsPerUser || q.max_jobs || q.maxJobs;
                const maxSubmitJobsVal = q.max_submit_jobs_per_user || q.maxSubmitJobsPerUser;
                const maxWallVal = q.max_wall_duration || q.maxWallDuration || q.max_wall || q.maxWall;

                return (
                  <tr
                    key={q.name}
                    style={{
                      borderBottom: isLast ? 'none' : '1px solid var(--row-line,#2a2f3a)',
                      transition: 'background-color .15s ease',
                    }}
                  >
                    {/* 1. QOS 名称 */}
                    <td style={{ ...td, ...mono }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: '0.45rem' }}>
                        <span style={{ fontWeight: 700, color: 'var(--text-main,#f1f5f9)' }}>{q.name}</span>
                        {isNormal && (
                          <span
                            style={{
                              fontSize: '0.68rem',
                              padding: '0.1rem 0.35rem',
                              borderRadius: 4,
                              background: 'rgba(6,182,212,0.15)',
                              color: 'var(--accent-cyan,#06B6D4)',
                              fontWeight: 700,
                            }}
                          >
                            默认
                          </span>
                        )}
                      </div>
                    </td>

                    {/* 2. 调度优先级 */}
                    <td style={{ ...td, ...mono, textAlign: 'right' }}>
                      {prioNum > 0 ? (
                        <span style={{ color: prioNum >= 1000 ? '#f59e0b' : 'var(--accent-cyan,#06B6D4)', fontWeight: 700 }}>
                          {q.priority}
                        </span>
                      ) : (
                        <span style={{ color: 'var(--text-muted,#94a3b8)' }}>{q.priority || '0'}</span>
                      )}
                    </td>

                    {/* 3. 租户共享上限 (GrpTRES) */}
                    <td style={td}>{formatTRES(grpTresVal)}</td>

                    {/* 4. 单用户上限 (MaxTRESPerUser) */}
                    <td style={td}>{formatTRES(maxTresVal)}</td>

                    {/* 5. 单人并发作业数 */}
                    <td style={{ ...td, ...mono }}>
                      {maxJobsVal && maxJobsVal !== '-1' && maxJobsVal !== 'UNLIMITED' ? (
                        <span style={{ padding: '0.12rem 0.4rem', borderRadius: 4, background: 'rgba(245,158,11,0.12)', color: '#fbbf24', fontWeight: 600, fontSize: '0.78rem' }}>
                          {maxJobsVal} 作业/人
                        </span>
                      ) : (
                        <span style={{ color: 'var(--text-muted,#94a3b8)' }}>无限制</span>
                      )}
                    </td>

                    {/* 6. 单人排队作业数 */}
                    <td style={{ ...td, ...mono }}>
                      {maxSubmitJobsVal && maxSubmitJobsVal !== '-1' && maxSubmitJobsVal !== 'UNLIMITED' ? (
                        <span style={{ padding: '0.12rem 0.4rem', borderRadius: 4, background: 'rgba(148,163,184,0.12)', color: 'var(--text-main,#f1f5f9)', fontWeight: 600, fontSize: '0.78rem' }}>
                          {maxSubmitJobsVal} 作业/人
                        </span>
                      ) : (
                        <span style={{ color: 'var(--text-muted,#94a3b8)' }}>无限制</span>
                      )}
                    </td>

                    {/* 7. 单作业最长时长 */}
                    <td style={td}>{formatWall(maxWallVal)}</td>

                    {/* 8. 策略描述 */}
                    <td style={{ ...td, maxWidth: 180, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={q.description}>
                      <span style={{ color: q.description ? 'var(--text-muted,#94a3b8)' : 'var(--text-dim,#64748b)', fontSize: '0.8rem' }}>
                        {q.description || '—'}
                      </span>
                    </td>

                    {/* 9. 操作 */}
                    <td style={{ ...td, textAlign: 'center', minWidth: 130 }}>
                      <div style={{ display: 'inline-flex', gap: '0.4rem' }}>
                        <MiniBtn onClick={() => setEditingQos(q)}>编辑</MiniBtn>
                        <button
                          type="button"
                          className="neu-btn"
                          style={{
                            padding: '0.25rem 0.55rem',
                            fontSize: '0.75rem',
                            borderRadius: 6,
                            color: 'var(--accent-rose,#f43f5e)',
                          }}
                          onClick={() => setDeletingQos(q)}
                        >
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* 新建模态框 */}
      {showCreateModal && (
        <CreateQOSModal
          onClose={() => setShowCreateModal(false)}
          onSuccess={(msg) => {
            setInfo(msg);
            setShowCreateModal(false);
            loadQos();
          }}
        />
      )}

      {/* 编辑模态框 */}
      {editingQos && (
        <EditQOSModal
          qos={editingQos}
          onClose={() => setEditingQos(null)}
          onSuccess={(msg) => {
            setInfo(msg);
            setEditingQos(null);
            loadQos();
          }}
        />
      )}

      {/* 删除确认模态框 */}
      {deletingQos && (
        <DeleteQOSModal
          qos={deletingQos}
          onClose={() => setDeletingQos(null)}
          onSuccess={(msg) => {
            setInfo(msg);
            setDeletingQos(null);
            loadQos();
          }}
        />
      )}

      {/* 租户 QOS 绑定模态框 */}
      {showBindModal && (
        <TenantQOSBindModal
          tenants={tenants}
          qosList={qosList}
          onClose={() => setShowBindModal(false)}
          onSuccess={(msg) => {
            setInfo(msg);
            setShowBindModal(false);
          }}
        />
      )}
    </div>
  );
}

// ----------------------------------------------------
// 弹窗子组件体系 (Create / Edit / Delete / Bind Modals)
// ----------------------------------------------------

interface CreateQOSModalProps {
  onClose: () => void;
  onSuccess: (msg: string) => void;
}

function CreateQOSModal({ onClose, onSuccess }: CreateQOSModalProps) {
  const [form, setForm] = useState<CreateQOSRequest>({
    name: '',
    description: '',
    priority: '',
    grpTRES: '',
    maxTRESPerUser: '',
    maxJobsPerUser: '',
    maxSubmitJobsPerUser: '',
    maxWallDuration: '',
  });
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      setErr('QOS 策略名称必填');
      return;
    }
    const namePattern = /^[A-Za-z][A-Za-z0-9_-]{0,31}$/;
    if (!namePattern.test(form.name.trim())) {
      setErr('QOS 名称必须以字母开头，仅允许包含字母、数字、下划线及连字符（最多 32 字符）');
      return;
    }

    setSubmitting(true);
    setErr('');
    try {
      const payload: CreateQOSRequest = {
        name: form.name.trim(),
        description: form.description?.trim() || undefined,
        priority: form.priority?.trim() || undefined,
        grpTRES: form.grpTRES?.trim() || undefined,
        maxTRESPerUser: form.maxTRESPerUser?.trim() || undefined,
        maxJobsPerUser: form.maxJobsPerUser?.trim() || undefined,
        maxSubmitJobsPerUser: form.maxSubmitJobsPerUser?.trim() || undefined,
        maxWallDuration: form.maxWallDuration?.trim() || undefined,
      };
      await slurm.createQOS(payload);
      onSuccess(`QOS 策略 ${form.name.trim()} 已成功创建并同步至 Slurm`);
    } catch (e: any) {
      setErr(e?.message || '创建 QOS 失败');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.65)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1100,
        padding: '1.5rem',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="neu-chiseled-card"
        style={{ maxWidth: 660, width: '100%', maxHeight: '90vh', overflowY: 'auto', padding: '1.75rem' }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.25rem' }}>
          <div>
            <h3 style={{ margin: 0, fontSize: '1.15rem', fontWeight: 700 }}>新建 QOS 调度策略</h3>
            <div style={{ fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)', marginTop: '0.25rem' }}>
              创建新的 Slurm QOS 实体，支持配额限制与调度优先级设置。
            </div>
          </div>
          <MiniBtn onClick={onClose}>关闭</MiniBtn>
        </div>

        {err && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{err}</Notice>}

        <form onSubmit={submit} style={{ display: 'grid', gap: '1.1rem' }}>
          {/* 第一组：基础信息与优先级 */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
            <Field label="QOS 名称 (必填)">
              <input
                className="form-control"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="如 vip-gpu, student-basic"
                required
              />
            </Field>
            <Field label="调度优先级 (Priority)">
              <input
                className="form-control"
                value={form.priority || ''}
                onChange={(e) => setForm({ ...form, priority: e.target.value })}
                placeholder="如 100, 1000 (缺省=0)"
              />
            </Field>
            <Field label="策略说明 (Description)">
              <input
                className="form-control"
                value={form.description || ''}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                placeholder="如 计费 VIP 队列，单人限1卡并发"
              />
            </Field>
          </div>

          {/* 第二组：TRES 资源配额限制 */}
          <div style={{ padding: '0.85rem', borderRadius: 10, background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)' }}>
            <div style={{ fontSize: '0.85rem', fontWeight: 700, marginBottom: '0.5rem', color: 'var(--text-main,#f1f5f9)' }}>
              资源限制 (TRES Limits)
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
              <div>
                <Field label="租户/组共享上限 (GrpTRES)">
                  <input
                    className="form-control"
                    value={form.grpTRES || ''}
                    onChange={(e) => setForm({ ...form, grpTRES: e.target.value })}
                    placeholder="如 gres/gpu=4,cpu=32,mem=64G"
                  />
                </Field>
                <div style={{ display: 'flex', gap: '0.35rem', marginTop: '0.35rem', flexWrap: 'wrap' }}>
                  <span
                    onClick={() => setForm({ ...form, grpTRES: 'gres/gpu=4,cpu=32' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(6,182,212,0.12)', color: 'var(--accent-cyan,#06B6D4)' }}
                  >
                    4 GPU + 32 CPU
                  </span>
                  <span
                    onClick={() => setForm({ ...form, grpTRES: 'gres/gpu=8,cpu=64' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(6,182,212,0.12)', color: 'var(--accent-cyan,#06B6D4)' }}
                  >
                    8 GPU + 64 CPU
                  </span>
                </div>
              </div>

              <div>
                <Field label="单用户上限 (MaxTRESPerUser)">
                  <input
                    className="form-control"
                    value={form.maxTRESPerUser || ''}
                    onChange={(e) => setForm({ ...form, maxTRESPerUser: e.target.value })}
                    placeholder="如 gres/gpu=1,cpu=8,mem=16G"
                  />
                </Field>
                <div style={{ display: 'flex', gap: '0.35rem', marginTop: '0.35rem', flexWrap: 'wrap' }}>
                  <span
                    onClick={() => setForm({ ...form, maxTRESPerUser: 'gres/gpu=1,cpu=8' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(168,85,247,0.12)', color: 'var(--accent-violet,#A855F7)' }}
                  >
                    1 GPU + 8 CPU
                  </span>
                  <span
                    onClick={() => setForm({ ...form, maxTRESPerUser: 'gres/gpu=2,cpu=16' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(168,85,247,0.12)', color: 'var(--accent-violet,#A855F7)' }}
                  >
                    2 GPU + 16 CPU
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* 第三组：作业并发与运行时长限制 */}
          <div style={{ padding: '0.85rem', borderRadius: 10, background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)' }}>
            <div style={{ fontSize: '0.85rem', fontWeight: 700, marginBottom: '0.5rem', color: 'var(--text-main,#f1f5f9)' }}>
              作业并发与时长 (Concurrency &amp; Walltime)
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
              <Field label="单用户并发运行作业 (MaxJobs)">
                <input
                  className="form-control"
                  value={form.maxJobsPerUser || ''}
                  onChange={(e) => setForm({ ...form, maxJobsPerUser: e.target.value })}
                  placeholder="如 1, 3, UNLIMITED"
                />
              </Field>
              <Field label="单用户最大排队作业 (MaxSubmitJobs)">
                <input
                  className="form-control"
                  value={form.maxSubmitJobsPerUser || ''}
                  onChange={(e) => setForm({ ...form, maxSubmitJobsPerUser: e.target.value })}
                  placeholder="如 5, 10, UNLIMITED"
                />
              </Field>
              <div>
                <Field label="单作业最长运行时长 (MaxWall)">
                  <input
                    className="form-control"
                    value={form.maxWallDuration || ''}
                    onChange={(e) => setForm({ ...form, maxWallDuration: e.target.value })}
                    placeholder="如 02:00:00 / 120 / 24:00:00"
                  />
                </Field>
                <div style={{ display: 'flex', gap: '0.35rem', marginTop: '0.35rem', flexWrap: 'wrap' }}>
                  <span
                    onClick={() => setForm({ ...form, maxWallDuration: '01:00:00' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(16,185,129,0.12)', color: '#10b981' }}
                  >
                    1小时
                  </span>
                  <span
                    onClick={() => setForm({ ...form, maxWallDuration: '02:00:00' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(16,185,129,0.12)', color: '#10b981' }}
                  >
                    2小时
                  </span>
                  <span
                    onClick={() => setForm({ ...form, maxWallDuration: '24:00:00' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(16,185,129,0.12)', color: '#10b981' }}
                  >
                    24小时
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* 模态框按钮 */}
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem', marginTop: '0.5rem' }}>
            <button type="button" className="neu-btn" onClick={onClose} disabled={submitting}>
              取消
            </button>
            <button type="submit" className="btn-primary" disabled={submitting} style={{ padding: '0.5rem 1.6rem' }}>
              {submitting ? '创建中…' : '立即创建 QOS'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

interface EditQOSModalProps {
  qos: QOSInfo;
  onClose: () => void;
  onSuccess: (msg: string) => void;
}

function EditQOSModal({ qos, onClose, onSuccess }: EditQOSModalProps) {
  const [form, setForm] = useState<UpdateQOSRequest>({
    description: qos.description || '',
    priority: qos.priority || '',
    grpTRES: qos.grp_tres || qos.grpTRES || '',
    maxTRESPerUser: qos.max_tres_per_user || qos.maxTRESPerUser || qos.max_tres || qos.maxTRES || '',
    maxJobsPerUser: qos.max_jobs_per_user || qos.maxJobsPerUser || qos.max_jobs || qos.maxJobs || '',
    maxSubmitJobsPerUser: qos.max_submit_jobs_per_user || qos.maxSubmitJobsPerUser || '',
    maxWallDuration: qos.max_wall_duration || qos.maxWallDuration || qos.max_wall || qos.maxWall || '',
  });
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setErr('');
    try {
      const payload: UpdateQOSRequest = {
        description: form.description?.trim() || undefined,
        priority: form.priority?.trim() || undefined,
        grpTRES: form.grpTRES?.trim() || undefined,
        maxTRESPerUser: form.maxTRESPerUser?.trim() || undefined,
        maxJobsPerUser: form.maxJobsPerUser?.trim() || undefined,
        maxSubmitJobsPerUser: form.maxSubmitJobsPerUser?.trim() || undefined,
        maxWallDuration: form.maxWallDuration?.trim() || undefined,
      };
      await slurm.updateQOS(qos.name, payload);
      onSuccess(`QOS 策略 ${qos.name} 已成功更新并同步至 Slurm`);
    } catch (e: any) {
      setErr(e?.message || '更新 QOS 失败');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.65)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1100,
        padding: '1.5rem',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="neu-chiseled-card"
        style={{ maxWidth: 660, width: '100%', maxHeight: '90vh', overflowY: 'auto', padding: '1.75rem' }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.25rem' }}>
          <div>
            <h3 style={{ margin: 0, fontSize: '1.15rem', fontWeight: 700 }}>
              编辑 QOS 策略：<span style={{ color: 'var(--accent-cyan,#06B6D4)', fontFamily: "'JetBrains Mono', monospace" }}>{qos.name}</span>
            </h3>
            <div style={{ fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)', marginTop: '0.25rem' }}>
              留空保持原值；输入 <code>-1</code> 或 <code>UNLIMITED</code> 可清除对应限额。
            </div>
          </div>
          <MiniBtn onClick={onClose}>关闭</MiniBtn>
        </div>

        {err && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{err}</Notice>}

        <form onSubmit={submit} style={{ display: 'grid', gap: '1.1rem' }}>
          {/* 第一组：基础信息与优先级 */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
            <Field label="QOS 名称 (底层主键不可变更)">
              <input className="form-control" value={qos.name} disabled style={{ opacity: 0.6, cursor: 'not-allowed' }} />
            </Field>
            <Field label={`调度优先级 (当前: ${qos.priority || '0'})`}>
              <input
                className="form-control"
                value={form.priority || ''}
                onChange={(e) => setForm({ ...form, priority: e.target.value })}
                placeholder="如 100, 1000"
              />
            </Field>
            <Field label={`策略说明 (当前: ${qos.description || '无'})`}>
              <input
                className="form-control"
                value={form.description || ''}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                placeholder="策略备注说明"
              />
            </Field>
          </div>

          {/* 第二组：TRES 资源配额限制 */}
          <div style={{ padding: '0.85rem', borderRadius: 10, background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)' }}>
            <div style={{ fontSize: '0.85rem', fontWeight: 700, marginBottom: '0.5rem', color: 'var(--text-main,#f1f5f9)' }}>
              资源限制 (TRES Limits)
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
              <div>
                <Field label={`租户/组共享上限 (当前: ${qos.grp_tres || qos.grpTRES || '未设置'})`}>
                  <input
                    className="form-control"
                    value={form.grpTRES || ''}
                    onChange={(e) => setForm({ ...form, grpTRES: e.target.value })}
                    placeholder="如 gres/gpu=4,cpu=32,mem=64G (填 -1 清除)"
                  />
                </Field>
                <div style={{ display: 'flex', gap: '0.35rem', marginTop: '0.35rem', flexWrap: 'wrap' }}>
                  <span
                    onClick={() => setForm({ ...form, grpTRES: '-1' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(239,68,68,0.12)', color: 'var(--accent-rose,#f43f5e)' }}
                  >
                    清除限额 (-1)
                  </span>
                </div>
              </div>

              <div>
                <Field label={`单用户上限 (当前: ${qos.max_tres_per_user || qos.maxTRESPerUser || '未设置'})`}>
                  <input
                    className="form-control"
                    value={form.maxTRESPerUser || ''}
                    onChange={(e) => setForm({ ...form, maxTRESPerUser: e.target.value })}
                    placeholder="如 gres/gpu=1,cpu=8,mem=16G (填 -1 清除)"
                  />
                </Field>
                <div style={{ display: 'flex', gap: '0.35rem', marginTop: '0.35rem', flexWrap: 'wrap' }}>
                  <span
                    onClick={() => setForm({ ...form, maxTRESPerUser: '-1' })}
                    style={{ fontSize: '0.7rem', padding: '0.1rem 0.35rem', borderRadius: 4, cursor: 'pointer', background: 'rgba(239,68,68,0.12)', color: 'var(--accent-rose,#f43f5e)' }}
                  >
                    清除限额 (-1)
                  </span>
                </div>
              </div>
            </div>
          </div>

          {/* 第三组：作业并发与运行时长限制 */}
          <div style={{ padding: '0.85rem', borderRadius: 10, background: 'var(--card-bg)', border: '1px solid var(--border-color,#2a2f3a)' }}>
            <div style={{ fontSize: '0.85rem', fontWeight: 700, marginBottom: '0.5rem', color: 'var(--text-main,#f1f5f9)' }}>
              作业并发与时长 (Concurrency &amp; Walltime)
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
              <Field label={`单用户并发作业 (当前: ${qos.max_jobs_per_user || qos.maxJobsPerUser || '未设置'})`}>
                <input
                  className="form-control"
                  value={form.maxJobsPerUser || ''}
                  onChange={(e) => setForm({ ...form, maxJobsPerUser: e.target.value })}
                  placeholder="如 1, 3, UNLIMITED"
                />
              </Field>
              <Field label={`单用户排队作业 (当前: ${qos.max_submit_jobs_per_user || qos.maxSubmitJobsPerUser || '未设置'})`}>
                <input
                  className="form-control"
                  value={form.maxSubmitJobsPerUser || ''}
                  onChange={(e) => setForm({ ...form, maxSubmitJobsPerUser: e.target.value })}
                  placeholder="如 5, 10, UNLIMITED"
                />
              </Field>
              <Field label={`最长运行时长 (当前: ${qos.max_wall_duration || qos.maxWallDuration || qos.max_wall || '未设置'})`}>
                <input
                  className="form-control"
                  value={form.maxWallDuration || ''}
                  onChange={(e) => setForm({ ...form, maxWallDuration: e.target.value })}
                  placeholder="如 02:00:00 / 120 / UNLIMITED"
                />
              </Field>
            </div>
          </div>

          {/* 模态框按钮 */}
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem', marginTop: '0.5rem' }}>
            <button type="button" className="neu-btn" onClick={onClose} disabled={submitting}>
              取消
            </button>
            <button type="submit" className="btn-primary" disabled={submitting} style={{ padding: '0.5rem 1.6rem' }}>
              {submitting ? '保存中…' : '保存修改'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

interface DeleteQOSModalProps {
  qos: QOSInfo;
  onClose: () => void;
  onSuccess: (msg: string) => void;
}

function DeleteQOSModal({ qos, onClose, onSuccess }: DeleteQOSModalProps) {
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState('');

  const isNormal = qos.name === 'normal';

  const handleDelete = async () => {
    setSubmitting(true);
    setErr('');
    try {
      await slurm.deleteQOS(qos.name);
      onSuccess(`QOS 策略 ${qos.name} 已成功从 Slurm 中删除`);
    } catch (e: any) {
      setErr(e?.message || '删除 QOS 失败');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.65)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1100,
        padding: '1.5rem',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="neu-chiseled-card"
        style={{ maxWidth: 480, width: '100%', padding: '1.75rem' }}
      >
        <div style={{ textAlign: 'center', marginBottom: '1.25rem' }}>
          <div
            style={{
              width: 52,
              height: 52,
              borderRadius: '50%',
              background: 'rgba(244,63,94,0.15)',
              color: 'var(--accent-rose,#f43f5e)',
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: '1.5rem',
              marginBottom: '0.75rem',
            }}
          >
            ⚠️
          </div>
          <h3 style={{ margin: 0, fontSize: '1.15rem', fontWeight: 700 }}>确认删除 QOS 策略？</h3>
          <div style={{ marginTop: '0.5rem', fontSize: '0.9rem', color: 'var(--text-main,#f1f5f9)' }}>
            目标策略：<span style={{ fontWeight: 700, fontFamily: "'JetBrains Mono', monospace", color: 'var(--accent-rose,#f43f5e)' }}>{qos.name}</span>
          </div>
        </div>

        {err && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{err}</Notice>}

        <div
          style={{
            fontSize: '0.8rem',
            lineHeight: 1.6,
            color: 'var(--text-muted,#94a3b8)',
            background: 'var(--card-bg)',
            border: '1px solid var(--border-color,#2a2f3a)',
            borderRadius: 8,
            padding: '0.85rem',
            marginBottom: '1.25rem',
          }}
        >
          {isNormal ? (
            <div style={{ color: 'var(--accent-rose,#f43f5e)', fontWeight: 600 }}>
              ⚠️ 警告：<code>normal</code> 是 Slurm 集群的标准默认 QOS。删除可能导致未配置专用 QOS 的用户作业与会话排队失败！
            </div>
          ) : (
            <div>
              删除 QOS 将直接在 SlurmDBD 底层通过 <code>sacctmgr delete qos</code> 执行。若已有租户绑定此 QOS 或作业正在使用，可能影响调度正常运行。
            </div>
          )}
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <button type="button" className="neu-btn" onClick={onClose} disabled={submitting}>
            取消
          </button>
          <button
            type="button"
            onClick={handleDelete}
            disabled={submitting}
            style={{
              padding: '0.5rem 1.4rem',
              borderRadius: 8,
              border: 'none',
              cursor: 'pointer',
              background: 'var(--accent-rose,#f43f5e)',
              color: '#fff',
              fontWeight: 700,
            }}
          >
            {submitting ? '删除中…' : '确认删除'}
          </button>
        </div>
      </div>
    </div>
  );
}

interface TenantQOSBindModalProps {
  tenants: TenantInfo[];
  qosList: QOSInfo[];
  onClose: () => void;
  onSuccess: (msg: string) => void;
}

function TenantQOSBindModal({ tenants, qosList, onClose, onSuccess }: TenantQOSBindModalProps) {
  const [selectedTenant, setSelectedTenant] = useState(tenants[0]?.slug || '');
  // '__clear__' 是特殊占位符，表示"清除绑定"
  const [selectedQos, setSelectedQos] = useState(qosList[0]?.name || 'normal');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState('');
  const [currentBinding, setCurrentBinding] = useState<{ default_qos: string; allowed_qos: string[] } | null>(null);
  const [loadingBinding, setLoadingBinding] = useState(false);

  // 切换租户时自动拉取当前绑定
  useEffect(() => {
    if (!selectedTenant) { setCurrentBinding(null); return; }
    setLoadingBinding(true);
    setCurrentBinding(null);
    slurm.getTenantQOS(selectedTenant)
      .then((res) => setCurrentBinding(res))
      .catch(() => setCurrentBinding(null))
      .finally(() => setLoadingBinding(false));
  }, [selectedTenant]);

  const isClear = selectedQos === '__clear__';

  const handleBind = async (e: FormEvent) => {
    e.preventDefault();
    if (!selectedTenant) {
      setErr('请选择目标租户');
      return;
    }
    setSubmitting(true);
    setErr('');
    try {
      const qosName = isClear ? '' : selectedQos;
      await slurm.setTenantQOS(selectedTenant, qosName);
      if (isClear) {
        onSuccess(`租户 ${selectedTenant} 的 QOS 绑定已成功清除`);
      } else {
        onSuccess(`租户 ${selectedTenant} 默认 QOS 已成功绑定为 ${selectedQos}`);
      }
    } catch (e: any) {
      setErr(e?.message || '操作失败');
    } finally {
      setSubmitting(false);
    }
  };

  const qosOptions = [
    { value: '__clear__', label: '—— 清除绑定（恢复默认）' },
    ...(qosList.length === 0
      ? [{ value: 'normal', label: 'normal' }]
      : qosList.map((q) => ({
          value: q.name,
          label: `${q.name}${q.priority ? ` (P:${q.priority})` : ''}${q.description ? ` · ${q.description}` : ''}`,
        }))),
  ];

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.65)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1100,
        padding: '1.5rem',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="neu-chiseled-card"
        style={{ maxWidth: 480, width: '100%', padding: '1.75rem' }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.25rem' }}>
          <div>
            <h3 style={{ margin: 0, fontSize: '1.15rem', fontWeight: 700 }}>租户默认 QOS 绑定</h3>
            <div style={{ fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)', marginTop: '0.25rem' }}>
              为指定租户设置或清除默认关联的 Slurm QOS 策略。
            </div>
          </div>
          <MiniBtn onClick={onClose}>关闭</MiniBtn>
        </div>

        {err && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{err}</Notice>}

        <form onSubmit={handleBind} style={{ display: 'grid', gap: '1rem' }}>
          <Field label="目标租户">
            <Select
              value={selectedTenant}
              onChange={setSelectedTenant}
              options={
                tenants.length === 0
                  ? [{ value: '', label: '暂无租户' }]
                  : tenants.map((t) => ({ value: t.slug, label: `${t.slug} (${t.name})` }))
              }
            />
          </Field>

          {/* 当前绑定状态提示 */}
          {selectedTenant && (
            <div style={{
              padding: '0.6rem 0.9rem',
              borderRadius: 8,
              background: 'var(--surface-2,rgba(100,116,139,0.1))',
              fontSize: '0.82rem',
              color: 'var(--text-muted,#94a3b8)',
              display: 'flex',
              alignItems: 'center',
              gap: '0.5rem',
            }}>
              {loadingBinding ? (
                <span>查询当前绑定中…</span>
              ) : currentBinding ? (
                currentBinding.default_qos ? (
                  <>
                    <span style={{ color: '#22d3ee' }}>●</span>
                    当前绑定：
                    <strong style={{ color: 'var(--text-primary,#e2e8f0)' }}>{currentBinding.default_qos}</strong>
                    {currentBinding.allowed_qos?.length > 1 && (
                      <span>（允许：{currentBinding.allowed_qos.join('、')}）</span>
                    )}
                  </>
                ) : (
                  <><span style={{ color: '#94a3b8' }}>○</span> 当前未绑定 QOS</>
                )
              ) : (
                <span>暂无绑定信息</span>
              )}
            </div>
          )}

          <Field label={isClear ? '操作' : '绑定默认 QOS 策略'}>
            <Select
              value={selectedQos}
              onChange={setSelectedQos}
              options={qosOptions}
            />
          </Field>

          {isClear && (
            <Notice color="#f59e0b" bg="rgba(245,158,11,.1)">
              清除后该租户将使用 Slurm 集群默认 QOS，已绑定用户的个人 QOS 设置不受影响。
            </Notice>
          )}

          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem', marginTop: '0.5rem' }}>
            <button type="button" className="neu-btn" onClick={onClose} disabled={submitting}>
              取消
            </button>
            <button
              type="submit"
              className="btn-primary"
              disabled={submitting || !selectedTenant}
              style={{
                padding: '0.5rem 1.5rem',
                background: isClear ? 'var(--accent-rose,#f43f5e)' : undefined,
              }}
            >
              {submitting
                ? (isClear ? '清除中…' : '绑定中…')
                : (isClear ? '确认清除绑定' : '保存租户绑定')}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

