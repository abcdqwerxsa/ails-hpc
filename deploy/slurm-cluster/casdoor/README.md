# Casdoor SSO（生产部署源）

Casdoor v3.154.4（自建镜像 `quay.io/nilpo1/casdoor:v3.154.4`）的部署定义，2026-08-19 上生产。

- `docker-compose.yml`：端口 8000、`conf/` 与 `data/` 挂载、健康检查（`bash -c` 包裹——CMD-SHELL 走 /bin/sh 不支持 /dev/tcp）。
- `conf/app.conf`：权威配置（SQLite、`origin=https://sso.nilpo.app`——OIDC issuer 锚点（Cloudflare Tunnel 域名形态，2026-08-20 起；管理可仍走本机 IP）、prod 模式、国内网络去 CDN/socks5）。
- **敏感项不在本目录**：门户对接凭据在服务器 `/opt/slurm-cluster/.env`（AILS_OIDC_*），Casdoor admin 口令在 `/opt/slurm-cluster/casdoor/.admin-creds`（均不入仓）。
- 运维：`ssh root@192.168.20.226 'cd /opt/slurm-cluster/casdoor && docker compose {up -d,restart,logs}'`。
- 部署同步：deploy_web_portal.sh 会把本目录（不含 data/）rsync 到服务器；**data/（sqlite 库）只在服务器侧**，备份走 ails-db-backup.timer。
- Casdoor 侧用户/群组管理入口：https://sso.nilpo.app/login/built-in（admin；本机 IP http://192.168.20.226:8000/login/built-in 亦可）。对外经 Cloudflare Tunnel（portal/sso.nilpo.app → localhost:8090/8000，挂在既有 a1000-vllm 隧道的 Public Hostname）。门户用户群组命名规范 `<租户slug>-<角色>` 见门户侧映射配置。
