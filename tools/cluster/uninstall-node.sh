#!/usr/bin/env bash
set -euo pipefail
systemctl disable --now scopeweaver-executor.service 2>/dev/null || true
rm -f /etc/systemd/system/scopeweaver-executor.service
systemctl daemon-reload
echo "Executor removed. Local credentials remain in /etc/scopeweaver-executor for deliberate manual removal."
