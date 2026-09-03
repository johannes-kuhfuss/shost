# Stage 02 OpenTofu stacks

The infrastructure is split into independently managed root modules with separate state files:

- `image/` downloads and owns the Talos image in Proxmox.
- `vms/` creates Talos VMs and reads the image file ID from `image/terraform.tfstate`.
- `talos/` configures and bootstraps Talos, then produces the Kubernetes credentials.
- `cilium/` installs Cilium with Helm and waits for the complete cluster to become healthy.
- `cilium-config/` configures the Cilium load-balancer IP pool and L2 announcements.

Destroying one root does not automatically destroy resources owned by another root.

## Credentials

The image and VM roots use the provider-native `PROXMOX_VE_API_TOKEN` environment variable. The VM root also uses the local SSH agent with the Linux user configured by `proxmox_ssh_username`.

```sh
export PROXMOX_VE_API_TOKEN='opentofu@pve!token-id=token-secret'
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/proxmox_opentofu
```

The provider does not read `~/.ssh/config`.

## Configuration

Create the ignored variable files from the tracked examples and adjust their values:

```sh
cp image/terraform.tfvars.example image/terraform.tfvars
cp vms/terraform.tfvars.example vms/terraform.tfvars
cp talos/terraform.tfvars.example talos/terraform.tfvars
cp cilium/terraform.tfvars.example cilium/terraform.tfvars
cp cilium-config/terraform.tfvars.example cilium-config/terraform.tfvars
```

## Apply order

Initialize and apply the roots in dependency order:

```sh
tofu -chdir=image init
tofu -chdir=image plan
tofu -chdir=image apply

tofu -chdir=vms init
tofu -chdir=vms plan
tofu -chdir=vms apply

tofu -chdir=talos init
tofu -chdir=talos plan
tofu -chdir=talos apply

tofu -chdir=cilium init
tofu -chdir=cilium plan
tofu -chdir=cilium apply

tofu -chdir=cilium-config init
tofu -chdir=cilium-config plan
tofu -chdir=cilium-config apply
```

The Talos root waits for Talos and the Kubernetes control plane, but skips workload checks because the cluster deliberately has no CNI at that point. The Cilium root installs the Gateway API CRDs, waits for them to become established, installs the chart, waits for Helm resources and jobs, and then runs the full Talos cluster-health check. A successful Cilium apply therefore means the bootstrapped cluster, CNI, and Kubernetes workloads are healthy.

The Helm release has `take_ownership` enabled so it can adopt Cilium resources from the earlier inline-manifest approach. After the first successful Helm apply, Cilium upgrades and removal are managed by the Cilium root.

The load-balancer configuration is kept in a separate root because its Cilium custom-resource definitions must already exist when OpenTofu plans those resources. Adjust the address range in `cilium-config/terraform.tfvars` so it is unused on the LAN and excluded from DHCP allocation.

The VM, Cilium, and Cilium configuration roots use local `terraform_remote_state` data sources. If state is moved to a remote backend later, update those data sources to use the same backend.

Destroy in reverse order so Helm can still reach the Kubernetes API while uninstalling Cilium:

```sh
tofu -chdir=cilium-config destroy
tofu -chdir=cilium destroy
tofu -chdir=talos destroy
tofu -chdir=vms destroy
tofu -chdir=image destroy
```

## Store talosconfig and kubeconfig

Caution: this will overwrite an existing talosconfig and kubeconfig.

```sh
install -d -m 700 ~/.talos ~/.kube

tofu -chdir=talos output -raw talosconfig > ~/.talos/config
tofu -chdir=talos output -raw kubeconfig > ~/.kube/config

chmod 600 ~/.talos/config ~/.kube/config
```
