output "local_path_provisioner_release" {
  description = "Local Path Provisioner Helm release details"
  value = {
    name      = helm_release.local_path_provisioner.name
    namespace = helm_release.local_path_provisioner.namespace
    status    = helm_release.local_path_provisioner.status
    version   = helm_release.local_path_provisioner.version
  }
}
