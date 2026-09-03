output "talosconfig" {
  description = "Talos client configuration"
  value       = data.talos_client_configuration.client_config.talos_config
  sensitive   = true
}

output "kubeconfig" {
  description = "Kubernetes client configuration"
  value       = talos_cluster_kubeconfig.kubeconfig.kubeconfig_raw
  sensitive   = true
}

output "talos_client_configuration" {
  description = "Talos client credentials used by dependent OpenTofu roots"
  value       = talos_machine_secrets.machine_secrets.client_configuration
  sensitive   = true
}

output "control_plane_nodes" {
  description = "Talos control-plane node addresses"
  value       = var.talos_control_node_ips
}
