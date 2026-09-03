variable "cilium_lb_ip_pool" {
  type = object({
    name  = string
    start = string
    stop  = string
  })
  description = "Name and inclusive IPv4 address range for the Cilium load-balancer pool"

  default = {
    name  = "lan-pool"
    start = "192.168.200.220"
    stop  = "192.168.200.239"
  }

  validation {
    condition = (
      can(cidrnetmask("${var.cilium_lb_ip_pool.start}/32")) &&
      can(cidrnetmask("${var.cilium_lb_ip_pool.stop}/32"))
    )
    error_message = "The load-balancer pool start and stop values must be valid IPv4 addresses."
  }
}

variable "cilium_l2_announcement_policy_name" {
  type        = string
  description = "Name of the Cilium L2 announcement policy"
  default     = "lan-l2-policy"
}
