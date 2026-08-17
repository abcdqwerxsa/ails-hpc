#!/bin/bash
set -e

REMOTE_HOST="root@192.168.20.226"
REMOTE_DIR="/opt/slurm-cluster"
PORT="${PORT:-8090}"

echo "=== Deploying Slurm Web Portal to Remote Server (${REMOTE_HOST}) ==="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$SCRIPT_DIR"

echo "1. Building Linux x86_64 apiserver binary..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/apiserver ./cmd/apiserver

echo "2. Syncing apiserver binary, config/, apps/web static assets & systemd unit to ${REMOTE_HOST}:${REMOTE_DIR}..."
ssh -o BatchMode=yes ${REMOTE_HOST} "mkdir -p ${REMOTE_DIR}/bin ${REMOTE_DIR}/apps/web ${REMOTE_DIR}/config"
rsync -avz -e "ssh -o BatchMode=yes" bin/apiserver ${REMOTE_HOST}:${REMOTE_DIR}/bin/apiserver
# config/ 里有被容器**单文件 bind-mount** 的集群配置（slurm.conf 等）：必须 --inplace 原地写，
# 否则 rsync 的"临时文件+改名"会让宿主路径指向新 inode，容器仍挂着旧 inode（改了不生效）。
rsync -avz --inplace -e "ssh -o BatchMode=yes" config/   ${REMOTE_HOST}:${REMOTE_DIR}/config/
rsync -avz --exclude node_modules -e "ssh -o BatchMode=yes" apps/web/  ${REMOTE_HOST}:${REMOTE_DIR}/apps/web/
rsync -avz -e "ssh -o BatchMode=yes" deploy/slurm-cluster/ails-apiserver.service \
    ${REMOTE_HOST}:/etc/systemd/system/ails-apiserver.service
# 2.4 用户库每日备份 timer（sqlite3 在线快照，7 份轮转按星期）
rsync -avz -e "ssh -o BatchMode=yes" deploy/slurm-cluster/ails-db-backup.service deploy/slurm-cluster/ails-db-backup.timer \
    ${REMOTE_HOST}:/etc/systemd/system/

echo "3. Installing systemd service on remote Slurm server (192.168.20.226:${PORT})..."
echo "   Requires ${REMOTE_DIR}/.env containing AILS_JWT_SECRET (server is fail-closed without it)."
echo "   First-time setup:"
echo "     ssh ${REMOTE_HOST} \"install -m600 /dev/null ${REMOTE_DIR}/.env && printf 'AILS_JWT_SECRET=%s\\\\n' \\\"\$(openssl rand -hex 32)\\\" >> ${REMOTE_DIR}/.env\""
ssh -o BatchMode=yes -o ServerAliveInterval=15 ${REMOTE_HOST} "
  # 不用 set -e：任一步失败都继续到末尾验证块给出明确诊断（之前 set -e 在中途断过，留 unit inactive）
  # 1) 一次性确保专用系统用户 ails（最小权限运行 apiserver）
  id ails >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin ails

  # 2) 归属：binary/config/web 给 ails 可读；.env（含 JWT secret）仅 ails 可读
  chmod +x ${REMOTE_DIR}/bin/apiserver
  chown -R ails:ails ${REMOTE_DIR}
  if [ -f ${REMOTE_DIR}/.env ]; then chown ails:ails ${REMOTE_DIR}/.env && chmod 600 ${REMOTE_DIR}/.env; fi

  # 2.5) 清掉前端构建期产物。node_modules 不该上生产；之前误传会让下面 chown -R
  #      极慢（数万文件）→ ssh 超时 → 重启那步没跑完，unit 留在 inactive（两次宕机的真因）。
  rm -rf ${REMOTE_DIR}/apps/web/node_modules 2>/dev/null || true

  # 3) systemd 原子重启。systemd 已托管 unit，无需手动 stop/pkill——那会在重启间隙留空窗。
  chmod 644 /etc/systemd/system/ails-apiserver.service
  mkdir -p ${REMOTE_DIR}/var/lib/ails/backups && chown -R ails:ails ${REMOTE_DIR}/var/lib/ails
  systemctl daemon-reload
  systemctl enable ails-apiserver >/dev/null 2>&1 || true
  systemctl restart ails-apiserver
  systemctl enable --now ails-db-backup.timer >/dev/null 2>&1 || true

  # 4) 验证：is-active + /healthz 探活；失败则打印 journalctl 排障
  if systemctl is-active --quiet ails-apiserver && curl -fsS http://127.0.0.1:${PORT}/healthz >/dev/null; then
    echo 'apiserver: active (systemd, user=ails)'
  else
    echo '!!! apiserver FAILED TO START !!!'
    echo '--- last 30 journal lines ---'
    journalctl -u ails-apiserver -n 30 --no-pager || true
    echo '    (most likely ${REMOTE_DIR}/.env is missing AILS_JWT_SECRET, or curl not installed on remote)'
    exit 1
  fi
"

echo "=== Slurm Web Portal Deployed Successfully! ==="
echo "Manage with:  ssh ${REMOTE_HOST} 'systemctl {status,restart,stop} ails-apiserver'"
echo "Logs:         ssh ${REMOTE_HOST} 'journalctl -u ails-apiserver -f'"
echo "Access Portal UI via: http://192.168.20.226:${PORT}/portal/"
