# Proxmox
variable "proxmox_endpoint" {
    type = string
    description = "Proxmox API endpoint"
}

variable "proxmox_api_token" {
    type = string
    description = "Proxmox API token"
    sensitive = true
}

variable "proxmox_datastore" {
    type = string
    description = "Name of Proxmox data store to use"
}

variable "proxmox_node" {
    type = string
    description = "name of Proxmox node to use"
}

# Talos
variable "talos_version" {
    type = string
    description = "Talos Linux version"
}

variable "talos_image_schematic_id" {
    type = string
    description = "Talos Schematic ID identifying the exact image"
}

