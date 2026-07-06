# rocketplane-agent

The rocketplane in-cluster agent. A single, lightweight pod that connects your
Kubernetes cluster to the rocketplane control-plane.

## What it does

- **Outbound-only.** The agent opens connections *to* the control-plane over
  HTTPS. The control-plane never connects into your cluster — nothing needs to be
  exposed, no inbound ports, no ingress.
- **Read-only.** It reads the `kube-system` namespace UID (your cluster
  identity), then lists/watches namespaces and reports the full set to the
  control-plane. It cannot mutate anything in your cluster.
- **Self-enrolling.** On startup it exchanges the one-time enroll token
  (`rpe_...`) for a long-lived agent token, which it keeps in memory and uses as
  a `Bearer` token for heartbeats and namespace syncs.

## Connect a cluster

1. In the rocketplane UI, open your org and click **Connect cluster**, then enter
   a name.
2. Copy the generated install command. It looks like:

   ```sh
   helm install rocketplane-agent \
     oci://ghcr.io/olemeyer/rocketplaneio/charts/rocketplane-agent \
     --namespace rocketplane --create-namespace \
     --set controlplane.url=<CP_URL> \
     --set enrollToken=<TOKEN> \
     --set clusterName=<NAME>
   ```

3. Run it against the target cluster. Within a few seconds the cluster flips to
   **connected** in the UI and its namespaces appear.

The UI polls for the connection while you run the command, so you can watch it go
green live.

### kubectl-only alternative

If you don't use Helm, apply `deploy/install.yaml` after replacing the two
`REPLACE_ME_*` placeholders (control-plane URL and enroll token):

```sh
kubectl apply -f install.yaml
```

## Values

| Key | Default | Description |
|---|---|---|
| `controlplane.url` | `""` | Control-plane base URL. **Required.** → `RP_CONTROLPLANE_URL` |
| `enrollToken` | `""` | One-time enroll token (`rpe_...`). **Required.** Stored in a Secret → `RP_ENROLL_TOKEN` |
| `clusterName` | `""` | Display name in the UI → `RP_CLUSTER_NAME` |
| `image.repository` | `ghcr.io/olemeyer/rocketplaneio/agent` | Agent image |
| `image.tag` | `latest` | Image tag (defaults to chart `appVersion` if unset) |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `resources` | `50m` / `64Mi` req | Slim resource requests/limits |
| `rbac.create` | `true` | Create the read-only ClusterRole + binding |
| `serviceAccount.create` | `true` | Create the ServiceAccount |
| `serviceAccount.name` | `rocketplane-agent` | ServiceAccount name |
| `replicaCount` | `1` | Keep at 1 (single writer) |

## RBAC

The chart grants a **read-only** `ClusterRole`:

```yaml
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get", "list", "watch"]
```

No write or remediation verbs (`patch`, `update`, `delete`, `create`) are granted.
Remediation will be an explicit, opt-in capability in a future release — never on
by default, so a connected cluster stays observe-only until you choose otherwise.

## Uninstall

Helm:

```sh
helm uninstall rocketplane-agent --namespace rocketplane
kubectl delete namespace rocketplane   # optional
```

kubectl:

```sh
kubectl delete -f install.yaml
```

Uninstalling stops the sync; the cluster goes `stale` and can be reconnected from
the UI (which issues a fresh enroll token) at any time.
