# Security Policy

## Reporting a vulnerability

Please report security issues **privately** via
[GitHub security advisories](https://github.com/olemeyer/rocketplaneIO/security/advisories/new) —
not as public issues. You'll get an acknowledgement within a few days.

## Scope & status

rocketplaneIO is **alpha**. It is not hardened for production use yet, and the following is
explicitly on the roadmap rather than done: multi-user RBAC hardening and a server-side
encrypted vault for LLM provider keys (currently browser-local).

Reports we care about most:

- Anything that lets the agent read or mutate beyond its enumerated RBAC.
- Anything that lets the copilot/LLM bypass the action whitelist, parameter validation,
  risk-level approval gates or the namespace scope.
- Tenancy violations across orgs/clusters in the control plane.
- Secret material leaking into telemetry, inventory, logs or LLM prompts
  (the inventory is designed to carry secret *metadata* only — names, types, key counts).

## Design notes for reviewers

- The in-cluster agent is outbound-only (it dials the control plane; nothing connects in) and
  runs with enumerated RBAC — see [`deploy/install.yaml`](deploy/install.yaml).
- The LLM never executes anything directly: it proposes catalog actions, which are validated,
  risk-graded and human-approved server-side before the agent claims them.
