# Stage 02 OpenTofu stacks

The infrastructure is split into two independently managed root modules with separate state files:

- `image/` downloads and owns the Talos image in Proxmox.
- `vms/` creates Talos VMs and reads the image file ID from `image/terraform.tfstate`.

Destroying the VM root does not destroy the image:

```sh
tofu -chdir=vms destroy
```

Only running `tofu -chdir=image destroy` destroys the downloaded image.

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

## Migrating the previous single-root local state

The previous state contains the image at `proxmox_download_file.talos_image`. Move it into the image root before planning either new root:

```sh
tofu state mv \
  -state terraform.tfstate \
  -state-out image/terraform.tfstate \
  proxmox_download_file.talos_image \
  proxmox_download_file.talos_image
```

Verify the migration before continuing:

```sh
tofu -chdir=image state list
tofu state list
```

The image address should appear only in the first command. Keep the automatically generated state backups until the migration and both plans have been verified.

Apply the image root once after migration. This both reconciles any pending image filename change and records the outputs consumed by the VM root:

```sh
tofu -chdir=image plan
tofu -chdir=image apply
```

Do not apply the VM root until `tofu -chdir=image output -raw file_id` returns a non-empty Proxmox file ID.
