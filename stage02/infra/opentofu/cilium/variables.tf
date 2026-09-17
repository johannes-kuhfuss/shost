variable "cilium_version" {
  type        = string
  description = "Cilium Helm chart version; update the matching DHI image pins in cilium-values.yaml when changing this"
  default     = "1.20.1"
}
