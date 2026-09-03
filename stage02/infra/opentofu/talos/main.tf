terraform {
  required_providers {
    talos = {
      source  = "siderolabs/talos"
      version = "0.11.0"
    }

    helm = {
      source  = "hashicorp/helm"
      version = "3.3.0"
    }

    http = {
      source  = "hashicorp/http"
      version = "3.6.1"
    }
  }
}

# Download Gateway API manifest for Cilium
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
      error_message = "Failed to download the Gateway API manifest."
    }
  }
}

# Render Cilium Helm chart
data "helm_template" "cilium" {
  name         = "cilium"
  namespace    = "kube-system"
  chart        = "oci://quay.io/cilium/charts/cilium"
  version      = "1.20.1"
  kube_version = var.talos_kubernetes_version

  include_crds = true
  skip_tests   = true

  values = [
    file("${path.module}/cilium-values.yaml"),
    yamlencode({
      operator = {
        replicas = length(var.talos_control_node_ips) == 1 ? 1 : 2
      }
    }),
  ]
}

# Define local variables
locals {
  # Set first node IP as control node IP until we have a virtual IP
  primary_control_node_ip = var.talos_control_node_ips[0]

  # Set cluster endpoint
  cluster_endpoint = "https://${local.primary_control_node_ip}:6443"

  # Installation image name
  install_image = "factory.talos.dev/nocloud-installer-secureboot/${var.talos_image_schematic_id}:v${var.talos_version}"

  # Additional config: Set installation disk
  control_patch_install_disk = yamlencode({
    machine = {
      install = {
        disk = var.talos_install_disk
      }
    }
  })

  # Additional config: Set the installation image used. Needs the secureboot variety, if secureboot is used
  control_patch_install_image = yamlencode({
    machine = {
      install = {
        image = local.install_image
      }
    }
  })

  # Additional config: Set network interface
  control_patch_network = yamlencode({
    machine = {
      network = {
        interfaces = [
          {
            interface = var.talos_network_interface
            dhcp      = true
          }
        ]
      }
    }
  })

  # Additional config: Enable TPM-based disk encryption
  control_patch_disk_encryption = yamlencode({
    machine = {
      systemDiskEncryption = {
        ephemeral = {
          provider = "luks2"
          keys = [
            {
              slot = 0
              tpm  = {}
            }
          ]
        }

        state = {
          provider = "luks2"
          keys = [
            {
              slot = 0
              tpm  = {}
            }
          ]
        }
      }
    }
  })

  # Additional config: Enable running workloads on controlplane nodes
  control_patch_scheduling = yamlencode({
    cluster = {
      allowSchedulingOnControlPlanes = true
    }
  })

  # Enable certificate rotation and enable metrics server
  control_patch_metrics = yamlencode({
    machine = {
      kubelet = {
        extraArgs = {
          rotate-server-certificates = "true"
        }
      }
    }
    cluster = {
      extraManifests = [
        "https://raw.githubusercontent.com/alex1989hu/kubelet-serving-cert-approver/v0.12.0/deploy/standalone-install.yaml",
        "https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml",
      ]
    }
  })

  # Disable default CNI and kube-proxy. Will be replaced by Cilium
  control_patch_nocni = yamlencode({
    cluster = {
      network = {
        cni = {
          name = "none"
        }
      }
      proxy = {
        disabled = true
      }
    }
  })

  # Create Cilium inline manifest
  control_patch_cilium = yamlencode({
    cluster = {
      inlineManifests = [
        {
          name     = "gateway-api"
          contents = data.http.gateway_api.response_body
        },
        {
          name     = "cilium"
          contents = data.helm_template.cilium.manifest
        },
      ]
    }
  })
}

# Create machine secrets
resource "talos_machine_secrets" "machine_secrets" {
  talos_version = "v${var.talos_version}"
}

# Generate client config
data "talos_client_configuration" "client_config" {
  cluster_name         = var.talos_cluster_name
  client_configuration = talos_machine_secrets.machine_secrets.client_configuration
  endpoints            = var.talos_control_node_ips
  nodes                = var.talos_control_node_ips
}

# Configure control machines
data "talos_machine_configuration" "control_machine_config" {
  cluster_name       = var.talos_cluster_name
  cluster_endpoint   = local.cluster_endpoint
  machine_type       = "controlplane"
  machine_secrets    = talos_machine_secrets.machine_secrets.machine_secrets
  kubernetes_version = "v${var.talos_kubernetes_version}"
  talos_version      = "v${var.talos_version}"

  config_patches = [
    local.control_patch_install_disk,
    local.control_patch_install_image,
    local.control_patch_network,
    local.control_patch_disk_encryption,
    local.control_patch_scheduling,
    local.control_patch_metrics,
    local.control_patch_nocni,
    local.control_patch_cilium,
  ]
}

# Apply config to machines
resource "talos_machine_configuration_apply" "control_machine_config_apply" {
  for_each                    = toset(var.talos_control_node_ips)
  client_configuration        = talos_machine_secrets.machine_secrets.client_configuration
  machine_configuration_input = data.talos_machine_configuration.control_machine_config.machine_configuration
  node                        = each.value
}

# Bootstrap the cluster
resource "talos_machine_bootstrap" "bootstrap" {
  depends_on           = [talos_machine_configuration_apply.control_machine_config_apply]
  client_configuration = talos_machine_secrets.machine_secrets.client_configuration
  node                 = local.primary_control_node_ip
  endpoint             = local.primary_control_node_ip
}

# Wait until Talos and Kubernetes are healthy
data "talos_cluster_health" "health" {
  depends_on = [
    talos_machine_bootstrap.bootstrap
  ]

  client_configuration = talos_machine_secrets.machine_secrets.client_configuration
  control_plane_nodes  = var.talos_control_node_ips
  endpoints            = var.talos_control_node_ips

  timeouts = {
    read = "15m"
  }
}

resource "talos_cluster_kubeconfig" "kubeconfig" {
  depends_on = [
    data.talos_cluster_health.health
  ]

  client_configuration = talos_machine_secrets.machine_secrets.client_configuration
  node                 = local.primary_control_node_ip
  endpoint             = local.primary_control_node_ip
}
