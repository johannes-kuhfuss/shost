#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: extract-talos-config.sh [--overwrite]

Extract talosconfig and kubeconfig from the Talos OpenTofu state.
Existing configuration files are skipped unless --overwrite (or -f) is used.
EOF
}

overwrite=false

while (($# > 0)); do
  case "$1" in
    -f | --overwrite)
      overwrite=true
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown option: %s\n\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

if ! command -v tofu >/dev/null 2>&1; then
  printf 'OpenTofu (tofu) was not found in PATH.\n' >&2
  exit 1
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
talos_root="${script_dir}/../opentofu/talos"

extract_config() {
  local output_name="$1"
  local destination="$2"
  local destination_dir
  local temporary_file

  if [[ -e "${destination}" || -L "${destination}" ]]; then
    if [[ "${overwrite}" != true ]]; then
      printf 'Skipping existing file: %s\n' "${destination}"
      return 0
    fi
  fi

  destination_dir="$(dirname -- "${destination}")"
  install -d -m 700 -- "${destination_dir}"
  temporary_file="$(mktemp "${destination_dir}/.config.tmp.XXXXXX")"

  if ! tofu -chdir="${talos_root}" output -raw "${output_name}" >"${temporary_file}"; then
    rm -f -- "${temporary_file}"
    printf 'Failed to extract OpenTofu output: %s\n' "${output_name}" >&2
    return 1
  fi

  chmod 600 -- "${temporary_file}"
  mv -f -- "${temporary_file}" "${destination}"
  printf 'Written: %s\n' "${destination}"
}

extract_config talosconfig "${HOME:?HOME must be set}/.talos/config"
extract_config kubeconfig "${HOME}/.kube/config"
