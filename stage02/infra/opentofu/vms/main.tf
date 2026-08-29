terraform {
  required_version = ">= 1.9.0"

  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "0.111.1"
    }
  }
}

provider "proxmox" {
  endpoint = var.proxmox_endpoint
  insecure = var.proxmox_insecure

  ssh {
    agent    = true
    username = var.proxmox_ssh_username
  }
}

data "terraform_remote_state" "image" {
  backend = "local"

  config = {
    path = "${path.module}/../image/terraform.tfstate"
  }
}

locals {
  selected_talos_nodes = {
    for name in slice(
      sort(keys(var.talos_nodes)),
      0,
      var.talos_node_count
    ) : name => var.talos_nodes[name]
  }
}

resource "proxmox_virtual_environment_vm" "node" {
  for_each = local.selected_talos_nodes

  name      = each.key
  node_name = each.value.proxmox_node
  vm_id     = each.value.vm_id

  machine       = "q35"
  bios          = "ovmf"
  scsi_hardware = "virtio-scsi-pci"

  agent {
    enabled = true
  }

  efi_disk {
    datastore_id      = var.proxmox_vm_datastore
    type              = "4m"
    pre_enrolled_keys = false
  }

  tpm_state {
    datastore_id = var.proxmox_vm_datastore
    version      = "v2.0"
  }

  disk {
    datastore_id = var.proxmox_vm_datastore
    interface    = "scsi0"
    size         = 32
    cache        = "none"
    discard      = "on"
    file_id      = data.terraform_remote_state.image.outputs.file_id
  }

  disk {
    datastore_id = var.proxmox_vm_datastore
    interface    = "scsi1"
    size         = each.value.data_disk_size
    cache        = "none"
    discard      = "on"
  }

  cpu {
    cores = each.value.cores
    type  = "host"
  }

  memory {
    dedicated = each.value.memory
  }

  network_device {
    bridge      = var.proxmox_network_bridge
    model       = "virtio"
    mac_address = each.value.mac
  }

  boot_order = ["scsi0"]
  started    = true

  lifecycle {
    precondition {
      condition     = try(data.terraform_remote_state.image.outputs.file_id, null) != null
      error_message = "The image root has no Talos image file_id. Apply ../image before planning or applying the VM root."
    }
  }
}
