#!/bin/bash
set -e

REMOTE_HOST="root@192.168.20.226"
REMOTE_DIR="/opt/slurm-cluster"

echo "=== Deploying Slurm Web Portal to Remote Server (${REMOTE_HOST}) ==="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$SCRIPT_DIR"

echo "1. Building Linux x86_64 apiserver binary..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/apiserver ./cmd/apiserver

echo "2. Syncing apiserver binary & apps/web static assets to ${REMOTE_HOST}:${REMOTE_DIR}..."
ssh -o BatchMode=yes ${REMOTE_HOST} "mkdir -p ${REMOTE_DIR}/bin ${REMOTE_DIR}/apps/web"
rsync -avz -e "ssh -o BatchMode=yes" bin/apiserver ${REMOTE_HOST}:${REMOTE_DIR}/bin/apiserver
rsync -avz -e "ssh -o BatchMode=yes" apps/web/ ${REMOTE_HOST}:${REMOTE_DIR}/apps/web/

echo "3. Restarting Web Portal service on remote Slurm server (192.168.20.226:8090)..."
ssh -o BatchMode=yes ${REMOTE_HOST} "
  killall apiserver || true
  chmod +x ${REMOTE_DIR}/bin/apiserver
  cd ${REMOTE_DIR}
  nohup ./bin/apiserver -port 8090 > /var/log/slurm-web-portal.log 2>&1 &
  sleep 2
  ps aux | grep apiserver | grep -v grep
"

echo "=== Slurm Web Portal Deployed Successfully! ==="
echo "Access Portal UI via: http://192.168.20.226:8090/portal/"
