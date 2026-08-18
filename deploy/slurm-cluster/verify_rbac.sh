#!/bin/bash
# 维护期 v2 交付的生产验证脚本（在 192.168.20.226 上执行，或经 ssh 传入）。
# 只读为主；角色 create→delete 全程可回退；不触碰既有账号凭证。
set -u
API=http://127.0.0.1:8090/api/v1
PASS=0; FAIL=0
ok()   { echo "  ✓ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✗ $1"; FAIL=$((FAIL+1)); }
check() { # name expected actual
  if [ "$2" = "$3" ]; then ok "$1 ($3)"; else bad "$1 want=$2 got=$3"; fi
}

echo "== 0) 进程与迁移 =="
check "healthz" "ok" "$(curl -s $API/../../healthz | python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])' 2>/dev/null)"
MIG=$(python3 -c "
import sqlite3;db=sqlite3.connect('file:/opt/slurm-cluster/var/lib/ails/ails.db?mode=ro',uri=True)
print(','.join(str(r[0]) for r in db.execute('SELECT version FROM schema_migrations ORDER BY version')))")
check "migrations" "1,2,3,4,5" "$MIG"

echo "== 1) 登录与 /auth/me =="
LOGIN=$(curl -s -X POST $API/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin123"}')
TOKEN=$(echo "$LOGIN" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))' 2>/dev/null)
[ -n "$TOKEN" ] && ok "admin login 200 + token" || bad "admin login"
ME=$(curl -s $API/auth/me -H "Authorization: Bearer $TOKEN")
echo "$ME" | grep -q '"roles:manage"' && ok "me has roles:manage" || bad "me roles:manage"
echo "$ME" | grep -q '"authSource"' && ok "me authSource" || bad "me authSource"
curl -s -o /dev/null -w '%{http_code}' $API/auth/me/sessions -H "Authorization: Bearer $TOKEN" | grep -q 200 && ok "sessions endpoint" || bad "sessions endpoint"

echo "== 2) 权限门（member vs admin 面）=="
MTOKEN=$(curl -s -X POST $API/auth/login -H 'Content-Type: application/json' -d '{"username":"member","password":"member123"}' | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
check "member nodes read" "200" "$(curl -s -o /dev/null -w '%{http_code}' $API/slurm/nodes -H "Authorization: Bearer $MTOKEN")"
check "member drain denied" "403" "$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/slurm/nodes/node1/state -H "Authorization: Bearer $MTOKEN" -H 'Content-Type: application/json' -d '{"state":"DRAIN"}')"
check "member roles api denied" "403" "$(curl -s -o /dev/null -w '%{http_code}' $API/admin/roles -H "Authorization: Bearer $MTOKEN")"
# 分区管理（partitions:manage）：member 403；admin 空体 400（无副作用——不触 scontrol）
check "member partitions api denied" "403" "$(curl -s -o /dev/null -w '%{http_code}' -X PATCH $API/admin/partitions/debug -H "Authorization: Bearer $MTOKEN" -H 'Content-Type: application/json' -d '{"state":"DOWN"}')"
check "admin partitions empty update 400" "400" "$(curl -s -o /dev/null -w '%{http_code}' -X PATCH $API/admin/partitions/debug -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{}')"
# 平台用户生命周期（users:manage）：member 403；admin 只读目录 200；自禁用/弱重置 400（均无副作用）
check "member users api denied" "403" "$(curl -s -o /dev/null -w '%{http_code}' $API/admin/users -H "Authorization: Bearer $MTOKEN")"
check "admin users dir 200" "200" "$(curl -s -o /dev/null -w '%{http_code}' $API/admin/users -H "Authorization: Bearer $TOKEN")"
check "admin self disable 400" "400" "$(curl -s -o /dev/null -w '%{http_code}' -X PATCH $API/admin/users/admin -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"status":"disabled"}')"
check "admin weak reset 400" "400" "$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/admin/users/member/password -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"newPassword":"weak"}')"

echo "== 3) 内置角色 seed =="
ROLES=$(curl -s $API/admin/roles -H "Authorization: Bearer $TOKEN")
for r in admin ops_admin tenant_admin member; do
  echo "$ROLES" | grep -q "\"name\":\"$r\"" && ok "system role $r" || bad "system role $r"
done
check "system role delete denied" "409" "$(curl -s -o /dev/null -w '%{http_code}' -X DELETE $API/admin/roles/admin -H "Authorization: Bearer $TOKEN")"

echo "== 4) 自定义角色 lifecycle（create → verify → delete）=="
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/admin/roles -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"smoke-auditor","description":"deploy smoke","permissions":["audit:read","tenants:read"]}')
check "role create" "200" "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/admin/roles -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"smoke-esc","permissions":["jobs:submit"]}')
check "role escalation denied" "400" "$C"
C=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE $API/admin/roles/smoke-auditor -H "Authorization: Bearer $TOKEN")
check "role delete" "200" "$C"

echo "== 5) 密码策略 =="
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST $API/admin/users -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"username":"weakpw","role":"ops_admin","tenantSlug":"system","password":"weak"}')
check "weak password rejected" "400" "$C"

echo "== 6) OIDC 未配置面 =="
curl -s $API/auth/oidc/config | grep -q '"enabled":false' && ok "oidc config disabled (no IdP in prod)" || bad "oidc config"
check "oidc login unconfigured" "400" "$(curl -s -o /dev/null -w '%{http_code}' $API/auth/oidc/login)"

echo "== 7) 审计（A2）=="
AUD=$(curl -s "$API/admin/audit?action=auth.login&limit=5" -H "Authorization: Bearer $TOKEN")
echo "$AUD" | grep -q '"actor":"admin"' && ok "auth.login audited" || bad "auth.login audited"

echo "== 8) 前端 =="
curl -s http://127.0.0.1:8090/portal/ | grep -q 'assets/index' && ok "portal served" || bad "portal served"

echo
echo "PASS=$PASS FAIL=$FAIL"
[ $FAIL -eq 0 ]
