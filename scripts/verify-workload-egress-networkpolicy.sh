#!/usr/bin/env bash
set -euo pipefail

chart_dir="charts/k8s-runner"
rendered="$(mktemp)"
error_log="$(mktemp)"
trap 'rm -f "$rendered" "$error_log"' EXIT

helm dependency build "$chart_dir" >/dev/null
helm lint "$chart_dir" >/dev/null

helm template k8s-runner "$chart_dir" \
  --set workloadNamespace=agyn-workloads \
  --set workloadEgressNetworkPolicy.enabled=true \
  --set workloadEgressNetworkPolicy.zitiWorkloadDNS.enabled=true \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.enabled=true \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.cidr=10.43.245.186/32 \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.port=2496 \
  >"$rendered"

assert_contains() {
  local expected="$1"
  if ! grep -Fq -- "$expected" "$rendered"; then
    echo "expected rendered chart to contain: $expected" >&2
    exit 1
  fi
}

assert_contains 'kind: NetworkPolicy'
assert_contains 'name: "agent-workload-egress"'
assert_contains 'namespace: "agyn-workloads"'
assert_contains 'cidr: "100.64.0.0/10"'
assert_contains 'kubernetes.io/metadata.name: kube-system'
assert_contains 'k8s-app: kube-dns'
assert_contains 'kubernetes.io/metadata.name: ziti'
assert_contains 'app.kubernetes.io/name: ziti-workload-dns'
assert_contains 'cidr: "10.43.245.186/32"'
assert_contains 'port: 2496'
assert_contains 'cidr: "0.0.0.0/0"'

udp_53_count="$(grep -F 'protocol: UDP' -A1 "$rendered" | grep -F 'port: 53' | wc -l | tr -d ' ')"
tcp_53_count="$(grep -F 'protocol: TCP' -A1 "$rendered" | grep -F 'port: 53' | wc -l | tr -d ' ')"
if [[ "$udp_53_count" -lt 2 || "$tcp_53_count" -lt 2 ]]; then
  echo "expected at least two UDP/TCP 53 rules for kube-dns and ziti-workload-dns" >&2
  exit 1
fi

if helm template k8s-runner "$chart_dir" \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.enabled=true \
  > /dev/null 2>"$error_log"; then
  echo "expected helm template to fail when ziti controller enrollment CIDR is missing" >&2
  exit 1
fi

if ! grep -Fq 'workloadEgressNetworkPolicy.zitiControllerEnrollment.cidr is required when zitiControllerEnrollment is enabled' "$error_log"; then
  echo "expected missing CIDR validation error" >&2
  cat "$error_log" >&2
  exit 1
fi
