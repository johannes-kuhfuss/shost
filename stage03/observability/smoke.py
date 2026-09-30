"""Send synthetic OTLP signals and verify storage plus an exemplar.

Run the four localhost port forwards documented in README.md first.
Uses no application code or external Python packages. Telemetry expires normally.
"""
import json
import secrets
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


def request(base, path, data=None, params=None):
    if params:
        path += "?" + urlencode(params)
    encoded = None if data is None else json.dumps(data).encode()
    req = Request(base + path, data=encoded, headers={
        "Content-Type": "application/json", "Accept": "application/json",
    })
    with urlopen(req, timeout=10) as response:
        body = response.read()
    return json.loads(body) if body else {}


def eventually(description, check, timeout=180):
    deadline = time.monotonic() + timeout
    last = "no matching data"
    while time.monotonic() < deadline:
        try:
            if check():
                print(f"PASS: {description}", flush=True)
                return
        except (HTTPError, URLError, TimeoutError) as error:
            last = str(error)
        time.sleep(3)
    raise SystemExit(f"FAIL: {description}: {last}")


def main():
    otlp = "http://127.0.0.1:4318"
    prometheus = "http://127.0.0.1:9090"
    loki = "http://127.0.0.1:3100"
    tempo = "http://127.0.0.1:3200"
    trace_id, span_id = secrets.token_hex(16), secrets.token_hex(8)
    now = time.time_ns()
    start = now - 250_000_000
    resource = {"attributes": [{"key": "service.name", "value": {"stringValue": "observability-smoke"}}]}
    scope = {"name": "shost.stack.smoke"}
    traces = {"resourceSpans": [{"resource": resource, "scopeSpans": [{
        "scope": scope, "spans": [{
            "traceId": trace_id, "spanId": span_id,
            "name": "stack-smoke", "kind": 2,
            "startTimeUnixNano": str(start), "endTimeUnixNano": str(now),
            "status": {"code": 1}, "flags": 1,
        }],
    }]}]}
    logs = {"resourceLogs": [{"resource": resource, "scopeLogs": [{
        "scope": scope, "logRecords": [{
            "timeUnixNano": str(now), "observedTimeUnixNano": str(now),
            "severityNumber": 9, "severityText": "INFO",
            "body": {"stringValue": "Observability stack smoke check"},
            "traceId": trace_id, "spanId": span_id, "flags": 1,
        }],
    }]}]}
    metrics = {"resourceMetrics": [{"resource": resource, "scopeMetrics": [{
        "scope": scope, "metrics": [{
            "name": "observability_smoke_value", "description": "Synthetic stack verification",
            "gauge": {"dataPoints": [{"timeUnixNano": str(now), "asInt": "1"}]},
        }],
    }]}]}
    for signal, payload in (("traces", traces), ("logs", logs), ("metrics", metrics)):
        response = request(otlp, f"/v1/{signal}", payload)
        partial = response.get("partialSuccess", {})
        if partial.get("errorMessage") or any(int(partial.get(k, 0)) for k in (
            "rejectedSpans", "rejectedLogRecords", "rejectedDataPoints",
        )):
            raise SystemExit(f"OTLP {signal} rejected data: {partial}")
    print(f"Sent OTLP log, trace, and metric. Trace ID: {trace_id}", flush=True)
    eventually("trace query", lambda: bool(request(tempo, f"/api/traces/{trace_id}")))
    # Same query as Grafana's trace-to-logs link; native OTLP trace_id is metadata.
    eventually("trace-correlated log query", lambda: bool(request(loki, "/loki/api/v1/query_range", params={
        "query": f'{{service_name="observability-smoke"}} | json | trace_id = "{trace_id}"',
        "start": str(start - 60_000_000_000), "end": str(time.time_ns()),
    })["data"]["result"]))
    eventually("OTLP metric query", lambda: bool(request(prometheus, "/api/v1/query", params={
        "query": 'observability_smoke_value{service_name="observability-smoke"}',
    })["data"]["result"]))

    def has_exemplar():
        data = request(prometheus, "/api/v1/query_exemplars", params={
            "query": 'traces_spanmetrics_latency_bucket{service="observability-smoke"}',
            "start": str(start / 1e9 - 60), "end": str(time.time()),
        })["data"]
        return any(exemplar["labels"].get("trace_id") == trace_id
                   for series in data for exemplar in series.get("exemplars", []))

    eventually("Tempo-generated metric exemplar links to the trace", has_exemplar)
    print("Verify the same log/trace links in Grafana Explore; sample service: observability-smoke.")


if __name__ == "__main__":
    main()
