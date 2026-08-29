output "file_id" {
  description = "Proxmox file ID of the downloaded Talos image"
  value       = proxmox_download_file.talos_image.id
}

output "file_name" {
  description = "Filename of the downloaded Talos image"
  value       = proxmox_download_file.talos_image.file_name
}

output "talos_version" {
  description = "Talos version represented by the downloaded image"
  value       = var.talos_version
}

output "talos_image_schematic_id" {
  description = "Talos Image Factory schematic represented by the image"
  value       = var.talos_image_schematic_id
}
