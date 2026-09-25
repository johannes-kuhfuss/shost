# Demo Service

The demo service exercises the platform's HTTP hosting, health probes, graceful
shutdown, backend TLS, and automatic certificate rotation.

## Capabilities

- Hosting
- Certificates
- Graceful request draining
- Kubernetes startup, readiness, and liveness probes

## Endpoints

| Path | Purpose |
| --- | --- |
| `/` | Basic service response |
| `/health/startup` | Startup probe |
| `/health/ready` | Readiness probe; returns 503 while draining |
| `/health/live` | Liveness probe |
| `/certificate` | Web page displaying the current TLS certificate's subject, issuer, serial number, DNS names, validity dates, and SHA-256 fingerprint |

`/certificate` returns 503 when TLS is disabled or no certificate has been
loaded. It never returns private-key material.

## Build

The builder uses `dhi.io/golang:1.27.1-debian13-dev`. Authenticate with your
Docker Hub username (or organization name) and read-only access token before
building. The development variant supplies the shell and build tools needed
by the Dockerfile's `RUN` instructions.

```bash
docker login dhi.io
docker build -t johanneskuhfuss/demo-service .
```

Adjust the tag to your account / linking.

For GitHub Actions, configure repository secrets `DHI_USERNAME` and `DHI_TOKEN`
with the same read-only registry access. The workflow logs in before building
and logs out afterward. Fork pull requests and Dependabot pull requests run
the Go and manifest checks but skip the authenticated image build and container
smoke test; those checks run on ordinary same-repository pull requests and pushes.
The Kubernetes pull Secret does not authenticate local or CI builds.

The final image is a non-root `scratch` image. It contains only the service
binary and listens on plain HTTP by default:

```bash
docker run --rm --read-only --user 65532:65532 -p 8080:8080 \
  johanneskuhfuss/demo-service
curl http://localhost:8080/health/ready
```

## TLS

TLS is enabled by setting `USE_TLS=true` and mounting a certificate and matching
private key. For example:

```bash
docker run --rm --read-only --user 65532:65532 -p 8443:8443 \
  -e USE_TLS=true \
  -e CERT_FILE=/var/run/demo-service/tls/tls.crt \
  -e KEY_FILE=/var/run/demo-service/tls/tls.key \
  -v /path/to/tls:/var/run/demo-service/tls:ro \
  johanneskuhfuss/demo-service
```

The certificate files are watched and reloaded without restarting the server.
TLS 1.3 is required; Go negotiates HTTP/2 automatically when the client supports
it. The Kubernetes manifests request and mount the certificate through
cert-manager.

## Configuration

The `/probes` page can temporarily disable readiness for a duration entered in
seconds (1–3600, default 60). The response includes the recovery time and a
countdown that works while the pod is unreachable through the Service. The
server automatically ends the pause at its deadline without another UI request.
Kubernetes needs to observe readiness success and update routing before Service
access returns. Shutdown still keeps the pod unready. A new pause replaces the
previous deadline; restarting the application clears the pause.

| Variable | Default | Description |
| --- | --- | --- |
| `POD_NAME` | empty | Pod name from the Downward API (`metadata.name`) |
| `POD_IP` | empty | Pod IP from the Downward API (`status.podIP`) |
| `POD_NAMESPACE` | empty | Pod namespace from the Downward API (`metadata.namespace`) |
| `NODE_NAME` | empty | Node name from the Downward API (`spec.nodeName`) |
| `SERVER_HOST` | empty (all interfaces) | Listen address |
| `SERVER_PORT` | `8080` | Plain HTTP port |
| `SERVER_TLS_PORT` | `8443` | HTTPS port |
| `USE_TLS` | `false` | Enable HTTPS |
| `CERT_FILE` | `/var/run/demo-service/tls/tls.crt` | PEM certificate path |
| `KEY_FILE` | `/var/run/demo-service/tls/tls.key` | PEM private-key path |
| `DRAIN_REQUESTS_TIME` | `12` | Seconds to become unready before shutdown |
| `GRACEFUL_SHUTDOWN_TIME` | `10` | Seconds allowed for active requests |
| `GIN_MODE` | `release` | Gin mode |
| `LOG_TO_LOGGER` | `false` | Enable Gin access logs |
| `LOG_FORMAT` | `text` | Console format: `text` or `json` |
| `LOG_LEVEL` | `info` | Minimum severity: `debug`, `info`, `warn`, or `error` |

An optional `.env` file can provide the same values. Existing process
environment variables take precedence.

The deployment injects the four Kubernetes fields through Downward API environment
variables. The status page displays them, or `N/A` when they are unset (for example,
when running locally). Values are read at application startup.

## Logging

The service uses Go's `log/slog` with structured attributes and writes to stderr.
The default text output is readable in the terminal and through `kubectl logs`:

```text
time=2026-09-17T14:32:10Z level=INFO msg=Listening service.name=demo-service server.address=:8080
```

Set `LOG_FORMAT=json` for one JSON object per line. Both formats honor
`LOG_LEVEL`. Invalid logging settings fail startup. Configuration errors before
the logger is initialized use a bootstrap text logger.

`LOG_TO_LOGGER=true` enables structured access records with the HTTP method,
route template, response status, and duration. Responses in the 4xx range log
at warn; 5xx responses log at error. Query strings, request headers, and raw URL
paths are omitted. Panic recovery logs at error even when access logs are off.
HTTP server errors and certificate watcher events use the same logger.

Logging calls carry the available context so a future OpenTelemetry `otelslog`
handler can correlate records with active spans. This change does not install
tracing or export telemetry. JSON console output is ordinary slog JSON, not
OTLP JSON. When adding export, combine the console handler and OTel bridge with
`slog.NewMultiHandler`, configure service resource metadata and batched OTLP
export, and flush the provider during shutdown. Keep console text enabled for
operators; avoid collecting the same records through both console scraping and
OTLP.

## Verification

Run the local checks with:

```bash
go test -race ./...
go vet ./...
```

The repository workflow also validates the Kubernetes manifests, builds the
container, and smoke-tests it as UID/GID 65532 with a read-only root filesystem.
All test certificates are generated at test time; no reusable private key is
stored in the repository.
