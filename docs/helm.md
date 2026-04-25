# Helm Chart

Gypsum includes a Helm chart for deploying to Kubernetes.

## Install from OCI Registry

```bash
helm install gypsum oci://ghcr.io/mnorrsken/charts/gypsum
```

## Install from Source

```bash
helm install gypsum ./charts/gypsum
```

## Configuration

See all available values:

```bash
helm show values oci://ghcr.io/mnorrsken/charts/gypsum
```

### Image

| Parameter | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/mnorrsken/gypsum` | Container image repository |
| `image.tag` | `""` (uses `appVersion`) | Image tag override |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |

### Persistence

| Parameter | Default | Description |
|---|---|---|
| `persistence.enabled` | `true` | Enable persistent storage |
| `persistence.size` | `1Gi` | PVC size |
| `persistence.storageClass` | `""` | Storage class name. Leave empty to use the cluster default |
| `persistence.accessModes` | `[ReadWriteOnce]` | PVC access modes |
| `persistence.existingClaim` | `""` | Use an existing PVC |

### Git

| Parameter | Default | Description |
|---|---|---|
| `git.init` | `true` | Initialize a git repo in the data directory |
| `git.commitName` | `Gypsum` | Git commit author name |
| `git.commitEmail` | `gypsum@local` | Git commit author email |
| `git.remoteName` | `""` | Git remote name (default `origin` when `remoteUrl` is set) |
| `git.remoteUrl` | `""` | Git remote URL for backup/sync |
| `git.username` | `""` | Git username for HTTPS authentication |
| `git.password` | `""` | Git password for HTTPS authentication |
| `git.token` | `""` | Git token for HTTPS auth (takes precedence over username/password) |
| `git.pullInterval` | `5m` | Pull interval (Go duration). Only used when `remoteUrl` is set |
| `git.existingSecret` | `""` | Existing Secret for git credentials (must contain `password` and/or `token` keys) |

### OAuth (MCP)

| Parameter | Default | Description |
|---|---|---|
| `oauth.enabled` | `false` | Enable the OAuth-protected `/mcp` endpoint |
| `oauth.password` | `""` | OAuth login password. If empty, auto-generated |
| `oauth.externalUrl` | `""` | Public base URL (e.g. `https://wiki.example.com`). Required when `oauth.enabled` is true |
| `oauth.tokenTtl` | `24h` | Access token lifetime |
| `oauth.existingSecret` | `""` | Existing Secret with an `oauth-password` key |

### Authentication (Reverse Proxy)

| Parameter | Default | Description |
|---|---|---|
| `auth.userHeader` | `""` | Header name for reverse proxy auth (e.g. `Remote-User`). Enables header auth when set |
| `auth.groupHeader` | `Remote-Group` | Header containing comma-separated group names |
| `auth.requiredGroup` | `""` | If set, requests without this group are rejected with 403 |

### Metrics

| Parameter | Default | Description |
|---|---|---|
| `metrics.enabled` | `false` | Expose the Prometheus metrics port |
| `metrics.port` | `9090` | Container port for the metrics server (`/metrics`) |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus `ServiceMonitor` for automatic scrape discovery |
| `metrics.serviceMonitor.interval` | `""` | Scrape interval (e.g. `30s`). Omit to use the Prometheus default |
| `metrics.serviceMonitor.labels` | `{}` | Additional labels for the ServiceMonitor (e.g. `release: kube-prometheus-stack`) |

### Networking

| Parameter | Default | Description |
|---|---|---|
| `service.type` | `ClusterIP` | Kubernetes Service type |
| `service.port` | `80` | Service port |
| `ingress.enabled` | `false` | Enable Ingress |
| `ingress.className` | `""` | Ingress class name |
| `ingress.annotations` | `{}` | Ingress annotations |
| `ingress.tls` | `[]` | TLS configuration |
| `probePort` | `9091` | Health-probe server port (`/healthz`, `/readyz`) |

## Secret Handling

Encryption of `{{secure:...}}` fields happens entirely in the browser; the
cluster never holds the encryption passphrase. Each user enters it once via
the 🔒 dialog in the top bar.

The OAuth password (`GYPSUM_OAUTH_PASSWORD`) follows the usual pattern:

1. **Auto-generated (default)** — a pre-install hook Job generates a random password and stores it in a Secret that persists across upgrades.
2. **Explicit value** — set `oauth.password` in your values.
3. **Existing secret** — set `oauth.existingSecret` to reference a Secret you manage externally.

Retrieve an auto-generated OAuth password:

```bash
kubectl get secret <release>-gypsum-oauth -o jsonpath='{.data.oauth-password}' | base64 -d
```

## Example: Install with Ingress

```bash
helm install gypsum oci://ghcr.io/mnorrsken/charts/gypsum \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=wiki.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix
```

## Example: Enable OAuth + Metrics

```bash
helm install gypsum oci://ghcr.io/mnorrsken/charts/gypsum \
  --set oauth.enabled=true \
  --set oauth.externalUrl=https://wiki.example.com \
  --set oauth.password=your-password \
  --set metrics.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.serviceMonitor.labels.release=kube-prometheus-stack
```

## Example: Reverse Proxy Authentication (Authelia)

```bash
helm install gypsum oci://ghcr.io/mnorrsken/charts/gypsum \
  --set auth.userHeader=Remote-User \
  --set auth.requiredGroup=wiki-users
```

See [Authentication](authentication.md) for the corresponding Authelia `access_control` rules.
