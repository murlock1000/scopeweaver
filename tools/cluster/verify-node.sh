#!/usr/bin/env bash
set -euo pipefail
ENV_FILE=${SCOPEWEAVER_EXECUTOR_ENV:-/etc/scopeweaver-executor/executor.env}
[[ -r $ENV_FILE ]] || { echo "executor environment not found: $ENV_FILE" >&2; exit 1; }
env_value(){ sed -n "s/^$1=//p" "$ENV_FILE" | tail -n 1; }
SCOPEWEAVER_WIREGUARD_IP=$(env_value SCOPEWEAVER_WIREGUARD_IP)
SCOPEWEAVER_SHARED_PATH=$(env_value SCOPEWEAVER_SHARED_PATH)
SCOPEWEAVER_EGRESS_IP=$(env_value SCOPEWEAVER_EGRESS_IP)
ARTEX_LLM_PROXY=$(env_value ARTEX_LLM_PROXY)
checks=0
ok(){ printf '%-32s ok\n' "$1"; }; bad(){ printf '%-32s FAILED\n' "$1"; checks=$((checks+1)); }
ip -brief address | grep -Fq "$SCOPEWEAVER_WIREGUARD_IP" && ok "WireGuard address" || bad "WireGuard address"
mountpoint -q "$SCOPEWEAVER_SHARED_PATH" && ok "Shared storage mount" || bad "Shared storage mount"
systemctl is-active --quiet scopeweaver-executor.service && ok "Executor service" || bad "Executor service"
docker compose -f "$(dirname "$0")/docker-compose.executor.yml" ps --status running --quiet | grep -q . && ok "Worker container" || bad "Worker container"
ip_now=$(curl -fsS --max-time 15 --proxy "$ARTEX_LLM_PROXY" https://api.ipify.org || true)
[[ -n $ip_now && $ip_now == "$SCOPEWEAVER_EGRESS_IP" ]] && ok "NordVPN LLM egress" || bad "NordVPN LLM egress"
[[ $checks -eq 0 ]] || exit 1
