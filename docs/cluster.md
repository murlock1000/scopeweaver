# Cooperative ScopeWeaver cluster

One ordinary ScopeWeaver installation remains the UI, Planner, PostgreSQL owner,
and source of truth. Contributed servers run the same binary in `executor` mode:
they start Worker loops only and atomically claim open intents from the canonical
graph. Every run records both an execution node ID and its container ID.

## Network layout

- WireGuard carries PostgreSQL, shared storage, and cluster control traffic.
- Each executor's node-local HTTP/SOCKS proxy carries only its LLM API calls
  through that contributor's NordVPN connection.
- Target traffic is configured independently. Never point ScopeWeaver's global
  target proxy at the LLM egress proxy merely because both are proxies.

Keep the WireGuard subnet out of NordVPN policy routing. Prefer placing NordVPN
and its forward proxy in a separate network namespace or container with a kill
switch, so a tunnel outage cannot silently use the host's direct public address.

## Control plane

```bash
sudo ./tools/cluster/install-control-plane.sh
# Restart the existing ScopeWeaver service to apply the additive schema.
```

Export the shared data root only on the private WireGuard subnet. The existing
ScopeWeaver data directory and the exported directory must refer to the same
storage.

PostgreSQL must listen on the WireGuard address and authorize a dedicated
executor database role. Do not expose PostgreSQL on a public interface.

## Contributor node

Prepare WireGuard and a NordVPN-backed HTTP or SOCKS proxy. Put the Ollama key in
a root-readable file and run:

```bash
sudo ./tools/cluster/join-node.sh \
  --node-id alice-01 \
  --owner alice \
  --wireguard-ip 10.77.0.12 \
  --dsn-file /root/scopeweaver.dsn \
  --shared-source 10.77.0.1:/srv/scopeweaver-shared \
  --llm-key-file /root/ollama.key \
  --llm-proxy http://127.0.0.1:3128 \
  --model deepseek-v4.1-flash \
  --slots 4 \
  --capabilities worker,browser,scopesentry
```

The script verifies the WireGuard address, NFS mount, VPN public IP, and Ollama
API before writing `/etc/scopeweaver-executor/executor.env` with mode `0600`.
Provider and VPN secrets remain local and are never placed in shared storage.
All nodes should run the same committed ScopeWeaver revision. The node version is
reported to the Cluster page so an operator can spot a contributor that needs an
update before accepting more work.

## Agent-assisted installation

An administrator can hand this document and a short-lived database/WireGuard
configuration to a local coding agent. Enter secrets only through a hidden local
prompt or a root-readable file; never paste them into an agent conversation.

Ask the agent to run `join-node.sh`, then `verify-node.sh`, and return only the
sanitized check names and status. The common UI is available under **System →
Cluster**.

## Trust boundary

Executor mode currently shares PostgreSQL and the configured data root. Only add
coworkers and machines authorized to access every assigned program. Program
authorization still applies independently to every researcher, machine, source
IP, and testing technique. Drain a node in the UI before maintenance.
