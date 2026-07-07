## What & why

<!-- One or two sentences: what does this change, and what problem does it solve? -->

## How it was verified

<!-- Build/typecheck is assumed. What did you run against a real cluster, and what did you see? -->

## Checklist

- [ ] Conventional commit(s), one concern per commit
- [ ] `go build ./...` (agent and/or control plane) and `pnpm typecheck` pass
- [ ] New safe actions declare a risk level and define verify + rollback behavior
- [ ] UI changes follow the RETICLE guidelines (healthy is calm, only anomalies speak)
