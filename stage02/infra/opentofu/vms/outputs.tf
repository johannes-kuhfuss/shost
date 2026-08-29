output "nodes" {
  description = "Talos VM identifiers and network information"
  value = {
    for name, node in proxmox_virtual_environment_vm.node : name => {
      vm_id          = node.vm_id
      proxmox_node   = node.node_name
      mac_addresses  = node.mac_addresses
      ipv4_addresses = node.ipv4_addresses
    }
  }
}
