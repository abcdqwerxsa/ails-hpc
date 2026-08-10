import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';

export const Route = createFileRoute('/tenant')({
  component: TenantPage,
});

function TenantPage() {
  const [tenants] = useState([
    {
      orgName: '高性能计算实验室',
      orgSlug: 'hpc-lab',
      k8sNamespace: 'hpc-tenant-hpc-lab',
      localQueue: 'queue-hpc-lab',
      role: '创建者 (Owner)',
      members: 5,
      consumedCoreHours: 12.45,
      allocatedStorage: '50Gi',
    },
    {
      orgName: '大模型分布式训练组',
      orgSlug: 'ai-group',
      k8sNamespace: 'hpc-tenant-ai-group',
      localQueue: 'queue-ai-group',
      role: '管理员 (Admin)',
      members: 12,
      consumedCoreHours: 184.20,
      allocatedStorage: '500Gi',
    },
    {
      orgName: '生物基因计算课题组',
      orgSlug: 'bio-lab',
      k8sNamespace: 'hpc-tenant-bio-lab',
      localQueue: 'queue-bio-lab',
      role: '研究员 (Member)',
      members: 8,
      consumedCoreHours: 45.10,
      allocatedStorage: '200Gi',
    },
  ]);

  return (
    <div>
      <div className="header-bar">
        <div className="header-title">
          <h1>多租户、组织隔离与算力账单计量</h1>
          <p>基于 Better-Auth 的多租户团队隔离、Kubernetes Namespace 映射与算力核时计量明细</p>
        </div>
      </div>

      <div className="table-card">
        <div className="table-header">
          <h2>活跃团队组织与算力消耗账单 (Tenant Accounting)</h2>
        </div>
        <table className="custom-table">
          <thead>
            <tr>
              <th>组织团队名称</th>
              <th>组织标识 (Slug)</th>
              <th>映射 K8s Namespace</th>
              <th>分配的算力队列</th>
              <th>累计算力核时 (Core-Hours)</th>
              <th>存储配额 (Local-Path PVC)</th>
              <th>当前用户角色</th>
            </tr>
          </thead>
          <tbody>
            {tenants.map((t) => (
              <tr key={t.orgSlug}>
                <td style={{ fontWeight: 600 }}>{t.orgName}</td>
                <td>
                  <code className="font-mono">{t.orgSlug}</code>
                </td>
                <td>
                  <code className="font-mono">{t.k8sNamespace}</code>
                </td>
                <td>{t.localQueue}</td>
                <td className="font-mono" style={{ color: 'var(--accent-primary)', fontWeight: 600 }}>
                  {t.consumedCoreHours.toFixed(2)} 核时
                </td>
                <td className="font-mono">{t.allocatedStorage}</td>
                <td>
                  <span className="badge badge-succeeded">{t.role}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
