# Proxmox
variable "proxmox_endpoint" {
  type        = string
  description = "Proxmox API endpoint"
}

variable "proxmox_api_token" {
  type        = string
  description = "Proxmox API token"
  sensitive   = true
}

variable "proxmox_iso_datastore" {
  type        = string
  description = "Name of Proxmox data store to use for ISO images"
}

variable "proxmox_vm_datastore" {
  type        = string
  description = "Name of Proxmox data store to use for VMs"
}

variable "proxmox_node" {
  type        = string
  description = "name of Proxmox node to use"
}

variable "proxmox_network_bridge" {
  type        = string
  description = "name of Proxmox network bridge to use"
  default     = "vmbr0"
}

# Talos
variable "talos_version" {
  type        = string
  description = "Talos Linux version"
}

variable "talos_image_schematic_id" {
  type        = string
  description = "Talos Schematic ID identifying the exact image"
}

variable "talos_nodes" {
  description = "List of nodes and their configurations"
  type = map(object({
    proxmox_node   = string
    vm_id          = number
    mac            = string
    cores          = number
    memory         = number
    data_disk_size = number
  }))
}

variable "talos_node_count" {
  type        = number
  description = "Number of Talos nodes on Proxmox, must be 1 or 3"
  validation {
    condition     = contains([1, 3], var.talos_node_count)
    error_message = "talos_node_count must be either 1 or 3."
  }
}
