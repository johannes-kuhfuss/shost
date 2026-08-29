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
