variable "cilium_version" {
  type        = string
  description = "Cilium Helm chart version; update the matching DHI image pins in cilium-values.yaml when changing this"
  default     = "1.20.1"
}

variable "check_cluster_health" {
  type        = bool
  description = "Wait for Talos cluster health; temporarily disable for state imports/recovery while Cilium is unavailable"
  default     = true
}
