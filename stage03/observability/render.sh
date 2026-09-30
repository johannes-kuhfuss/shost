#!/usr/bin/env bash
# Render and lint only. Never contacts the Kubernetes API or installs releases.
set -euo pipefail
cd "$(dirname "$0")"
source versions.env
output="${1:?Usage: bash render.sh /absolute/output/directory}"
mkdir -p "$output"
output="$(cd "$output" && pwd)"
helm repo add grafana https://grafana.github.io/helm-charts --force-update
helm repo add grafana-community https://grafana-community.github.io/helm-charts --force-update
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
helm repo update

render() {
  local name="$1" repo="$2" version="$3"
  shift 3
  mkdir -p "$output/charts"
  helm pull "$repo/$name" --version "$version" --destination "$output/charts"
  local chart="$output/charts/$name-$version.tgz"
  helm lint "$chart" --strict -f "$name/values.yaml" "$@"
  helm template "$name" "$chart" --namespace observability \
    --kube-version 1.35.0 -f "$name/values.yaml" "$@" > "$output/$name.yaml"
}
render prometheus prometheus-community "$PROMETHEUS_CHART_VERSION"
render loki grafana "$LOKI_CHART_VERSION"
render tempo grafana-community "$TEMPO_CHART_VERSION"
render alloy grafana "$ALLOY_CHART_VERSION" \
  --set-file alloy.configMap.content=alloy/config.alloy
render grafana grafana-community "$GRAFANA_CHART_VERSION" \
  --set-file dashboards.platform.overview.json=grafana/overview.json
python3 validate.py "$output"
