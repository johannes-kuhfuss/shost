# Stage 02 OpenTofu stacks

The infrastructure is split into independently managed root modules with separate state files:

- `image/` downloads and owns the Talos image in Proxmox.
- `vms/` creates Talos VMs and reads the image file ID from `image/terraform.tfstate`.

Destroying the VM root does not destroy the image.

## Credentials

Both roots use the provider-native `PROXMOX_VE_API_TOKEN` environment variable. The VM root also uses the local SSH agent with the Linux user configured by `proxmox_ssh_username`.

```sh
export PROXMOX_VE_API_TOKEN='opentofu@pve!token-id=token-secret'
ssh-add ~/.ssh/proxmox_opentofu
```

The provider does not read `~/.ssh/config`.

## Configuration

Create the ignored variable files from the tracked examples and adjust their values:

```sh
cp image/terraform.tfvars.example image/terraform.tfvars
cp vms/terraform.tfvars.example vms/terraform.tfvars
```

## Apply order

Initialize and apply the image first, followed by the VMs:

```sh
tofu -chdir=image init
tofu -chdir=image apply

tofu -chdir=vms init
tofu -chdir=vms apply
```

The VM root uses a local `terraform_remote_state` data source. If the image state is moved to a remote backend later, update the data source in `vms/main.tf` to use that backend.
