# Stage 02 – Automating the Installation

## Sources

- <https://registry.terraform.io/providers/bpg/proxmox/latest/docs>
- <https://registry.terraform.io/providers/siderolabs/talos/latest/docs>
- <https://www.jonashietala.se/blog/2026/05/22/talos_linux_on_proxmox_with_terraform/>
- <https://opentofu.org/docs/intro/install/standalone/>
- <https://docs.siderolabs.com/talos/v1.13/getting-started/talosctl>
- <https://github.com/rancher/local-path-provisioner/tree/v0.0.37/deploy/chart/local-path-provisioner>

## Prepare a deployment machine

### Install prerequisites

Update the machine:

```bash
sudo apt update
sudo apt upgrade
```

Install unzip, cosign, curl, jq and git:

```bash
sudo apt install unzip cosign curl jq git
```

Install the Talos control binary:

```bash
curl -sL https://talos.dev/install | sh
```

Download kubectl:

```bash
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
```

Make the binary executable and move it to `/usr/local/bin`:

```bash
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
rm kubectl
```

Install Helm:

```bash
curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4
chmod 700 get_helm.sh
./get_helm.sh
rm get_helm.sh
```

Install Cilium CLI:

```bash
CILIUM_CLI_VERSION=$(curl -s https://raw.githubusercontent.com/cilium/cilium-cli/main/stable.txt)
CLI_ARCH=amd64
curl -L --fail --remote-name-all https://github.com/cilium/cilium-cli/releases/download/${CILIUM_CLI_VERSION}/cilium-linux-${CLI_ARCH}.tar.gz{,.sha256sum}
sha256sum --check cilium-linux-${CLI_ARCH}.tar.gz.sha256sum
sudo tar xzvfC cilium-linux-${CLI_ARCH}.tar.gz /usr/local/bin
rm cilium-linux-${CLI_ARCH}.tar.gz{,.sha256sum}
```

Install OpenTofu:

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://get.opentofu.org/install-opentofu.sh -o install-opentofu.sh
```

Inspect the script.

```bash
chmod +x install-opentofu.sh
./install-opentofu.sh --install-method standalone
rm -f install-opentofu.sh
```

## Create Proxmox API Token and setup SSH

Log into the Proxmox node as root via SSH.

Create the Proxmox API user:

```bash
pveum user add opentofu@pve --comment "OpenTofu automation"
```

Create the supplemental role:

```bash
pveum role add OpenTofuImageDownload -privs "Sys.Audit Sys.Modify Datastore.AllocateTemplate"
```

Assign the roles, assuming the datastores are named `local` and `local-lvm`:

```bash
pveum aclmod / -user opentofu@pve -role PVEVMAdmin
pveum aclmod / -user opentofu@pve -role PVESDNUser
pveum aclmod / -user opentofu@pve -role OpenTofuImageDownload
pveum aclmod /storage/local -user opentofu@pve -role PVEDatastoreAdmin
pveum aclmod /storage/local-lvm -user opentofu@pve -role PVEDatastoreAdmin
```

Check the ACLs:

```bash
pveum acl list
```

![Proxmox ACL list showing the OpenTofu user roles](images/proxmox-acl-list.png)

Create the API token:

```bash
pveum user token add opentofu@pve opentofu --privsep 0
```

Save the returned token secret. With `--privsep 0`, the token uses the permissions of its backing user.

![Proxmox API token creation output](images/proxmox-api-token.png)

Create a separate Linux/PAM account on every Proxmox node on which the provider may perform SSH operations:

```bash
useradd --create-home --shell /bin/bash opentofu
```

Install sudo:

```bash
apt update
apt install sudo
```

On the **deployment machine**, generate the SSH key:

```bash
ssh-keygen -t ed25519 -a 100 -f ~/.ssh/proxmox_opentofu -C "opentofu-proxmox"
```

Then on each Proxmox node:

```bash
install -d -m 700 -o opentofu -g opentofu /home/opentofu/.ssh
```

Put the contents of:

```text
~/.ssh/proxmox_opentofu.pub
```

from the deployment machine into:

```text
/home/opentofu/.ssh/authorized_keys
```

Set the ownership and permissions explicitly:

```bash
chown opentofu:opentofu /home/opentofu/.ssh/authorized_keys
chmod 600 /home/opentofu/.ssh/authorized_keys
```

Add user to sudoers:

```bash
visudo -f /etc/sudoers.d/opentofu
```

Add:

```text
opentofu ALL=(root) NOPASSWD: /usr/sbin/pvesm
opentofu ALL=(root) NOPASSWD: /usr/sbin/qm
```

Set permissions:

```bash
chmod 0440 /etc/sudoers.d/opentofu
visudo --check
```

![Successful visudo validation](images/sudoers-validation.png)

On the deployment machine, start an SSH agent and load the key:

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/proxmox_opentofu
```

![SSH agent with the Proxmox OpenTofu key loaded](images/ssh-agent-key-loaded.png)

Test normal SSH:

```bash
ssh opentofu@192.168.200.53
```

![Successful SSH login to the Proxmox node](images/proxmox-ssh-login.png)

Exit, then test the exact passwordless sudo behavior the provider needs:

```bash
ssh opentofu@192.168.200.53 sudo pvesm apiinfo
```

![Successful passwordless pvesm API test](images/passwordless-pvesm-test.png)

## Run the automation

The infrastructure is split into independently managed root modules with separate state files:

- `image/` downloads and owns the Talos image in Proxmox.
- `vms/` creates Talos VMs and reads the image file ID from `image/terraform.tfstate`.
- `talos/` configures and bootstraps Talos, then produces the Kubernetes credentials.
- `cilium/` installs Cilium with Helm and waits for the complete cluster to become healthy.
- `local-path-provisioner/` installs the default local StorageClass backed by each node's encrypted data disk.
- `cilium-config/` configures the Cilium load-balancer IP pool and L2 announcements.

Destroying one root does not automatically destroy resources owned by another root.

### Clone repo

```bash
git clone https://github.com/johannes-kuhfuss/shost.git
```

### Credentials

The image and VM roots use the provider-native `PROXMOX_VE_API_TOKEN` environment variable. The VM root also uses the local SSH agent with the Linux user configured by `proxmox_ssh_username`.

Edit the setup script and add your Proxmox token, then execute the script:

```bash
source stage02/infra/scripts/setup-opentofu-env.sh
```

### Configuration

Go to the base folder:

```bash
cd stage02/infra/opentofu/
```

Copy the example files using the script:

```bash
../scripts/copy-opentofu-tfvars.sh
```

Edit the files (`terraform.tfvars`) and fill in the correct values.

### Apply

Initialize and apply the roots in dependency order.

Download the image first:

```bash
tofu -chdir=image init
tofu -chdir=image plan
tofu -chdir=image apply
```

Create the VMs:

```bash
tofu -chdir=vms init
tofu -chdir=vms plan
tofu -chdir=vms apply
```

Install Talos:

```bash
tofu -chdir=talos init
tofu -chdir=talos plan
tofu -chdir=talos apply
```

### DHI registry access before Cilium

Cilium needs authenticated image pulls on its first startup. Create the source
`dhi-pull-secret` in the existing `kube-system` namespace immediately after the
Talos apply, before installing Cilium. This avoids depending on networking or
local-path-provisioner to bootstrap registry access. See
[Docker's Kubernetes authentication instructions](https://docs.docker.com/dhi/how-to/use/#use-with-kubernetes).

Run the following Bash commands on the deployment machine from
`stage02/infra/opentofu/`. Secret creation also requires `base64` and a Docker
Hub personal access token with read access (or an organization access token
with access to public repositories, using the organization name as username).

Select the target cluster before creating the Secret:

```bash
../scripts/extract-talos-config.sh
kubectl config current-context
kubectl cluster-info
```

Verify that kubectl points at the cluster managed by this OpenTofu state. The
extraction script preserves existing configuration files; use its `--overwrite`
option if those files need replacing.

Create or update `dhi-pull-secret` in `kube-system`. This uses a temporary,
private Docker configuration so only the DHI credential is uploaded. The token
is entered interactively, encoded into the temporary file, and kept out of Git and
OpenTofu configuration/state. Do not run this block with shell tracing enabled.

```bash
(
  set -euo pipefail
  umask 077
  dhi_config_dir="$(mktemp -d)"
  trap 'rm -rf -- "$dhi_config_dir"' EXIT
  read -r -p 'Docker Hub username (or organization name): ' dhi_username
  read -r -s -p 'Read-only Docker access token: ' dhi_token
  printf '\n'
  dhi_auth="$(printf '%s:%s' "$dhi_username" "$dhi_token" | base64 | tr -d '\r\n')"
  printf '{"auths":{"dhi.io":{"auth":"%s"}}}\n' "$dhi_auth" \
    > "$dhi_config_dir/config.json"
  unset dhi_token
  unset dhi_auth
  kubectl --namespace kube-system create secret generic dhi-pull-secret \
    --type=kubernetes.io/dockerconfigjson \
    --from-file=.dockerconfigjson="$dhi_config_dir/config.json" \
    --dry-run=client -o yaml | kubectl apply -f -
)
```

Repeat this block when rotating the token. The Helm values reference the Secret
through `imagePullSecrets`; workstation authentication alone does not give the
cluster access. Recreate the Secret if the namespace is deleted and reinstalled.

### Install Cilium with DHI images

The upstream Cilium chart remains at `1.20.1`. The values override all images
used by the enabled components, including init containers and certificate jobs:

| Component | DHI image |
| --- | --- |
| Agent and init containers | `dhi.io/cilium:1.20.1-debian13` |
| Generic operator | `dhi.io/cilium-operator-generic:1.20.1-debian13` |
| Envoy | `dhi.io/cilium-envoy:1.20.1-debian13-compat` |
| Hubble Relay | `dhi.io/hubble-relay:1.20.1-debian13` |
| Hubble UI frontend | `dhi.io/hubble-ui:0.13.5-debian13` |
| Hubble UI backend | `dhi.io/hubble-ui-backend:0.13.5-debian13` |
| Certificate generator | `dhi.io/cilium-certgen:0.4.9-debian13` |

Full `image.override` references bypass upstream image digests and automatic
operator suffixes. Envoy uses the compatibility variant for the chart's
`/usr/bin/cilium-envoy-starter` command; its DHI version follows the Cilium
release, not the upstream Envoy image's long build tag. Hubble UI runs as
UID/GID `65532` to match the DHI images' file ownership. Review all image pins
in `cilium/cilium-values.yaml` whenever changing `cilium_version`. Features not
enabled here, such as Cluster Mesh and preflight checks, need their own image
review before enabling them.

For an existing deployment that already has the credential in
`local-path-storage`, either create it in `kube-system` using the block above
or copy it there before applying Cilium. Keep the release name and namespace.

```bash
kubectl -n kube-system get secret dhi-pull-secret
tofu -chdir=cilium init
tofu -chdir=cilium plan
tofu -chdir=cilium apply
cilium status --wait
kubectl -n kube-system rollout status daemonset/cilium --timeout=5m
kubectl -n kube-system rollout status daemonset/cilium-envoy --timeout=5m
kubectl -n kube-system rollout status deployment/cilium-operator --timeout=5m
kubectl -n kube-system rollout status deployment/hubble-relay --timeout=5m
kubectl -n kube-system rollout status deployment/hubble-ui --timeout=5m
```

After completing Stage 02, run the extended checks in the Tests section to
verify networking and load balancing. Check Hubble UI and Gateway TLS after
Stage 03. If image pulls fail, inspect pod events and the namespace-local
Secret before retrying. To revert an existing migration, restore the prior
Cilium values and apply this root again.

### Local Path Provisioner: DHI registry access and installation

The upstream chart remains pinned to `0.0.37`. Its controller uses
`dhi.io/local-path-provisioner:0.0.37`; the separate BusyBox helper remains at
`1.37.0`. Initialize its root:

```bash
tofu -chdir=local-path-provisioner init
```

On a **first installation only**, create the OpenTofu-managed namespace before
copying the Secret. Skip the targeted apply on an existing installation; the
full apply below still deploys the release:

```bash
tofu -chdir=local-path-provisioner apply \
  -target=kubernetes_namespace_v1.local_path_storage
```

Copy the source credential from `kube-system` using the following block before
applying the Local Path Provisioner release.

#### Reuse the DHI credential across namespaces

Kubernetes image-pull Secrets are namespace-scoped. Reuse the same read-only
credential by copying `dhi-pull-secret` from `kube-system` into each
namespace that will run DHI workloads. Pods must reference the copy in their
own namespace; there is no cluster-wide image-pull Secret. See the
[Kubernetes private-registry documentation](https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/).

The following Bash block requires `jq`. Target namespaces must already exist.
For Stage 02 the target is `local-path-storage`. Add `cert-manager` to the array
after Stage 03 creates that namespace, including during subsequent token
rotations. Do not enable shell tracing (`set -x`).

```bash
(
  set -euo pipefail
  dhi_namespaces=(local-path-storage)

  # Check every target before changing any Secrets.
  for namespace in "${dhi_namespaces[@]}"; do
    kubectl get namespace "$namespace" >/dev/null
  done

  for namespace in "${dhi_namespaces[@]}"; do
    kubectl --namespace kube-system get secret dhi-pull-secret -o json |
      jq --arg namespace "$namespace" '{
        apiVersion: "v1",
        kind: "Secret",
        metadata: {
          name: "dhi-pull-secret",
          namespace: $namespace
        },
        type: .type,
        data: .data
      }' |
      kubectl apply -f -
  done

  for namespace in kube-system "${dhi_namespaces[@]}"; do
    kubectl --namespace "$namespace" get secret dhi-pull-secret
  done
)
```

This creates or updates the destination Secrets without copying source object
identity or ownership metadata, writing credentials to disk, or printing their
contents. The final commands confirm that the Secrets exist; they do not test
registry authentication.

Each workload still needs an explicit reference to its namespace's Secret.
Local Path Provisioner's Helm values already contain:

```yaml
imagePullSecrets:
  - name: dhi-pull-secret
```

Use each chart's documented setting when migrating other workloads; the values
key may differ. Copying the Secret alone does not switch images or attach it
to Pods. Cilium and Local Path Provisioner already reference this Secret in
their values files; Stage 03 configures cert-manager and trust-manager too.

For token rotation, rerun the creation block above with the new credential,
then rerun the copy block for every namespace using it. Verify new image pulls
with the updated credential before revoking the old token. The Secrets are
independent copies: updates are not automatically synchronized, and newly
created or recreated namespaces need a copy before deploying DHI workloads.

#### Apply and verify Local Path Provisioner

Review and apply the complete root. On an existing installation, expect an
update to the Helm release, with no storage migration:

```bash
tofu -chdir=local-path-provisioner plan
tofu -chdir=local-path-provisioner apply
kubectl --namespace local-path-storage rollout status \
  deployment/local-path-provisioner --timeout=5m
kubectl --namespace local-path-storage get deployment local-path-provisioner \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
printf '\n'
kubectl --namespace local-path-storage logs deployment/local-path-provisioner \
  --tail=100
```

The Deployment should use `dhi.io/local-path-provisioner:0.0.37`. The existing
non-root UID/GID `65534`, dropped capabilities, and read-only root filesystem
remain configured. Validate actual provisioning with the `--extended` checks
in the Tests section after completing installation; these create a PVC and
consuming pod and check file writes/reads. A PVC alone stays pending because
the StorageClass uses `WaitForFirstConsumer`. Confirm the temporary PVC/PV and
helper pods are cleaned up after the checks.

If the rollout reports `ImagePullBackOff`, inspect the pod events and verify
the Secret's namespace, registry, and token access. The release uses atomic
upgrades. To explicitly revert the image migration, set `image.repository` to
`docker.io/rancher/local-path-provisioner` and `image.tag` to `v0.0.37`, remove
the DHI `imagePullSecrets` entry, and plan/apply this root again.

The provisioner uses the encrypted Talos user volume mounted at
`/var/mnt/local-storage` on every node. The `local-path` StorageClass is the
cluster default and uses `WaitForFirstConsumer`. Requested PVC capacity is not
enforced individually; all local volumes on a node share the space on that
node's data disk.

### Configure Cilium resources

```bash
tofu -chdir=cilium-config init
tofu -chdir=cilium-config plan
tofu -chdir=cilium-config apply
```

### Store talosconfig and kubeconfig

Extract the configurations with the helper script. Existing files are skipped by default:

```bash
../scripts/extract-talos-config.sh
```

To replace existing `~/.talos/config` and `~/.kube/config` files explicitly, pass the overwrite flag:

```bash
../scripts/extract-talos-config.sh --overwrite
```

## Tests

Run the read-only installation checks:

```bash
stage02/infra/scripts/verify-installation.sh
```

Also provision a PVC, write and read test data, and exercise cluster networking
and load balancing:

```bash
stage02/infra/scripts/verify-installation.sh --extended
```

## Clean-up

Destroy in reverse order so Helm can still reach the Kubernetes API while uninstalling Cilium:

```bash
tofu -chdir=cilium-config destroy
tofu -chdir=local-path-provisioner destroy
tofu -chdir=cilium destroy
tofu -chdir=talos destroy
tofu -chdir=vms destroy
tofu -chdir=image destroy
```
