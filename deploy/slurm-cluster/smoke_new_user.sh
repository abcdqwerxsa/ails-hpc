#!/bin/bash
# v5-H1 新用户全链路冒烟（在 192.168.20.226 上执行，或经 ssh 传入——同 verify_rbac.sh 教义）。
# 走完新用户旅程的每一层，任何一层坏当场 FAIL：
#   面板建号(must_change=1) → 首登被 403 拦(A1) → 自助改密 → 旧令牌吊销 →
#   提交作业 → COMPLETED → mini IDE 拉起(Jupyter 真启动) → 会话回收 →
#   计费可查(sacct) → 禁用留痕 → 登录被拒
# 覆盖的坑层：sacct 实体 / 容器 POSIX / slurmctld assoc 缓存 / /shared 运行时目录权限
#            / 首登改密策略 / 作业-IDE-计费三链
# 副作用受控：smoke-<时间戳> 账号（结束禁用留痕，不删号）+ hostname 作业 + ≤5 分钟 mini IDE。
set -u
API=http://127.0.0.1:8090/api/v1
PASS=0; FAIL=0
ok()  { echo "  ✓ $1"; PASS=$((PASS+1)); }
bad() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else bad "$1 want=$2 got=$3"; fi }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)" 2>/dev/null; }

TS=$(date +%m%d%H%M)
U="smoke-$TS"
PW1="Init-$TS-Aa1!"      # 初始密码（四类字符满足策略）
PW2="New-$TS-Bb2@?"      # 首登改密后的密码

echo "== 0) admin 登录 + 面板建号（users:create → provisioner 三层供给）=="
TOKEN=$(curl -s -X POST $API/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin123"}' | jget 'd["token"]')
[ -n "$TOKEN" ] && ok "admin login" || bad "admin login"
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/admin/users -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"role\":\"member\",\"tenantSlug\":\"hpc-lab\",\"password\":\"$PW1\"}")
check "create $U" "200" "$C"

echo "== 1) 首登强制改密（A1）=="
UT=$(curl -s -X POST $API/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"$PW1\"}" | jget 'd["token"]')
[ -n "$UT" ] && ok "first login" || bad "first login"
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/slurm/jobs/submit -H "Authorization: Bearer $UT" \
  -H 'Content-Type: application/json' -d '{"name":"blocked","script":"#!/bin/bash\ntrue"}')
check "must_change blocks submit" "403" "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/auth/password -H "Authorization: Bearer $UT" \
  -H 'Content-Type: application/json' -d "{\"oldPassword\":\"$PW1\",\"newPassword\":\"$PW2\"}")
check "self password change" "200" "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' $API/auth/me -H "Authorization: Bearer $UT")
check "old token revoked by change" "401" "$C"
UT=$(curl -s -X POST $API/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"$PW2\"}" | jget 'd["token"]')
[ -n "$UT" ] && ok "login with new password" || bad "login with new password"

echo "== 2) 提交作业 → COMPLETED（assoc/POSIX/输出目录层）=="
JOB=$(curl -s -X POST $API/slurm/jobs/submit -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' \
  -d '{"name":"smoke-hostname","partition":"standard","time_limit":"5","script":"#!/bin/bash\nhostname\n"}' | jget 'd["job_id"]')
[ -n "$JOB" ] && ok "submit job ($JOB)" || bad "submit job"
STATE=""
for i in $(seq 1 30); do
  STATE=$(curl -s $API/slurm/jobs/$JOB/detail -H "Authorization: Bearer $UT" | jget 'd["state"].upper()' 2>/dev/null)
  case "$STATE" in COMPLETED|FAILED|CANCELLED|TIMEOUT) break;; esac
  sleep 3
done
check "job $JOB final state" "COMPLETED" "${STATE:-UNKNOWN}"

echo "== 3) mini IDE 拉起（Jupyter 真启动 + 回收）=="
SID=$(curl -s -X POST $API/slurm/containers/launch -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' \
  -d '{"env_type":"jupyter","time_limit_min":5}' | jget 'd["container_id"]')
[ -n "$SID" ] && ok "launch session ($SID)" || bad "launch session"
IDE_UP=""
for i in $(seq 1 20); do
  if docker exec slurmctld cat "/shared/sessions/$SID.log" 2>/dev/null | grep -q "is running at"; then IDE_UP=yes; break; fi
  sleep 3
done
[ -n "$IDE_UP" ] && ok "jupyter server up" || bad "jupyter server up (log: $(docker exec slurmctld tail -3 /shared/sessions/$SID.log 2>/dev/null | tr '\n' ' '))"
C=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE $API/slurm/containers/$SID -H "Authorization: Bearer $UT")
check "recycle session" "200" "$C"

echo "== 4) 计费可查（sacct 层）=="
JOBS_N=$(curl -s "$API/slurm/billing/usage" -H "Authorization: Bearer $UT" | jget 'd["job_count"]')
[ "${JOBS_N:-0}" -ge 1 ] 2>/dev/null && ok "billing sees job_count=$JOBS_N" || bad "billing job_count=$JOBS_N"

echo "== 5) 禁用留痕 → 登录被拒 =="
C=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH $API/admin/users/$U -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"status":"disabled"}')
check "disable $U" "200" "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"$PW2\"}")
check "disabled login rejected" "401" "$C"

echo
echo "PASS=$PASS FAIL=$FAIL"
[ $FAIL -eq 0 ]
