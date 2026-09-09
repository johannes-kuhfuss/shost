terraform {
  required_version = ">= 1.9.0"

  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "3.3.0"
    }

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

resource "kubernetes_namespace_v1" "local_path_storage" {
  metadata {
    name = "local-path-storage"

    labels = {
      "pod-security.kubernetes.io/enforce" = "privileged"
    }
  }
}

resource "helm_release" "local_path_provisioner" {
  name      = "local-path-provisioner"
  namespace = kubernetes_namespace_v1.local_path_storage.metadata[0].name

  chart   = "oci://ghcr.io/rancher/local-path-provisioner/charts/local-path-provisioner"
  version = "0.0.37"

  values = [
    file("${path.module}/local-path-provisioner-values.yaml"),
  ]

  atomic        = true
  timeout       = 600
  wait          = true
  wait_for_jobs = true

  lifecycle {
    precondition {
      condition     = data.terraform_remote_state.cilium.outputs.cilium_release.status == "deployed"
      error_message = "The Cilium Helm release must be deployed before installing local-path-provisioner."
    }
  }
}
