"""Check chart integration contracts and extract configs for native validators.

Usage: python3 validate.py /path/to/rendered (requires PyYAML).
No Kubernetes API access. Fails on mismatched Services, TLS, storage or links.
"""
import json
from pathlib import Path
import sys

import yaml


ROOT = Path(__file__).resolve().parent
rendered = Path(sys.argv[1])
objects = []
for name in ("grafana", "prometheus", "loki", "tempo", "alloy"):
    objects.extend(doc for doc in yaml.safe_load_all((rendered / f"{name}.yaml").read_text(encoding="utf-8")) if doc)
for path in (ROOT / "manifests").glob("*.yaml"):
    objects.extend(yaml.safe_load_all(path.read_text(encoding="utf-8")))
for name in ("gateway.yaml", "namespace.yaml"):
    objects.extend(yaml.safe_load_all((ROOT.parent / "cert-manager" / "manifests" / name).read_text(encoding="utf-8")))


def obj(kind, name):
    matches = [o for o in objects if o["kind"] == kind and o["metadata"]["name"] == name]
    assert len(matches) == 1, (kind, name, len(matches))
    return matches[0]


def config(name, key):
    text = obj("ConfigMap", name)["data"][key]
    (rendered / "configs" / key).write_text(text, encoding="utf-8")
    return yaml.safe_load(text)


(rendered / "configs").mkdir(exist_ok=True)
for name, ports in {"grafana": [443], "prometheus-server": [80], "loki": [3100],
                    "tempo": [3200, 4318], "alloy": [12345, 4317, 4318],
                    "kube-state-metrics": [8080]}.items():
    svc = obj("Service", name)["spec"]
    assert svc.get("type", "ClusterIP") == "ClusterIP", name
    assert set(ports) <= {p["port"] for p in svc["ports"]}, name
    assert any(all(o["spec"]["template"]["metadata"]["labels"].get(k) == v
                       for k, v in svc["selector"].items())
               for o in objects if o["kind"] in ("Deployment", "StatefulSet")), name

for name in ("grafana", "prometheus-server"):
    pvc = obj("PersistentVolumeClaim", name)
    assert pvc["spec"]["storageClassName"] == "local-path"
    assert pvc["metadata"]["annotations"]["helm.sh/resource-policy"] == "keep"
for name in ("loki", "tempo"):
    spec = obj("StatefulSet", name)["spec"]
    assert spec["replicas"] == 1
    assert spec["volumeClaimTemplates"], name
    for claim in spec["volumeClaimTemplates"]:
        assert claim["spec"]["storageClassName"] == "local-path"
    assert all(v == "Retain" for v in spec.get("persistentVolumeClaimRetentionPolicy", {}).values())

grafana = obj("Deployment", "grafana")["spec"]
assert grafana["strategy"]["type"] == "Recreate"
container = next(c for c in grafana["template"]["spec"]["containers"] if c["name"] == "grafana")
for probe in ("readinessProbe", "livenessProbe"):
    assert container[probe]["httpGet"]["scheme"] == "HTTPS"
password = next(e for e in container["env"] if e["name"] == "GF_SECURITY_ADMIN_PASSWORD")
assert password["valueFrom"]["secretKeyRef"]["name"] == "grafana-admin"
assert obj("Service", "grafana")["spec"]["ports"][0]["name"] == "https"
assert obj("BackendTLSPolicy", "grafana")["spec"]["validation"]["hostname"] in obj("Certificate", "grafana-backend")["spec"]["dnsNames"]
assert obj("HTTPRoute", "grafana")["spec"]["rules"][0]["backendRefs"][0] == {"name": "grafana", "port": 443}
listener = next(l for l in obj("Gateway", "internal-web")["spec"]["listeners"] if l["name"] == "grafana")
assert listener["hostname"] == obj("HTTPRoute", "grafana")["spec"]["hostnames"][0]
assert listener["tls"]["certificateRefs"][0]["name"] == "grafana-tls"
assert obj("Namespace", "observability")["metadata"]["labels"]["trust.tc.jku.internal/internal-ca"] == "true"

ds = yaml.safe_load(obj("ConfigMap", "grafana")["data"]["datasources.yaml"])["datasources"]
assert {d["uid"] for d in ds} == {"prometheus", "loki", "tempo"}
tempo_ds = next(d for d in ds if d["uid"] == "tempo")["jsonData"]
assert tempo_ds["tracesToLogsV2"]["datasourceUid"] == "loki"
assert tempo_ds["tracesToMetrics"]["datasourceUid"] == "prometheus"
dashboard = json.loads(obj("ConfigMap", "grafana-dashboards-platform")["data"]["overview.json"])
assert dashboard["uid"] == "platform-overview"
assert {p["datasource"]["uid"] for p in dashboard["panels"]} <= {d["uid"] for d in ds}

prom = config("prometheus-server", "prometheus.yml")
assert prom["storage"]["exemplars"]["max_exemplars"] > 0
jobs = {job["job_name"]: job for job in prom["scrape_configs"]}
assert {"kubernetes-api-servers", "kubernetes-nodes", "kubernetes-nodes-cadvisor", "kube-state-metrics", "observability"} <= jobs.keys()
for name in ("kubernetes-nodes", "kubernetes-nodes-cadvisor"):
    assert not jobs[name]["tls_config"]["insecure_skip_verify"]
    assert any(r.get("replacement", "").startswith("/api/v1/nodes/") for r in jobs[name]["relabel_configs"])
for key, value in obj("ConfigMap", "prometheus-server")["data"].items():
    if key != "allow-snippet-annotations":
        (rendered / "configs" / key).write_text(value, encoding="utf-8")
loki = config("loki", "config.yaml")
assert loki["limits_config"]["retention_period"] == "168h"
assert loki["compactor"]["retention_enabled"]
assert loki["schema_config"]["configs"][0]["schema"] == "v13"
tempo = config("tempo", "tempo.yaml")
assert tempo["backend_scheduler"]["provider"]["compaction"]["compaction"]["block_retention"] == "168h"
assert tempo["metrics_generator"]["storage"]["remote_write"][0]["send_exemplars"]
config("tempo", "overrides.yaml")
alloy = obj("Deployment", "alloy")["spec"]
assert alloy["replicas"] == 1 and alloy["strategy"]["type"] == "Recreate"
assert not any("hostPath" in v for v in alloy["template"]["spec"]["volumes"])
for role in (o for o in objects if o["kind"] == "ClusterRole" and o["metadata"]["name"] == "alloy"):
    assert not any("secrets" in r.get("resources", []) for r in role["rules"])
images = {}
for o in objects:
    if o["kind"] in ("Deployment", "StatefulSet"):
        for c in o["spec"]["template"]["spec"]["containers"]:
            if c["name"] in ("alloy", "prometheus-server", "loki", "tempo"):
                images[c["name"]] = c["image"]
assert set(images) == {"alloy", "prometheus-server", "loki", "tempo"}
(rendered / "images.json").write_text(json.dumps(images), encoding="utf-8")
print(f"Validated {len(objects)} resources, backend configs, TLS, storage, and Grafana provisioning.")
