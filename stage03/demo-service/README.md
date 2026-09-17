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
| `/certificate` | Metadata for the current TLS certificate |

`/certificate` returns 503 when TLS is disabled or no certificate has been
loaded. It never returns private-key material.

## Build

```bash
docker build -t johanneskuhfuss/demo-service .
```

Adjust the tag to your account / linking.

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

| Variable | Default | Description |
| --- | --- | --- |
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

An optional `.env` file can provide the same values. Existing process
environment variables take precedence.

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
