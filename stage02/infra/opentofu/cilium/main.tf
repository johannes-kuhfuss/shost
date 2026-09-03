terraform {
  required_version = ">= 1.9.0"

  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "3.3.0"
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
}

provider "helm" {
  kubernetes = {
    host                   = local.kube_cluster.server
    cluster_ca_certificate = base64decode(local.kube_cluster["certificate-authority-data"])
    client_certificate     = base64decode(local.kube_user["client-certificate-data"])
    client_key             = base64decode(local.kube_user["client-key-data"])
  }
}

resource "helm_release" "cilium" {
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
