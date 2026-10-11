#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
SHARED_PATH=${SCOPEWEAVER_SHARED_PATH:-/srv/scopeweaver-shared}

sudo install -d -m 0750 -o "$(id -un)" -g "$(id -gn)" "$SHARED_PATH"
mkdir -p "$SHARED_PATH"/{tasks,transcripts,evidence,artifacts}

echo "Shared ScopeWeaver data root: $SHARED_PATH"
echo "Restart the normal ScopeWeaver service once so PostgreSQL applies the additive cluster schema."
echo "Then open System → Cluster. No separate control-plane service is installed."
echo
echo "For NFS, export this path only to the private WireGuard subnet, with root_squash enabled."
echo "Example /etc/exports entry:"
echo "$SHARED_PATH 10.77.0.0/24(rw,sync,no_subtree_check,all_squash,anonuid=$(id -u),anongid=$(id -g))"
echo
echo "Repository: $ROOT"
