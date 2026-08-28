terraform {
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "0.111.1"
    }
    talos = {
      source  = "siderolabs/talos"
      version = "0.11.0"
    }
  }
}

provider "proxmox" {
  endpoint  = var.proxmox_endpoint
  api_token = var.proxmox_api_token
  insecure  = true

  ssh {
    agent = true
    usernmame = "opentofu"
  }
}

resource "proxmox_download_file" "talos_image" {
  content_type            = "iso"
  datastore_id            = var.proxmox_iso_datastore
  node_name               = var.proxmox_node
  url                     = "https://factory.talos.dev/image/${var.talos_image_schematic_id}/v${var.talos_version}/nocloud-amd64-secureboot.raw.xz"
  decompression_algorithm = "zst"
  file_name               = "talos-v${var.talos_version}-${var.talos_image_schematic_id}-nocloud-amd64-secureboot.img"
  overwrite               = false
}

# Filter talos_nodes based on talos_node_count
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
    file_id      = proxmox_download_file.talos_image.id
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

  boot_order = [
    "scsi0",
  ]

  started = true
}