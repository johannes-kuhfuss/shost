#!/usr/bin/env bash

# Format: <full-tokenid>=<value> from the pveum token creation output.
# For the guide's token: opentofu@pve!opentofu=<UUID secret>.
# Set the environment variable before sourcing, or enter it at the prompt.
PROXMOX_VE_API_TOKEN_DEFAULT='opentofu@pve!opentofu=<token-secret>'
PROXMOX_VE_API_TOKEN="${PROXMOX_VE_API_TOKEN:-$PROXMOX_VE_API_TOKEN_DEFAULT}"

# Source this file so the token and SSH agent variables remain available to
# OpenTofu commands executed in the current shell.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  printf 'Run this script with: source %q\n' "$0" >&2
  exit 1
fi

if [[ -z "${PROXMOX_VE_API_TOKEN:-}" ||
      "${PROXMOX_VE_API_TOKEN}" == "${PROXMOX_VE_API_TOKEN_DEFAULT}" ]]; then
  read -r -s -p \
    'Proxmox API token (opentofu@pve!opentofu=<UUID secret>): ' \
    PROXMOX_VE_API_TOKEN
  printf '\n'
fi

if [[ -z "${PROXMOX_VE_API_TOKEN}" ||
      "${PROXMOX_VE_API_TOKEN}" == "${PROXMOX_VE_API_TOKEN_DEFAULT}" ]]; then
  printf 'PROXMOX_VE_API_TOKEN must not be empty or use the default value.\n' >&2
  return 1
fi

export PROXMOX_VE_API_TOKEN

if [[ -z "${SSH_AUTH_SOCK:-}" ]] || ! ssh-add -l >/dev/null 2>&1; then
  eval "$(ssh-agent -s)"
fi

ssh-add "${HOME}/.ssh/proxmox_opentofu"
