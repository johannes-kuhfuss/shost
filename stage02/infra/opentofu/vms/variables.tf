variable "proxmox_endpoint" {
  type        = string
  description = "Proxmox API endpoint"
}

variable "proxmox_insecure" {
  type        = bool
  description = "Disable TLS certificate verification for the Proxmox API"
  default     = false
}

variable "proxmox_ssh_username" {
  type        = string
  description = "Linux user used for SSH operations on the Proxmox node"
  default     = "opentofu"
}

variable "proxmox_vm_datastore" {
  type        = string
  description = "Name of the Proxmox datastore used for VM disks"
}

variable "proxmox_network_bridge" {
  type        = string
  description = "Name of the Proxmox network bridge used by the VMs"
  default     = "vmbr0"
}

variable "talos_nodes" {
  description = "Candidate Talos nodes and their VM configurations"
  type = map(object({
    proxmox_node   = string
    vm_id          = number
    mac            = string
    cores          = number
    memory         = number
    data_disk_size = number
  }))

  validation {
    condition     = length(var.talos_nodes) >= 3
    error_message = "talos_nodes must define at least three candidate nodes."
  }
}

variable "talos_node_count" {
  type        = number
  description = "Number of Talos nodes to create; must be 1 or 3"

  validation {
    condition     = contains([1, 3], var.talos_node_count)
    error_message = "talos_node_count must be either 1 or 3."
  }
}
