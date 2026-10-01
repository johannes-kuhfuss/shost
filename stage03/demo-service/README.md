# Demo Service

The demo service exercises the platform's HTTP hosting, health probes, graceful
shutdown, backend TLS, and automatic certificate rotation.

## Capabilities

- Hosting
- Certificates
- Graceful request draining
- Kubernetes startup, readiness, and liveness probes
- OpenTelemetry Gin tracing and HTTP metrics, correlated JSON logs, and probe counters

## Endpoints

| Path | Purpose |
| --- | --- |
| `/` | Basic service response |
| `/health/startup` | Startup probe |
| `/health/ready` | Readiness probe; returns 503 while draining |
| `/health/live` | Liveness probe |
| `/logs` | Form to write a message with Debug, Info, Warn, or Error severity (GET displays the form; POST sends the message) |
| `/metrics` | Live per-pod Prometheus text metrics; linked from the navigation and also supports OpenMetrics negotiation |
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
| `POD_UID` | empty | Pod UID for telemetry resource identity and Alloy enrichment |
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
| `LOG_FORMAT` | `text` | Console format: `text` or `json` |
| `LOG_LEVEL` | `info` | Minimum severity: `debug`, `info`, `warn`, or `error` |
| `OTEL_ENABLED` | `false` | Enable asynchronous OTLP trace and metric export; deployed value is `true` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | SDK default `http://localhost:4318` | OTLP/HTTP base URL; deployed value is `http://alloy.observability.svc.cluster.local:4318` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | HTTP/protobuf implementation | Deployment explicitly sets `http/protobuf`; this service does not implement a gRPC exporter |
| `OTEL_METRIC_EXPORT_INTERVAL` | `15000` | Metric export interval in milliseconds (1–3600000) |
| `OTEL_SHUTDOWN_TIMEOUT` | `5s` | Shared final trace/metric flush window, greater than zero and at most `1m` |

An optional `.env` file can provide the same values. Existing process
environment variables take precedence.

The deployment injects Kubernetes metadata through Downward API environment
variables. The status page displays the original four fields; pod UID is used
for telemetry. Values are read at application startup.

## Logging

The service uses Go's `log/slog` with structured attributes and writes to stderr.
The Logs page sends messages through the same logger with request context and
`log.source=web`. Messages must contain 1–4,096 characters. The page reports
when `LOG_LEVEL` filters out the chosen severity; use `LOG_LEVEL=debug` to
exercise all four levels. Successful submissions redirect to prevent refresh
from sending the message again.
The default text output is readable in the terminal and through `kubectl logs`:

```text
time=2026-09-17T14:32:10Z level=INFO msg=Listening service.name=demo-service server.address=:8080
```

Set `LOG_FORMAT=json` for one JSON object per line. Both formats honor
`LOG_LEVEL`. Invalid logging settings fail startup. Configuration errors before
the logger is initialized use a bootstrap text logger.

Structured access records include the HTTP method,
route template, response status, and duration. Responses in the 4xx range log
at warn; 5xx responses log at error. Query strings, request headers, and raw URL
paths are omitted from access logs. Panic recovery logs at error.
HTTP server errors and certificate watcher events use the same logger.

Context-aware logs include root-level `trace_id`, `span_id`, and `trace_sampled`
when a valid span context exists. Background/startup logs do not invent trace IDs.
The deployment enables JSON stderr output, collected by Alloy into Loki, while
`kubectl logs` remains available. No OTLP log exporter is installed, so this does
not duplicate the existing container-log collection. `service.name` is fixed to
`demo-service`, matching the pod label that Alloy maps to Loki's `service_name`.

## OpenTelemetry

The **Metrics** navigation link opens `/metrics` as browser-readable Prometheus
text. It exposes the existing HTTP and probe instruments through an additional
OpenTelemetry reader, independently of the periodic OTLP exporter. It works
even with `OTEL_ENABLED=false`. OpenMetrics clients can request
`Accept: application/openmetrics-text; version=1.0.0` to include supported exemplars.

This is a snapshot of the individual pod serving the request, not a historical
or cluster-wide view. HTTP metrics appear after requests have been handled;
all six probe counter series are present from startup. `/metrics` is excluded
from Gin tracing, HTTP metrics, and routine access logging, and responses are
not cached. It follows the service's existing HTTPS and access configuration.
Keep using Grafana for history and aggregation. The stack continues receiving
OTLP metrics; no Prometheus scrape job is added for this endpoint, avoiding
duplicate ingestion of the same instruments.

`otelgin` runs before access logging and recovery. It extracts W3C `traceparent`
and `tracestate`; a valid incoming parent produces a new server span in the same
trace. Missing/invalid context starts a new trace. The response includes
`X-Trace-ID` for troubleshooting; an incoming `X-Trace-ID` does not define trace
parentage. Handler code uses `c.Request.Context()` for logging and child spans.
Probe endpoints are instrumented too. Recovered panics produce a 500, correlated
error logs, and an exception event on the server span.

The sampler records all new root traces and respects an upstream parent's
sampling decision. Unsampled requests still have IDs and metrics, but no stored
trace to navigate to. With `OTEL_ENABLED=false`, IDs and correlated logs still
work locally; providers perform no network export.

Traces use a bounded asynchronous batch exporter. Metrics use periodic OTLP/HTTP
export with cumulative temporality and the SDK's trace-based histogram exemplars.
`otelgin` records `http.server.request.duration` plus request/response body-size
histograms. Request counts are available from the duration histogram's count.
Route attributes use templates, not individual path parameters. No trace IDs
are added as metric labels. Traces use the standard HTTP instrumentation
attributes; review those separately if handling sensitive URLs.

Probe totals come from the same locked `RuntimeState` snapshots as the probes
page, exposed as the observable counter `demo_service.probe_checks`. Prometheus
normalizes it to `demo_service_probe_checks_total`, with `probe` set to `startup`,
`readiness`, or `liveness` and `outcome` set to `success` or `failure`. All six
series are emitted, including zeros. These count requests the process handled,
not kubelet connection failures or timeouts before a handler runs. Startup
currently always succeeds. Counts reset on process restart; use `rate()` or
`increase()` for time windows.

Resources include `service.name`, `service.instance.id`, and available Kubernetes
metadata. The instance ID is the pod UID, or a generated UUID outside Kubernetes.
Prometheus maps service instance identity to `instance`, keeping replicas' counters
separate. `OTEL_RESOURCE_ATTRIBUTES` can add metadata; the service's explicit
identity and available Kubernetes fields take precedence.

Collector outages do not prevent serving requests. Export errors are logged;
queues are bounded and can lose telemetry during prolonged outages or crashes.
After HTTP draining and shutdown, both providers flush using a fresh context
within `OTEL_SHUTDOWN_TIMEOUT`, even when SIGTERM canceled the application context.
The deployment budget is 35 seconds: 12 drain + 10 HTTP shutdown + 5 telemetry
flush + 8 margin. Update the manifest and CI budget check together when changing
these values.

For future downstream calls, reuse an instrumented client from
`a.telemetry.HTTPClient(timeout)` and create each request with
`http.NewRequestWithContext(c.Request.Context(), ...)`. The client creates a new
client span and injects W3C context, preserving the trace ID with a new span ID.
No downstream call is made by this version of the demo.

### Deploy and verify the integration

Install the [observability stack](../observability/README.md) first. Build/push
a new demo image, set that image in `manifests/app.yaml`, then apply the existing
raw resources; no Helm migration is involved:

```bash
kubectl apply -f manifests/
kubectl -n demo-service rollout status deployment/demo-service --timeout=5m
```

The namespace manifest opts into Alloy's OTLP ingress policy. Pod Security
Restricted settings, HTTPS hosting, and certificate rotation remain in place.
If reusing the existing image tag, an unchanged pod template will need an explicit
rollout; a unique image tag is preferable.

Send a log with known trace context (the POST redirects, so omit `curl -L` to
inspect that request's own response header):

```bash
curl --cacert /path/to/web-root-ca.pem -i \
  -H 'traceparent: 00-0123456789abcdef0123456789abcdef-0123456789abcdef-01' \
  --data-urlencode 'message=Correlated demo log' --data 'severity=info' \
  https://demo.tc.jku.internal/logs
```

Expect `X-Trace-ID: 0123456789abcdef0123456789abcdef`. Repeat without the header
to get a new ID. After a few seconds, query Loki in Grafana Explore:

```logql
{service_name="demo-service"} | json | trace_id="0123456789abcdef0123456789abcdef"
```

Open the log's trace link and then the span's related logs. In Prometheus,
after at least one export interval, query:

```promql
demo_service_probe_checks_total{service_name="demo-service"}
sum by (probe, outcome) (increase(demo_service_probe_checks_total{service_name="demo-service"}[5m]))
sum by (http_route, http_response_status_code) (rate(http_server_request_duration_seconds_count{service_name="demo-service"}[5m]))
```

Use the existing probes page to temporarily fail readiness, then observe
`probe="readiness", outcome="failure"` increasing. In Grafana's latency
histogram queries, enable exemplars to navigate to sampled request traces.
Application end-to-end verification requires the deployed cluster; local tests
use in-memory providers and an HTTP OTLP receiver.

## Verification

Run the local checks with:

```bash
go test -race ./...
go vet ./...
```

The race detector requires CGO and a C compiler. Linux runs also exercise
projected Secret rotation by atomically replacing the `..data` symlink and
checking the certificate served on a new TLS connection. Watcher tests wait for
reload events rather than assuming a fixed filesystem delay.

Run the Chromium readiness countdown test with Node.js and Go installed:

```bash
cd e2e
npm ci
npx playwright install --with-deps chromium
npm test
```

The browser test starts its own HTTP service on `127.0.0.1:18081`, disables
readiness, takes the browser offline, checks that the countdown completes, and
reconnects to verify recovery. It simulates loss of Service access; it does not
require a Kubernetes cluster. Set `DEMO_SERVICE_BINARY` to an absolute executable
path to use a prebuilt service instead of `go run .`. CI runs this browser test
alongside the Linux race tests.

The repository workflow also validates the Kubernetes manifests, builds the
container, and smoke-tests it as UID/GID 65532 with a read-only root filesystem.
All test certificates are generated at test time; no reusable private key is
stored in the repository.
