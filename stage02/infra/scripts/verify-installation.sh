#!/usr/bin/env bash

set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage: verify-installation.sh [--extended] [--keep]

Run post-installation sanity checks against the Stage 02 Talos cluster.

Options:
  --extended  Also run Cilium connectivity and LoadBalancer/L2 traffic tests.
  --keep      Keep resources created by --extended for troubleshooting.
  -h, --help  Show this help text.

The default checks are read-only. Configuration is read directly from the
Talos OpenTofu state; existing ~/.talos and ~/.kube files are not changed.
EOF
}

extended=false
keep=false

while (($# > 0)); do
  case "$1" in
    --extended)
      extended=true
      ;;
    --keep)
      keep=true
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown option: %s\n\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

if [[ "${keep}" == true && "${extended}" != true ]]; then
  printf '%s\n' '--keep can only be used together with --extended.' >&2
  exit 2
fi

required_commands=(tofu talosctl kubectl cilium jq timeout)
if [[ "${extended}" == true ]]; then
  required_commands+=(curl)
fi

missing_commands=()
for command_name in "${required_commands[@]}"; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    missing_commands+=("${command_name}")
  fi
done

if ((${#missing_commands[@]} > 0)); then
  printf 'Required command(s) not found in PATH: %s\n' "${missing_commands[*]}" >&2
  exit 1
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
talos_root="${script_dir}/../opentofu/talos"

temporary_dir=''
connectivity_namespace=''
load_balancer_namespace=''
configuration_ready=false

cleanup() {
  local exit_status=$?

  trap - EXIT
  set +e

  if [[ "${extended}" == true && "${keep}" != true ]]; then
    if [[ -n "${connectivity_namespace}" ]]; then
      kubectl delete namespace "${connectivity_namespace}" \
        --ignore-not-found --wait=false >/dev/null 2>&1 || true
    fi

    if [[ -n "${load_balancer_namespace}" ]]; then
      kubectl delete namespace "${load_balancer_namespace}" \
        --ignore-not-found --wait=false >/dev/null 2>&1 || true
    fi
  elif [[ "${extended}" == true && "${keep}" == true ]]; then
    printf '\nExtended-test resources were kept for troubleshooting.\n'
    if [[ -n "${connectivity_namespace}" ]]; then
      printf '  Connectivity namespace: %s\n' "${connectivity_namespace}"
    fi
    if [[ -n "${load_balancer_namespace}" ]]; then
      printf '  LoadBalancer namespace: %s\n' "${load_balancer_namespace}"
    fi
  fi

  if [[ -n "${temporary_dir}" && -d "${temporary_dir}" ]]; then
    rm -f -- \
      "${temporary_dir}/talosconfig" \
      "${temporary_dir}/kubeconfig" \
      "${temporary_dir}/expected-nodes.json" \
      "${temporary_dir}/cluster-nodes.json" \
      "${temporary_dir}/ip-pools.json" \
      "${temporary_dir}/l2-policies.json" \
      "${temporary_dir}/metrics.txt" \
      "${temporary_dir}/load-balancer-address"
    rmdir -- "${temporary_dir}" 2>/dev/null || true
  fi

  exit "${exit_status}"
}

trap cleanup EXIT

diagnostics() {
  printf '\nDiagnostics:\n' >&2

  if [[ "${configuration_ready}" != true ]]; then
    printf 'Cluster configuration could not be loaded; diagnostics unavailable.\n' >&2
    return 0
  fi

  kubectl get nodes -o wide >&2 || true
  kubectl get pods --all-namespaces -o wide >&2 || true
  kubectl get events --all-namespaces \
    --sort-by=.lastTimestamp 2>/dev/null | tail -n 50 >&2 || true
  timeout 30s cilium status >&2 || true
}

checks_passed=0

run_check() {
  local description="$1"
  shift

  printf '[....] %s\n' "${description}"
  if "$@"; then
    ((checks_passed += 1))
    printf '[PASS] %s\n' "${description}"
    return 0
  fi

  printf '[FAIL] %s\n' "${description}" >&2
  diagnostics
  exit 1
}

extract_configuration() {
  tofu -chdir="${talos_root}" output -raw talosconfig \
    >"${temporary_dir}/talosconfig" || return 1
  tofu -chdir="${talos_root}" output -raw kubeconfig \
    >"${temporary_dir}/kubeconfig" || return 1
  tofu -chdir="${talos_root}" output -json control_plane_nodes \
    >"${temporary_dir}/expected-nodes.json" || return 1

  chmod 600 -- \
    "${temporary_dir}/talosconfig" \
    "${temporary_dir}/kubeconfig" \
    "${temporary_dir}/expected-nodes.json" || return 1

  jq -e \
    'type == "array" and length > 0 and all(.[]; type == "string")' \
    "${temporary_dir}/expected-nodes.json" >/dev/null
}

check_kubernetes_api() {
  [[ "$(kubectl get --raw=/readyz)" == 'ok' ]]
}

check_expected_nodes() {
  kubectl get nodes -o json >"${temporary_dir}/cluster-nodes.json" || return 1

  jq -e \
    --slurpfile expected "${temporary_dir}/expected-nodes.json" \
    '($expected[0] | unique) as $wanted
     | ([.items[].status.addresses[]?
         | select(.type == "InternalIP")
         | .address] | unique) as $actual
     | (.items | length) == ($wanted | length)
       and (($wanted - $actual) | length) == 0' \
    "${temporary_dir}/cluster-nodes.json" >/dev/null
}

ip_pools_are_healthy() {
  jq -e \
    '.items | length > 0
     and all(.[];
       ([.status.conditions[]?
         | select(.type == "cilium.io/PoolConflict")
         | .status]) as $conflicts
       | ($conflicts | length) > 0
         and all($conflicts[]; . == "False"))' \
    "${temporary_dir}/ip-pools.json" >/dev/null
}

check_ip_pools() {
  local deadline=$((SECONDS + 120))

  while ((SECONDS < deadline)); do
    if kubectl get ciliumloadbalancerippools -o json \
      >"${temporary_dir}/ip-pools.json" 2>/dev/null && \
      ip_pools_are_healthy; then
      return 0
    fi
    sleep 5
  done

  kubectl get ciliumloadbalancerippools -o json \
    >"${temporary_dir}/ip-pools.json" || return 1
  ip_pools_are_healthy
}

check_l2_policies() {
  kubectl get ciliuml2announcementpolicies -o json \
    >"${temporary_dir}/l2-policies.json" || return 1
  jq -e '.items | length > 0' \
    "${temporary_dir}/l2-policies.json" >/dev/null
}

check_metrics_api() {
  local deadline=$((SECONDS + 120))

  while ((SECONDS < deadline)); do
    if kubectl top nodes >"${temporary_dir}/metrics.txt" 2>/dev/null; then
      cat "${temporary_dir}/metrics.txt"
      return 0
    fi
    sleep 5
  done

  kubectl top nodes
}

run_connectivity_test() {
  local namespace_base="shost-sanity-connectivity-$$-${RANDOM}"
  connectivity_namespace="${namespace_base}-1"

  timeout 20m cilium connectivity test \
    --test-concurrency 1 \
    --test-namespace "${namespace_base}"
}

create_load_balancer_test() {
  kubectl create namespace "${load_balancer_namespace}" >/dev/null || return 1
  kubectl label namespace "${load_balancer_namespace}" \
    pod-security.kubernetes.io/enforce=restricted >/dev/null || return 1

  kubectl apply --namespace "${load_balancer_namespace}" -f - <<'EOF' || return 1
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 1
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: web
          image: nginxinc/nginx-unprivileged:1.29-alpine
          ports:
            - name: http
              containerPort: 8080
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector:
    app: web
  ports:
    - name: http
      port: 80
      targetPort: http
  type: LoadBalancer
EOF

  kubectl rollout status deployment/web \
    --namespace "${load_balancer_namespace}" --timeout=5m
}

check_load_balancer_address() {
  local deadline=$((SECONDS + 180))
  local service_address=''

  while ((SECONDS < deadline)); do
    service_address="$(
      kubectl get service web \
        --namespace "${load_balancer_namespace}" \
        -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true
    )"
    if [[ -n "${service_address}" ]]; then
      printf 'Assigned LoadBalancer address: %s\n' "${service_address}"
      printf '%s' "${service_address}" >"${temporary_dir}/load-balancer-address"
      return 0
    fi
    sleep 5
  done

  return 1
}

check_l2_lease() {
  local deadline=$((SECONDS + 120))
  local lease_name="cilium-l2announce-${load_balancer_namespace}-web"

  while ((SECONDS < deadline)); do
    if kubectl get lease "${lease_name}" \
      --namespace kube-system >/dev/null 2>&1; then
      return 0
    fi
    sleep 5
  done

  kubectl get lease "${lease_name}" --namespace kube-system
}

check_load_balancer_traffic() {
  local deadline=$((SECONDS + 120))
  local service_address

  service_address="$(<"${temporary_dir}/load-balancer-address")"
  while ((SECONDS < deadline)); do
    if curl --fail --silent --show-error --max-time 5 \
      "http://${service_address}" >/dev/null; then
      return 0
    fi
    sleep 5
  done

  curl --fail --show-error --max-time 10 "http://${service_address}" >/dev/null
}

umask 077
temporary_dir="$(mktemp -d)"

run_check 'OpenTofu state contains usable cluster configuration' \
  extract_configuration

export TALOSCONFIG="${temporary_dir}/talosconfig"
export KUBECONFIG="${temporary_dir}/kubeconfig"
configuration_ready=true

mapfile -t expected_nodes < <(
  jq -r '.[]' "${temporary_dir}/expected-nodes.json"
)
node_csv="$(IFS=,; printf '%s' "${expected_nodes[*]}")"

run_check 'Talos and Kubernetes report healthy' \
  talosctl --nodes "${expected_nodes[0]}" health \
    --control-plane-nodes "${node_csv}" --wait-timeout 10m
run_check 'Kubernetes API readiness endpoint returns ok' \
  check_kubernetes_api
run_check 'Kubernetes contains exactly the expected control-plane nodes' \
  check_expected_nodes
run_check 'All Kubernetes nodes are Ready' \
  kubectl wait --for=condition=Ready nodes --all --timeout=5m
run_check 'All kube-system Deployments completed rollout' \
  kubectl rollout status deployment --all \
    --namespace kube-system --timeout=5m
run_check 'All kube-system DaemonSets completed rollout' \
  kubectl rollout status daemonset --all \
    --namespace kube-system --timeout=5m
run_check 'Cilium reports healthy' \
  timeout 5m cilium status --wait
run_check 'Cilium GatewayClass is accepted' \
  kubectl wait --for=condition=Accepted gatewayclass/cilium --timeout=2m
run_check 'Cilium LoadBalancer IP pools exist without conflicts' \
  check_ip_pools
run_check 'At least one Cilium L2 announcement policy exists' \
  check_l2_policies
run_check 'Kubernetes metrics API returns node metrics' \
  check_metrics_api

if [[ "${extended}" == true ]]; then
  run_check 'Cilium connectivity test passes' \
    run_connectivity_test

  load_balancer_namespace="shost-sanity-lb-$$-${RANDOM}"
  run_check 'Temporary LoadBalancer workload becomes ready' \
    create_load_balancer_test
  run_check 'Cilium assigns a LoadBalancer address' \
    check_load_balancer_address
  run_check 'Cilium creates an L2 announcement lease' \
    check_l2_lease
  run_check 'LoadBalancer address serves HTTP from the deployment machine' \
    check_load_balancer_traffic
fi

printf '\nAll %d sanity checks passed.\n' "${checks_passed}"
