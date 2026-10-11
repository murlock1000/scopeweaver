#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: sudo ./tools/cluster/join-node.sh \
  --node-id alice-01 --owner alice --wireguard-ip 10.77.0.12 \
  --dsn-file /root/scopeweaver.dsn --shared-source 10.77.0.1:/srv/scopeweaver-shared \
  --llm-key-file /root/ollama.key --llm-proxy http://127.0.0.1:3128 \
  [--model model-name] [--slots 4] [--capabilities worker,browser,scopesentry]

The WireGuard interface and the NordVPN-backed HTTP/SOCKS proxy must already be
running. The API key is read from a file so it never appears in argv or logs.
EOF
}

NODE_ID= OWNER= WG_IP= DSN_FILE= SHARED_SOURCE= KEY_FILE= LLM_PROXY=
MODEL=${SCOPEWEAVER_LLM_MODEL:-deepseek-v4.1-flash}
BASE_URL=${SCOPEWEAVER_LLM_BASE_URL:-https://ollama.com/v1}
SLOTS=1 CAPABILITIES=worker SHARED_PATH=/srv/scopeweaver-shared
while (($#)); do
  case "$1" in
    --node-id) NODE_ID=$2; shift 2;; --owner) OWNER=$2; shift 2;;
    --wireguard-ip) WG_IP=$2; shift 2;; --dsn-file) DSN_FILE=$2; shift 2;;
    --shared-source) SHARED_SOURCE=$2; shift 2;; --llm-key-file) KEY_FILE=$2; shift 2;;
    --llm-proxy) LLM_PROXY=$2; shift 2;; --model) MODEL=$2; shift 2;;
    --base-url) BASE_URL=$2; shift 2;; --slots) SLOTS=$2; shift 2;;
    --capabilities) CAPABILITIES=$2; shift 2;; --shared-path) SHARED_PATH=$2; shift 2;;
    -h|--help) usage; exit 0;; *) echo "Unknown argument: $1" >&2; usage; exit 2;;
  esac
done
for pair in NODE_ID OWNER WG_IP DSN_FILE SHARED_SOURCE KEY_FILE LLM_PROXY; do
  [[ -n ${!pair} ]] || { echo "Missing ${pair,,}" >&2; usage; exit 2; }
done
[[ -r $KEY_FILE ]] || { echo "Cannot read LLM key file" >&2; exit 1; }
[[ -r $DSN_FILE ]] || { echo "Cannot read PostgreSQL DSN file" >&2; exit 1; }
[[ $SLOTS =~ ^[1-9][0-9]*$ ]] || { echo "slots must be positive" >&2; exit 2; }
command -v docker >/dev/null || { echo "Docker is required" >&2; exit 1; }
docker compose version >/dev/null
ip -brief address | grep -Fq "$WG_IP" || { echo "WireGuard IP $WG_IP is not present on this host" >&2; exit 1; }

install -d -m 0700 /etc/scopeweaver-executor
install -m 0600 "$DSN_FILE" /etc/scopeweaver-executor/pg-dsn
install -m 0600 "$KEY_FILE" /etc/scopeweaver-executor/llm-api-key
{ printf 'Authorization: Bearer '; cat "$KEY_FILE"; } >/etc/scopeweaver-executor/llm-auth-header
chmod 0600 /etc/scopeweaver-executor/llm-auth-header
install -d -m 0750 "$SHARED_PATH"
if ! mountpoint -q "$SHARED_PATH"; then
  command -v mount.nfs >/dev/null || { echo "Install nfs-common first" >&2; exit 1; }
  mount -t nfs4 -o rw,nosuid,nodev "$SHARED_SOURCE" "$SHARED_PATH"
  printf '%s %s nfs4 rw,nosuid,nodev,_netdev,x-systemd.after=wg-quick@wg0.service 0 0\n' "$SHARED_SOURCE" "$SHARED_PATH" >> /etc/fstab
fi

EGRESS_IP=$(curl -fsS --max-time 20 --proxy "$LLM_PROXY" https://api.ipify.org)
curl -fsS --max-time 20 --proxy "$LLM_PROXY" "${BASE_URL%/}/models" \
  -H @/etc/scopeweaver-executor/llm-auth-header >/dev/null

umask 077
cat >/etc/scopeweaver-executor/executor.env <<EOF
ARTEX_PG_DSN_FILE=/run/secrets/pg-dsn
ARTEX_LLM_PROVIDER=openai
ARTEX_LLM_MODEL=$MODEL
ARTEX_LLM_BASE_URL=$BASE_URL
ARTEX_LLM_PROXY=$LLM_PROXY
OPENAI_API_KEY_FILE=/run/secrets/llm-api-key
SCOPEWEAVER_NODE_ID=$NODE_ID
SCOPEWEAVER_NODE_NAME=$NODE_ID
SCOPEWEAVER_NODE_OWNER=$OWNER
SCOPEWEAVER_WIREGUARD_IP=$WG_IP
SCOPEWEAVER_CAPABILITIES=$CAPABILITIES
SCOPEWEAVER_SLOTS=$SLOTS
SCOPEWEAVER_SHARED_PATH=$SHARED_PATH
SCOPEWEAVER_EGRESS_IP=$EGRESS_IP
EOF

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cat >/etc/systemd/system/scopeweaver-executor.service <<EOF
[Unit]
Description=ScopeWeaver cooperative executor ($NODE_ID)
After=docker.service network-online.target
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=$ROOT
EnvironmentFile=/etc/scopeweaver-executor/executor.env
ExecStart=/usr/bin/docker compose -f tools/cluster/docker-compose.executor.yml up -d --build
ExecStop=/usr/bin/docker compose -f tools/cluster/docker-compose.executor.yml down
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now scopeweaver-executor.service
echo "Node $NODE_ID joined. LLM egress IP: $EGRESS_IP"
echo "Verify with: $ROOT/tools/cluster/verify-node.sh"
