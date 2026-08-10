import { createFileRoute } from '@tanstack/react-router';
import { useState, useEffect } from 'react';

export const Route = createFileRoute('/queues')({
  component: QueuesPage,
});

function QueuesPage() {
  const [queuesData, setQueuesData] = useState<any>(null);

  useEffect(() => {
    fetch('http://192.168.20.226:8090/api/v1/queues')
      .then((r) => r.json())
      .then((d) => setQueuesData(d))
      .catch((e) => console.error(e));
  }, []);

  const clusterQueue = queuesData?.clusterQueues?.[0];
  const localQueue = queuesData?.localQueues?.[0];

  return (
    <div>
      <div className="header-bar">
        <div className="header-title">
          <h1>队列与算力配额</h1>
          <p>Kueue 集群级 ClusterQueue 资源池与 Namespace 租户 LocalQueue 准入状态</p>
        </div>
      </div>

      <div className="grid-stats">
        <div className="stat-card">
          <div className="stat-label">全局 ClusterQueue 名称</div>
          <div className="stat-value font-mono" style={{ fontSize: '1.3rem' }}>
            {clusterQueue?.metadata?.name || 'cluster-queue'}
          </div>
          <div className="stat-subtext">Cohort: 公共全局算力池</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">标称 CPU 核心数上限</div>
          <div className="stat-value font-mono" style={{ fontSize: '1.3rem', color: '#818cf8' }}>
            16 Cores
          </div>
          <div className="stat-subtext">硬性物理配额上限</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">已准入运行作业</div>
          <div className="stat-value font-mono" style={{ fontSize: '1.3rem', color: '#34d399' }}>
            {clusterQueue?.status?.admittedWorkloads || 0} 个作业
          </div>
          <div className="stat-subtext">计算节点占用中</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">队列公平调度策略</div>
          <div className="stat-value font-mono" style={{ fontSize: '1.3rem' }}>
            BestEffortFIFO
          </div>
          <div className="stat-subtext">准入控制引擎</div>
        </div>
      </div>

      <div className="table-card">
        <div className="table-header">
          <h2>已注册租户本地队列 (LocalQueues)</h2>
        </div>
        <table className="custom-table">
          <thead>
            <tr>
              <th>本地队列标识</th>
              <th>所属命名空间 (Namespace)</th>
              <th>绑定的全局 ClusterQueue</th>
              <th>等待队列数 (Pending)</th>
              <th>队列控制状态</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td style={{ fontWeight: 600 }}>{localQueue?.metadata?.name || 'user-queue'}</td>
              <td>{localQueue?.metadata?.namespace || 'default'}</td>
              <td>{localQueue?.spec?.clusterQueue || 'cluster-queue'}</td>
              <td className="font-mono">{localQueue?.status?.pendingWorkloads || 0}</td>
              <td>
                <span className="badge badge-running">Active (正常)</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}
