variable "proxmox_endpoint" {
  type        = string
  description = "Proxmox API endpoint"
}

variable "proxmox_insecure" {
  type        = bool
  description = "Disable TLS certificate verification for the Proxmox API"
  default     = false
}

variable "proxmox_iso_datastore" {
  type        = string
  description = "Name of the Proxmox datastore used for downloaded images"
}

variable "proxmox_node" {
  type        = string
  description = "Name of the Proxmox node that stores the image"
}

variable "talos_version" {
  type        = string
  description = "Talos Linux version"
}

variable "talos_image_schematic_id" {
  type        = string
  description = "Talos Image Factory schematic ID"
}
