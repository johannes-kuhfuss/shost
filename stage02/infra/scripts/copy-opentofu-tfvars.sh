#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
opentofu_dir="${script_dir}/../opentofu"

roots=(
  image
  vms
  talos
  cilium
  cilium-config
)

for root in "${roots[@]}"; do
  source_file="${opentofu_dir}/${root}/terraform.tfvars.example"
  destination_file="${opentofu_dir}/${root}/terraform.tfvars"

  if [[ -e "${destination_file}" || -L "${destination_file}" ]]; then
    printf 'Skipping existing file: %s\n' "${destination_file}"
    continue
  fi

  cp -- "${source_file}" "${destination_file}"
  printf 'Created: %s\n' "${destination_file}"
done
