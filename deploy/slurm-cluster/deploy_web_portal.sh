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

echo "2. Syncing apiserver binary, config/ & apps/web static assets to ${REMOTE_HOST}:${REMOTE_DIR}..."
ssh -o BatchMode=yes ${REMOTE_HOST} "mkdir -p ${REMOTE_DIR}/bin ${REMOTE_DIR}/apps/web ${REMOTE_DIR}/config"
rsync -avz -e "ssh -o BatchMode=yes" bin/apiserver ${REMOTE_HOST}:${REMOTE_DIR}/bin/apiserver
rsync -avz -e "ssh -o BatchMode=yes" config/   ${REMOTE_HOST}:${REMOTE_DIR}/config/
rsync -avz -e "ssh -o BatchMode=yes" apps/web/  ${REMOTE_HOST}:${REMOTE_DIR}/apps/web/

echo "3. Restarting Web Portal service on remote Slurm server (192.168.20.226:${PORT})..."
echo "   Requires ${REMOTE_DIR}/.env containing AILS_JWT_SECRET (server is fail-closed without it)."
echo "   First-time setup:"
echo "     ssh ${REMOTE_HOST} \"install -m600 /dev/null ${REMOTE_DIR}/.env && printf 'AILS_JWT_SECRET=%s\\\\n' \\\"\$(openssl rand -hex 32)\\\" >> ${REMOTE_DIR}/.env\""
ssh -o BatchMode=yes ${REMOTE_HOST} "
  killall apiserver || true
  chmod +x ${REMOTE_DIR}/bin/apiserver
  cd ${REMOTE_DIR}
  # 加载运行时环境变量（必须含 AILS_JWT_SECRET，否则服务 fail-closed 拒绝启动）
  if [ -f ${REMOTE_DIR}/.env ]; then set -a; . ${REMOTE_DIR}/.env; set +a; fi
  nohup ./bin/apiserver -port ${PORT} > /var/log/slurm-web-portal.log 2>&1 &
  sleep 2
  if ps aux | grep -v grep | grep -q './bin/apiserver'; then
    echo 'apiserver process: running'
  else
    echo '!!! apiserver FAILED TO START — see /var/log/slurm-web-portal.log !!!'
    echo '    (most likely ${REMOTE_DIR}/.env is missing AILS_JWT_SECRET)'
    tail -n 20 /var/log/slurm-web-portal.log || true
    exit 1
  fi
"

echo "=== Slurm Web Portal Deployed Successfully! ==="
echo "Access Portal UI via: http://192.168.20.226:${PORT}/portal/"
