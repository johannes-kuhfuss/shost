terraform {
  required_version = ">= 1.9.0"

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "2.38.0"
    }
  }
}

data "terraform_remote_state" "talos" {
  backend = "local"

  config = {
    path = "${path.module}/../talos/terraform.tfstate"
  }
}

data "terraform_remote_state" "cilium" {
  backend = "local"

  config = {
    path = "${path.module}/../cilium/terraform.tfstate"
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

provider "kubernetes" {
  host                   = local.kube_cluster.server
  cluster_ca_certificate = base64decode(local.kube_cluster["certificate-authority-data"])
  client_certificate     = base64decode(local.kube_user["client-certificate-data"])
  client_key             = base64decode(local.kube_user["client-key-data"])
}

resource "kubernetes_manifest" "load_balancer_ip_pool" {
  manifest = {
    apiVersion = "cilium.io/v2"
    kind       = "CiliumLoadBalancerIPPool"

    metadata = {
      name = var.cilium_lb_ip_pool.name
    }

    spec = {
      blocks = [
        {
          start = var.cilium_lb_ip_pool.start
          stop  = var.cilium_lb_ip_pool.stop
        }
      ]
    }
  }

  field_manager {
    force_conflicts = true
  }

  lifecycle {
    precondition {
      condition     = data.terraform_remote_state.cilium.outputs.cilium_release.status == "deployed"
      error_message = "The Cilium Helm release must be deployed before applying Cilium custom resources."
    }
  }
}

resource "kubernetes_manifest" "l2_announcement_policy" {
  manifest = {
    apiVersion = "cilium.io/v2alpha1"
    kind       = "CiliumL2AnnouncementPolicy"

    metadata = {
      name = var.cilium_l2_announcement_policy_name
    }

    spec = {
      loadBalancerIPs = true
    }
  }

  field_manager {
    force_conflicts = true
  }

  lifecycle {
    precondition {
      condition     = data.terraform_remote_state.cilium.outputs.cilium_release.status == "deployed"
      error_message = "The Cilium Helm release must be deployed before applying Cilium custom resources."
    }
  }
}
