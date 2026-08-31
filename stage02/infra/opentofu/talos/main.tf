terraform {
  required_providers {
    talos = {
      source  = "siderolabs/talos"
      version = "0.11.0"
    }
  }
}

resource "talos_machine_secrets" "machine_secrets" {
  talos_version = "v${var.talos_version}"
}

data "talos_client_configuration" "client_config" {
  cluster_name         = var.talos_cluster_name
  client_configuration = talos_machine_secrets.machine_secrets.client_configuration
  endpoints            = var.talos_control_nodes
  nodes                = var.talos_control_nodes
}

locals {
  primary_control_node_ip = var.talos_control_nodes[0]
}

data "talos_machine_configuration" "control_machine_config" {
  cluster_name       = var.talos_cluster_name
  cluster_endpoint   = local.cluster_endpoint
  machine_type       = "controlplane"
  machine_secrets    = talos_machine_secrets.machine_secrets.machine_secrets
  kubernetes_version = "v${var.kubernetes_version}"
  talos_version      = "v${var.talos_version}"

  config_patches = []
}

# Set installation disk
data "talos_machine_configuration" "control_machine_config" {
  config_patches = [
    yamlencode({
      machine = {
        install = {
          disk = var.talos_install_disk
        }
      }
    })
  ]
}

locals {
  install_image = "factory.talos.dev/nocloud-installer-secureboot/${var.talos_image_schematic_id}:v${var.talos_version}"
}

# Set the installation image used. Needs the secureboot variety, if secureboot is used
data "talos_machine_configuration" "control_machine_config" {
  config_patches = [
    yamlencode({
      machine = {
        install = {
          image = local.install_image
        }
      }
    })
  ]
}

# Set network interface
data "talos_machine_configuration" "control_machine_config" {
  config_patches = [
    yamlencode({
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
  ]
}

# Enable TPM-based disk encryption
data "talos_machine_configuration" "control_machine_config" {
  config_patches = [
    yamlencode({
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
  ]
}

# Enable running workloads on controlplane nodes
data "talos_machine_configuration" "control_machine_config" {
  config_patches = [
    yamlencode({
      cluster = {
        allowSchedulingOnControlPlanes = true
      }
    })
  ]
}
