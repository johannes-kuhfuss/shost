terraform {
  required_version = ">= 1.9.0"

  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "3.3.0"
    }

    http = {
      source  = "hashicorp/http"
      version = "3.6.1"
    }

    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "2.38.0"
    }

    talos = {
      source  = "siderolabs/talos"
      version = "0.11.0"
    }
  }
}

data "terraform_remote_state" "talos" {
  backend = "local"

  config = {
    path = "${path.module}/../talos/terraform.tfstate"
  }
}

data "http" "gateway_api" {
  url = "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.1/standard-install.yaml"

  retry {
    attempts     = 2
    min_delay_ms = 1000
    max_delay_ms = 5000
  }

  lifecycle {
    postcondition {
      condition     = self.status_code == 200
      error_message = "Failed to download the Gateway API CRDs."
    }
  }
}

locals {
  kubeconfig = yamldecode(data.terraform_remote_state.talos.outputs.kubeconfig)

  kube_context = one([
    for context in local.kubeconfig.contexts : context.context
    if context.name == local.kubeconfig["current-context"]
  ])

  kube_cluster = one([
    for cluster in local.kubeconfig.clusters : cluster.cluster
    if cluster.name == local.kube_context.cluster
  ])

  kube_user = one([
    for user in local.kubeconfig.users : user.user
    if user.name == local.kube_context.user
  ])

  gateway_api_manifests = {
    for manifest in provider::kubernetes::manifest_decode_multi(data.http.gateway_api.response_body) :
    "${manifest.kind}/${manifest.metadata.name}" => manifest
  }
}

provider "helm" {
  kubernetes = {
    host                   = local.kube_cluster.server
    cluster_ca_certificate = base64decode(local.kube_cluster["certificate-authority-data"])
    client_certificate     = base64decode(local.kube_user["client-certificate-data"])
    client_key             = base64decode(local.kube_user["client-key-data"])
  }
}

provider "kubernetes" {
  host                   = local.kube_cluster.server
  cluster_ca_certificate = base64decode(local.kube_cluster["certificate-authority-data"])
  client_certificate     = base64decode(local.kube_user["client-certificate-data"])
  client_key             = base64decode(local.kube_user["client-key-data"])
}

# Install the Gateway API CRDs and wait for API discovery to register them
# before Helm renders Cilium's GatewayClass resource.
resource "kubernetes_manifest" "gateway_api" {
  for_each = local.gateway_api_manifests

  manifest = each.value

  field_manager {
    force_conflicts = true
  }

  wait {
    condition {
      type   = "Established"
      status = "True"
    }
  }
}

resource "helm_release" "cilium" {
  depends_on = [kubernetes_manifest.gateway_api]

  name      = "cilium"
  namespace = "kube-system"

  chart   = "oci://quay.io/cilium/charts/cilium"
  version = var.cilium_version

  values = [
    file("${path.module}/cilium-values.yaml"),
    yamlencode({
      operator = {
        replicas = length(data.terraform_remote_state.talos.outputs.control_plane_nodes) == 1 ? 1 : 2
      }
    }),
  ]

  atomic         = true
  take_ownership = true
  timeout        = 900
  wait           = true
  wait_for_jobs  = true
}

# Do not finish the Cilium apply until Talos and Kubernetes report healthy.
data "talos_cluster_health" "health" {
  depends_on = [helm_release.cilium]

  client_configuration = data.terraform_remote_state.talos.outputs.talos_client_configuration
  control_plane_nodes  = data.terraform_remote_state.talos.outputs.control_plane_nodes
  endpoints            = data.terraform_remote_state.talos.outputs.control_plane_nodes

  timeouts = {
    read = "15m"
  }
}
