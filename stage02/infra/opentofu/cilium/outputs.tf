output "cilium_release" {
  description = "Cilium Helm release details consumed by dependent OpenTofu roots"
  value = {
    name      = helm_release.cilium.name
    namespace = helm_release.cilium.namespace
    status    = helm_release.cilium.status
    version   = helm_release.cilium.version
  }
}
