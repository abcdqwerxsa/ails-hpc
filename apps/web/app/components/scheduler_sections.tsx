// 调度管理页的预约与 QOS 面板（2026-08-19 IA 重组自 admin.tsx 迁出，逻辑不变，
// 状态自持）。分区面板仍在 routes/scheduler.tsx 内（原 partitions.tsx 整体迁入）。
import { useEffect, useState, type ChangeEvent } from 'react';
import { slurm, type QOSInfo, type Reservation, type TenantInfo } from '../services/slurm';
import { Field, MiniBtn, StatusBadge, cardStyle, emptyStyle, mono, th, td } from './panel_ui';

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

export function QOSPanel() {
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [tenants, setTenants] = useState<TenantInfo[]>([]);
  const [qosList, setQosList] = useState<QOSInfo[]>([]);
  const [qosForm, setQosForm] = useState({ name: '', grpTRES: '' });
  const [qosBindTenant, setQosBindTenant] = useState('');
  const [qosBindName, setQosBindName] = useState('');

  const loadQos = async () => {
    try {
      const r = await slurm.listQOS();
      setQosList(r.qos || []);
      if (r.qos?.length && !qosBindName) setQosBindName(r.qos[0].name);
    } catch (e: any) {
      setError(`QOS 读取失败：${e?.message || e}`);
    }
  };
  const loadTenants = async () => {
    try {
      const r = await slurm.listTenants();
      setTenants(r.tenants || []);
      if (r.tenants?.length && !qosBindTenant) setQosBindTenant(r.tenants[0].slug);
    } catch (e: any) {
      setError(`租户列表读取失败：${e?.message || e}`);
    }
  };
  const submitQos = async () => {
    setError(''); setInfo('');
    try {
      await slurm.createQOS(qosForm.name.trim(), qosForm.grpTRES.trim() || undefined);
      setInfo(`QOS ${qosForm.name.trim()} 已创建`);
      setQosForm({ name: '', grpTRES: '' });
      await loadQos();
    } catch (e: any) {
      setError(`创建 QOS 失败：${e?.message || e}`);
    }
  };
  const bindQos = async () => {
    setError(''); setInfo('');
    try {
      await slurm.setTenantQOS(qosBindTenant, qosBindName);
      setInfo(`租户 ${qosBindTenant} 已绑定 QOS ${qosBindName}`);
    } catch (e: any) {
      setError(`绑定失败：${e?.message || e}`);
    }
  };
  useEffect(() => { loadQos(); loadTenants(); }, []);

  return (
    <div style={{ ...cardStyle, marginTop: '1.5rem', display: 'block' }}>
      <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.75rem' }}>QOS 管理</div>
      {error && <div style={{ padding: '0.5rem 0.7rem', background: 'rgba(239,68,68,.1)', color: 'var(--accent-rose)', borderRadius: 6, fontSize: '0.8rem', marginBottom: '0.8rem' }}>{error}</div>}
      {info && <div style={{ padding: '0.5rem 0.7rem', background: 'rgba(16,185,129,.1)', color: '#10b981', borderRadius: 6, fontSize: '0.8rem', marginBottom: '0.8rem' }}>{info}</div>}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(160px,1fr))', gap: '0.75rem', marginBottom: '0.75rem' }}>
        <Field label="QOS 名">
          <input className="form-control" value={qosForm.name} onChange={(e) => setQosForm({ ...qosForm, name: e.target.value })} placeholder="high-prio" />
        </Field>
        <Field label="GrpTRES(可选)">
          <input className="form-control" value={qosForm.grpTRES} onChange={(e) => setQosForm({ ...qosForm, grpTRES: e.target.value })} placeholder="cpu=32,mem=64G" />
        </Field>
        <Field label="绑定租户">
          <select
            className="form-control"
            value={qosBindTenant}
            onChange={(e: ChangeEvent<HTMLSelectElement>) => setQosBindTenant(e.target.value)}
          >
            {tenants.map((t) => (
              <option key={t.slug} value={t.slug}>{t.slug}</option>
            ))}
          </select>
        </Field>
        <Field label="绑定 QOS">
          <select
            className="form-control"
            value={qosBindName}
            onChange={(e: ChangeEvent<HTMLSelectElement>) => setQosBindName(e.target.value)}
          >
            {(qosList.length > 0 ? qosList.map((q) => q.name) : []).map((n) => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
        </Field>
        <div style={{ display: 'flex', alignItems: 'end', gap: '0.5rem' }}>
          <button className="btn-primary" type="button" onClick={submitQos} style={{ padding: '0.45rem 1.2rem' }}>创建 QOS</button>
          <button className="btn-primary" type="button" onClick={bindQos} style={{ padding: '0.45rem 1.2rem' }}>绑定</button>
          <MiniBtn onClick={loadQos}>刷新</MiniBtn>
        </div>
      </div>
      {qosList.length === 0 ? (
        <div style={emptyStyle}>暂无 QOS</div>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
            <thead>
              <tr style={{ textAlign: 'left' }}>
                <th style={th}>名称</th><th style={th}>优先级</th><th style={th}>GrpTRES</th>
                <th style={th}>MaxTRES</th><th style={th}>MaxWall</th>
              </tr>
            </thead>
            <tbody>
              {qosList.map((q, i) => (
                <tr key={q.name} style={{ borderBottom: i === qosList.length - 1 ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                  <td style={{ ...td, ...mono, fontWeight: 700 }}>{q.name}</td>
                  <td style={{ ...td, ...mono, textAlign: 'right' }}>{q.priority || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.8rem' }}>{q.grp_tres || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.8rem' }}>{q.max_tres || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.8rem' }}>{q.max_wall || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
