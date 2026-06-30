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
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[0].name=controller \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[0].cidr=10.43.245.186/32 \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[0].port=2496 \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[1].name=router \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[1].cidr=10.43.245.187/32 \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[1].port=2496 \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[2].name=ingress-gateway \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[2].cidr=10.43.245.188/32 \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[2].port=443 \
  >"$rendered"

assert_contains() {
  local expected="$1"
  if ! grep -Fq -- "$expected" "$rendered"; then
    echo "expected rendered chart to contain: $expected" >&2
    exit 1
  fi
}

assert_not_contains() {
  local unexpected="$1"
  if grep -Fq -- "$unexpected" "$rendered"; then
    echo "expected rendered chart not to contain: $unexpected" >&2
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
assert_contains 'cidr: "10.43.245.187/32"'
assert_contains 'cidr: "10.43.245.188/32"'
assert_contains 'port: 2496'
assert_contains 'port: 443'
assert_contains 'cidr: "0.0.0.0/0"'
assert_not_contains '10.0.0.0/8/32'

udp_53_count="$(grep -F 'protocol: UDP' -A1 "$rendered" | grep -F 'port: 53' | wc -l | tr -d ' ')"
tcp_53_count="$(grep -F 'protocol: TCP' -A1 "$rendered" | grep -F 'port: 53' | wc -l | tr -d ' ')"
if [[ "$udp_53_count" -lt 2 || "$tcp_53_count" -lt 2 ]]; then
  echo "expected at least two UDP/TCP 53 rules for kube-dns and ziti-workload-dns" >&2
  exit 1
fi

port_2496_count="$(grep -F 'port: 2496' "$rendered" | wc -l | tr -d ' ')"
if [[ "$port_2496_count" -lt 2 ]]; then
  echo "expected controller and router underlay TCP 2496 rules" >&2
  exit 1
fi

port_443_count="$(grep -F 'port: 443' "$rendered" | wc -l | tr -d ' ')"
if [[ "$port_443_count" -lt 1 ]]; then
  echo "expected runtime Istio ingress gateway TCP 443 rule" >&2
  exit 1
fi

helm template k8s-runner "$chart_dir" \
  --set workloadNamespace=agyn-workloads \
  --set workloadEgressNetworkPolicy.enabled=true \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.enabled=true \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.cidr=10.43.245.186/32 \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.port=2496 \
  --set workloadEgressNetworkPolicy.zitiRuntimeIngressGateway.enabled=true \
  --set workloadEgressNetworkPolicy.zitiRuntimeIngressGateway.cidr=10.43.245.188/32 \
  --set workloadEgressNetworkPolicy.zitiRuntimeIngressGateway.port=443 \
  >"$rendered"
assert_contains 'cidr: "10.43.245.186/32"'
assert_contains 'port: 2496'
assert_contains 'cidr: "10.43.245.188/32"'
assert_contains 'port: 443'

if helm template k8s-runner "$chart_dir" \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[0].name=controller \
  --set workloadEgressNetworkPolicy.zitiUnderlay.endpoints[0].port=2496 \
  > /dev/null 2>"$error_log"; then
  echo "expected helm template to fail when ziti underlay endpoint CIDR is missing" >&2
  exit 1
fi

if ! grep -Fq 'workloadEgressNetworkPolicy.zitiUnderlay.endpoints[controller].cidr is required' "$error_log"; then
  echo "expected missing underlay endpoint CIDR validation error" >&2
  cat "$error_log" >&2
  exit 1
fi

if helm template k8s-runner "$chart_dir" \
  --set workloadEgressNetworkPolicy.zitiControllerEnrollment.enabled=true \
  > /dev/null 2>"$error_log"; then
  echo "expected helm template to fail when deprecated ziti controller enrollment CIDR is missing" >&2
  exit 1
fi

if ! grep -Fq 'workloadEgressNetworkPolicy.zitiUnderlay.endpoints[zitiControllerEnrollment].cidr is required' "$error_log"; then
  echo "expected missing deprecated controller CIDR validation error" >&2
  cat "$error_log" >&2
  exit 1
fi

if helm template k8s-runner "$chart_dir" \
  --set workloadEgressNetworkPolicy.zitiRuntimeIngressGateway.enabled=true \
  > /dev/null 2>"$error_log"; then
  echo "expected helm template to fail when runtime ingress gateway CIDR is missing" >&2
  exit 1
fi

if ! grep -Fq 'workloadEgressNetworkPolicy.zitiUnderlay.endpoints[zitiRuntimeIngressGateway].cidr is required' "$error_log"; then
  echo "expected missing runtime ingress gateway CIDR validation error" >&2
  cat "$error_log" >&2
  exit 1
fi
