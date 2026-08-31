variable "talos_version" {
  type        = string
  description = "Talos Version"
}

variable "talos_image_schematic_id" {
  type        = string
  description = "Talos Image Factory schematic ID"
}

variable "talos_kubernetes_version" {
  type        = string
  description = "Kubernetes Version on Talos"
}

variable "talos_cluster_name" {
  type        = string
  description = "Cluster Name"
  default     = "talos-cluster"
}

variable "talos_control_nodes" {
  type        = list(string)
  description = "IPs of the Talos control nodes"

  validation {
    condition     = length(var.talos_control_nodes) == 1 || length(var.talos_control_nodes) == 3
    error_message = "You must specify either 1 or 3 nodes."
  }
}

variable "talos_install_disk" {
  type        = string
  description = "Disk onto which Talos is installed"
  default     = "/dev/sda"
}

variable "talos_network_interface" {
  type        = string
  description = "Network interface to use"
  default     = "eth0"
}
