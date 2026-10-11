#!/usr/bin/env bash
set -euo pipefail
echo "Drain is controlled centrally in ScopeWeaver: System → Cluster → Drain."
echo "After active containers reach zero, stop locally with:"
echo "  sudo systemctl stop scopeweaver-executor.service"
