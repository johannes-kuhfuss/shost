# Stage 03 - cert-manager, trust-manager, and HTTPS

This stage installs cert-manager with Helm and exposes the existing Hubble UI
through a shared Cilium Gateway at:

```text
https://hubble.tc.jku.internal
```

TLS terminates at the Gateway. Traffic from the Gateway to the `hubble-ui`
Service remains HTTP. Cilium's existing, independently managed Hubble mTLS is
not changed.

It also installs trust-manager to distribute the private root CA to opted-in
namespaces. The demo service uses that trust bundle with a `BackendTLSPolicy`
for TLS from the Gateway to the backend.

## Sources

- <https://cert-manager.io/docs/>
- <https://cert-manager.io/docs/trust/trust-manager/>
- <https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-57pt1r5.pdf>
- <https://certification.enisa.europa.eu/document/download/a845662b-aee0-484e-9191-890c4cfa7aaa_en?filename=ECCG%20Agreed%20Cryptographic%20Mechanisms%20version%202.pdf>
- <https://smallstep.com/blog/everything-pki/>
- <https://artifacthub.io/packages/helm/cert-manager/cert-manager>

## Design

- `192.168.200.221` is reserved from the Stage 02 Cilium load-balancer pool for
  the shared `internal-web` Gateway.
- An offline ECDSA P-384 root CA signs two constrained ECDSA P-384 intermediate
  CAs: one for Gateway-facing names and one for cluster-internal service names.
  All CA certificates use SHA-384 signatures.
- Only the two intermediate private keys are installed in Kubernetes.
- Two cert-manager `ClusterIssuer` resources use the respective intermediates.
  Gateway leaf certificates have a 90-day lifetime. Workload certificates set
  their own lifetime; the demo service deliberately uses one hour to exercise
  live certificate and private-key rotation.
- cert-manager derives each `Certificate` from an HTTPS listener on the
  annotated Gateway. Every listener uses a distinct Secret and therefore gets
  an independent leaf certificate.
- The shared Gateway and its generated Certificates and TLS Secrets live in the
  `observability` namespace. Hubble's HTTPRoute currently lives there as well.
- Only HTTPRoutes from namespaces labelled
  `internal-web-gateway-access: "true"` may attach to the Gateway.
- Hubble remains owned by the Cilium Helm release in `kube-system`.
- A narrowly scoped `ReferenceGrant` permits the HTTPRoute to reference only
  the `hubble-ui` Service across the namespace boundary.
- Trust is installed manually on administrator devices. trust-manager publishes
  the private root as a ConfigMap only in explicitly labelled namespaces for
  cluster workloads and Gateway backend validation.

## Prerequisites

Complete Stage 02 and verify that the following resources are healthy:

```bash
kubectl get nodes
kubectl get gatewayclass cilium
kubectl -n kube-system get service hubble-ui
kubectl get ciliuml2announcementpolicy
kubectl get ciliumloadbalancerippool
```

For a fresh installation, confirm that the reserved Gateway address is not
already assigned:

```bash
kubectl get services --all-namespaces --output=wide | grep 192.168.200.221
```

This command should produce no output. If the original dedicated Hubble Gateway
is already installed, it will legitimately show the old Gateway Service; use
the migration procedure in step 6 instead.

The deployment machine needs `kubectl`, Helm, and OpenSSL. The examples below
assume a Linux shell, as used by Stage 02. Run the CA commands on the offline
machine unless a step explicitly says to use the deployment machine.

cert-manager `v1.21.1` is pinned because the Stage 02 example targets Kubernetes
`v1.36.3`, which is within cert-manager 1.21's supported Kubernetes range.
trust-manager `v0.24.0` is pinned and supports Kubernetes `v1.25.0` and newer.

## 1. Create the offline root CA

Copy `pki/root-ca.cnf`, `pki/intermediate-ca-int.cnf`, and
`pki/intermediate-ca-ext.cnf` into an encrypted or otherwise protected
`tc-jku-pki` directory on the offline machine. Do not generate or retain
private keys inside the Git checkout.

On the offline machine, create an encrypted ECDSA P-384 root key and a 20-year
root certificate. SHA-384 is used as the certificate signature digest:

```bash
umask 077
mkdir -p tc-jku-pki
cd tc-jku-pki

# Generate private key, use elliptic curves
openssl genpkey \
  -algorithm EC \
  -pkeyopt ec_paramgen_curve:P-384 \
  -pkeyopt ec_param_enc:named_curve \
  -aes-256-cbc \
  -out root-ca.key

# Generate root CA request and sign it
openssl req \
  -new \
  -x509 \
  -sha384 \
  -days 7300 \
  -key root-ca.key \
  -config root-ca.cnf \
  -extensions root_ca \
  -out root-ca.crt
```

Record the root fingerprint through a channel independent of the certificate
file. It is used to verify copies before installing trust:

```bash
openssl x509 -in root-ca.crt -noout -subject -dates -fingerprint -sha384
openssl x509 -in root-ca.crt -noout -text \
  | grep -E 'Signature Algorithm|Public Key Algorithm|ASN1 OID'
```

The inspection output should identify `id-ecPublicKey`, the `secp384r1` curve
(OpenSSL's name for P-384), and `ecdsa-with-SHA384`.

Back up `root-ca.key`, its passphrase, `root-ca.crt`, and the fingerprint. Keep
the key offline.

## 2a. Create and sign the intermediate CA used for Gateway-facing names

Two intermediate CAs reduce the blast radius if either online signing key is
compromised.
Still on the offline machine, create an encrypted ECDSA P-384 intermediate key
and CSR:

```bash
# Generate private key, use elliptic curves
openssl genpkey \
  -algorithm EC \
  -pkeyopt ec_paramgen_curve:P-384 \
  -pkeyopt ec_param_enc:named_curve \
  -aes-256-cbc \
  -out intermediate-ca-ext.key

# Generate intermediate CA request
openssl req \
  -new \
  -sha384 \
  -key intermediate-ca-ext.key \
  -config intermediate-ca-ext.cnf \
  -out intermediate-ca-ext.csr
```

Sign it for ten years using SHA-384. The supplied extension restricts it to DNS
names below `tc.jku.internal` and prevents it from creating further intermediate
CAs:

```bash
openssl x509 \
  -req \
  -sha384 \
  -days 3650 \
  -in intermediate-ca-ext.csr \
  -CA root-ca.crt \
  -CAkey root-ca.key \
  -CAcreateserial \
  -extfile intermediate-ca-ext.cnf \
  -extensions intermediate_ca \
  -out intermediate-ca-ext.crt

openssl verify -CAfile root-ca.crt intermediate-ca-ext.crt
openssl x509 -in intermediate-ca-ext.crt -noout -subject -issuer -text \
  | grep -A4 -E \
    'Basic Constraints|Key Usage|Name Constraints|Signature Algorithm|Public Key Algorithm|ASN1 OID'
```

The intermediate inspection output should show the same P-384 public-key and
SHA-384 signature algorithms as the root.

Keep the encrypted intermediate key as the recovery copy. cert-manager needs an
unencrypted deployment copy, so create one temporarily:

```bash
openssl pkey \
  -in intermediate-ca-ext.key \
  -out intermediate-ca-ext-deploy.key

# Create chain including intermediate and root CA public keys
cat intermediate-ca-ext.crt root-ca.crt > intermediate-chain-ext.crt
```

Securely transfer only these files to the deployment machine:

- `intermediate-ca-ext-deploy.key`
- `intermediate-chain-ext.crt`
- `root-ca.crt`

Verify the root fingerprint after transfer. Never transfer `root-ca.key`.

## 2b. Create and sign the intermediate CA used for cluster-internal requests

Still on the offline machine, create an encrypted ECDSA P-384 intermediate key
and CSR:

```bash
# Generate private key, use elliptic curves
openssl genpkey \
  -algorithm EC \
  -pkeyopt ec_paramgen_curve:P-384 \
  -pkeyopt ec_param_enc:named_curve \
  -aes-256-cbc \
  -out intermediate-ca-int.key

# Generate intermediate CA request
openssl req \
  -new \
  -sha384 \
  -key intermediate-ca-int.key \
  -config intermediate-ca-int.cnf \
  -out intermediate-ca-int.csr
```

Sign it for ten years using SHA-384. The supplied extension restricts it to DNS
names below `svc.cluster.local` and prevents it from creating further intermediate
CAs:

```bash
openssl x509 \
  -req \
  -sha384 \
  -days 3650 \
  -in intermediate-ca-int.csr \
  -CA root-ca.crt \
  -CAkey root-ca.key \
  -CAcreateserial \
  -extfile intermediate-ca-int.cnf \
  -extensions intermediate_ca \
  -out intermediate-ca-int.crt

openssl verify -CAfile root-ca.crt intermediate-ca-int.crt
openssl x509 -in intermediate-ca-int.crt -noout -subject -issuer -text \
  | grep -A4 -E \
    'Basic Constraints|Key Usage|Name Constraints|Signature Algorithm|Public Key Algorithm|ASN1 OID'
```

The intermediate inspection output should show the same P-384 public-key and
SHA-384 signature algorithms as the root.

Keep the encrypted intermediate key as the recovery copy. cert-manager needs an
unencrypted deployment copy, so create one temporarily:

```bash
openssl pkey \
  -in intermediate-ca-int.key \
  -out intermediate-ca-int-deploy.key

# Create chain including intermediate and root CA public keys
cat intermediate-ca-int.crt root-ca.crt > intermediate-chain-int.crt
```

Securely transfer only these files to the deployment machine:

- `intermediate-ca-int-deploy.key`
- `intermediate-chain-int.crt`
- `root-ca.crt`

Verify the root fingerprint after transfer. Never transfer `root-ca.key`.

On the deployment machine, place the transferred root certificate, intermediate
chains, and temporary unencrypted deployment keys in
`stage03/cert-manager/pki/`. The directory's `.gitignore` excludes the chains
and keys, but verify `git status` before every commit and remove the unencrypted
keys immediately after creating the Kubernetes Secrets. The public root
certificate remains on the deployment machine for trust distribution and
administrator-device enrollment.

### Verify the name-constraint boundary

Before deploying the intermediates, use each one to sign representative test
leaf certificates and verify the complete paths with OpenSSL. Test both an
allowed and a deliberately forbidden DNS SAN for each issuer:

| Signing intermediate | Test DNS SAN | Expected result |
| --- | --- | --- |
| Gateway-facing | `hubble.tc.jku.internal` | verification succeeds |
| Gateway-facing | `app.example.svc.cluster.local` | permitted-subtree violation |
| Cluster-internal | `app.example.svc.cluster.local` | verification succeeds |
| Cluster-internal | `hubble.tc.jku.internal` | permitted-subtree violation |

Create disposable test leaves with exact DNS SANs:

```bash
issue_test_leaf() {
  leaf_name="$1"
  dns_name="$2"
  ca_cert="$3"
  ca_key="$4"

  openssl genpkey \
    -algorithm EC \
    -pkeyopt ec_paramgen_curve:P-256 \
    -out "${leaf_name}.key"

  openssl req \
    -new \
    -sha256 \
    -key "${leaf_name}.key" \
    -subj "/CN=${dns_name}" \
    -out "${leaf_name}.csr"

  printf '%s\n' \
    '[leaf]' \
    'basicConstraints = critical, CA:false' \
    'keyUsage = critical, digitalSignature' \
    'extendedKeyUsage = serverAuth' \
    "subjectAltName = DNS:${dns_name}" \
    > "${leaf_name}.cnf"

  openssl x509 \
    -req \
    -sha256 \
    -days 1 \
    -in "${leaf_name}.csr" \
    -CA "${ca_cert}" \
    -CAkey "${ca_key}" \
    -CAcreateserial \
    -extfile "${leaf_name}.cnf" \
    -extensions leaf \
    -out "${leaf_name}.crt"
}

issue_test_leaf gateway-allowed-leaf hubble.tc.jku.internal \
  intermediate-ca-ext.crt intermediate-ca-ext.key
issue_test_leaf gateway-forbidden-leaf app.example.svc.cluster.local \
  intermediate-ca-ext.crt intermediate-ca-ext.key
issue_test_leaf cluster-allowed-leaf app.example.svc.cluster.local \
  intermediate-ca-int.crt intermediate-ca-int.key
issue_test_leaf cluster-forbidden-leaf hubble.tc.jku.internal \
  intermediate-ca-int.crt intermediate-ca-int.key
```

Verify each leaf using the root as the trust anchor and the applicable
intermediate as the untrusted chain:

```bash
openssl verify \
  -CAfile root-ca.crt \
  -untrusted intermediate-ca-ext.crt \
  gateway-allowed-leaf.crt

openssl verify \
  -CAfile root-ca.crt \
  -untrusted intermediate-ca-int.crt \
  cluster-allowed-leaf.crt

if openssl verify \
  -CAfile root-ca.crt \
  -untrusted intermediate-ca-ext.crt \
  gateway-forbidden-leaf.crt; then
  echo 'ERROR: Gateway-facing CA accepted a cluster-internal name' >&2
  exit 1
fi

if openssl verify \
  -CAfile root-ca.crt \
  -untrusted intermediate-ca-int.crt \
  cluster-forbidden-leaf.crt; then
  echo 'ERROR: Cluster-internal CA accepted a Gateway-facing name' >&2
  exit 1
fi
```

The two forbidden-leaf commands must report a permitted-subtree violation. A
successful verification of an intermediate alone proves its signature chain,
but does not exercise its name constraints. cert-manager's built-in CA issuer
does not validate requested SANs against issuer name constraints, so a
violating request can be issued and only fail when a TLS client verifies it.

Remove all `*-leaf.*` test artifacts after completing the checks.

## 3. Install cert-manager

```bash
cd stage03
```

From this directory, install the pinned chart and wait for it to become ready:

```bash
helm upgrade --install cert-manager \
  oci://quay.io/jetstack/charts/cert-manager \
  --version v1.21.1 \
  --namespace cert-manager \
  --create-namespace \
  --values cert-manager/values.yaml \
  --wait \
  --timeout 10m

kubectl -n cert-manager rollout status deployment/cert-manager --timeout=5m
kubectl -n cert-manager rollout status deployment/cert-manager-webhook --timeout=5m
kubectl -n cert-manager rollout status deployment/cert-manager-cainjector --timeout=5m
```

The chart owns the cert-manager CRDs. Gateway integration is enabled so
cert-manager watches annotated Gateway listeners and creates their Certificate
resources. Stage 02 installs the Gateway API CRDs before cert-manager starts.

## 4. Install the intermediate signing keys

Create both signing Secrets in cert-manager's default cluster-resource
namespace:

```bash
kubectl -n cert-manager create secret tls tc-jku-web-intermediate-ca \
  --cert=cert-manager/pki/intermediate-chain-ext.crt \
  --key=cert-manager/pki/intermediate-ca-ext-deploy.key

kubectl -n cert-manager create secret tls tc-jku-cluster-intermediate-ca \
  --cert=cert-manager/pki/intermediate-chain-int.crt \
  --key=cert-manager/pki/intermediate-ca-int-deploy.key
```

Confirm that both Secrets exist without printing their contents:

```bash
kubectl -n cert-manager get secret \
  tc-jku-web-intermediate-ca \
  tc-jku-cluster-intermediate-ca
```

Remove both temporary unencrypted deployment keys from the deployment machine
after the Secrets are created. Retain the encrypted offline recovery copies.

Create a separately managed ConfigMap containing only the public root
certificate. This is trust-manager's source of truth; do not use either signing
Secret as a trust source:

```bash
kubectl -n cert-manager create configmap tc-jku-internal-root-ca \
  --from-file=ca.crt=cert-manager/pki/root-ca.crt \
  --dry-run=client \
  --output=yaml \
  | kubectl apply -f -

kubectl -n cert-manager get configmap tc-jku-internal-root-ca
```

The root certificate is public material, but its integrity is security
critical. Verify its recorded SHA-384 fingerprint before publishing it.

## 5. Install trust-manager and publish the root bundle

Install the pinned chart in the existing `cert-manager` namespace. The values
disable the unused public CA package and Secret targets; trust-manager only
needs to read the root source ConfigMap and write target ConfigMaps:

```bash
helm upgrade --install trust-manager \
  oci://quay.io/jetstack/charts/trust-manager \
  --version v0.24.0 \
  --namespace cert-manager \
  --values trust-manager/values.yaml \
  --wait \
  --timeout 5m

kubectl -n cert-manager rollout status deployment/trust-manager --timeout=5m
```

Create the cluster-scoped Bundle:

```bash
kubectl apply -f trust-manager/manifests/internal-ca-bundle.yaml
kubectl get bundle tc-jku-internal-ca
```

The Bundle must report `SYNCED=True`. It creates a ConfigMap named
`tc-jku-internal-ca`, containing `ca.crt`, in every namespace labelled
`trust.tc.jku.internal/internal-ca=true`. The selector is deliberately opt-in.
Do not create or edit those target ConfigMaps manually; trust-manager owns
them.

## 6. Create the issuers and shared Gateway

Apply the namespace and issuer first:

```bash
kubectl apply -f cert-manager/manifests/namespace.yaml
kubectl apply -f cert-manager/manifests/cluster-issuer.yaml
kubectl wait clusterissuer/tc-jku-web-ca \
  --for=condition=Ready \
  --timeout=2m
kubectl wait clusterissuer/tc-jku-cluster-ca \
  --for=condition=Ready \
  --timeout=2m
```

The critical X.509 name constraints limit which DNS names produce valid
certificate paths, but they are not an authorization boundary between
namespaces. They also do not prohibit other SAN types. Before allowing
untrusted tenants to create `Certificate` resources for these ClusterIssuers,
install an approval controller such as cert-manager approver-policy and enforce
all of the following:

- Gateway requests may contain only DNS SANs below `tc.jku.internal`.
- Workload requests may contain only DNS SANs of the form
  `<service>.<request-namespace>.svc.cluster.local`.
- IP, URI, email, and other SAN types are denied unless explicitly required.
- CA certificates cannot be requested.

Keep cert-manager's built-in auto-approval enabled until such policies and
their RBAC bindings are installed and tested. Disabling auto-approval without a
working replacement stops certificate issuance.

Apply the shared Gateway and Hubble resources
in dependency order:

```bash
kubectl apply -f cert-manager/manifests/hubble/reference-grant.yaml
kubectl apply -f cert-manager/manifests/gateway.yaml
kubectl wait -n observability certificate/hubble-tls \
  --for=create \
  --timeout=2m
kubectl wait -n observability certificate/hubble-tls \
  --for=condition=Ready \
  --timeout=2m
kubectl wait -n observability certificate/demo-service-tls \
  --for=create \
  --timeout=2m
kubectl wait -n observability certificate/demo-service-tls \
  --for=condition=Ready \
  --timeout=2m
kubectl apply -f cert-manager/manifests/hubble/http-route.yaml
```

The Gateway can exist briefly with `ResolvedRefs=False` while cert-manager
creates its TLS Secrets. It should reconcile automatically when they become
available.

## 7. Configure DNS

Create host (A) entries with these values:

```text
Host:   hubble
Domain: tc.jku.internal
Type:   A
IP:     192.168.200.221

Host:   demo
Domain: tc.jku.internal
Type:   A
IP:     192.168.200.221
```

Apply the configuration and verify it from an administrator device:

```bash
nslookup hubble.tc.jku.internal
nslookup demo.tc.jku.internal
```

Both answers must be `192.168.200.221`.

## 8. Install the root CA on administrator devices

Verify the SHA-384 fingerprint before trusting `root-ca.crt`.

On Windows, from an elevated terminal:

```powershell
certutil -addstore -f Root root-ca.crt
```

On Debian or Ubuntu:

```bash
sudo install -m 0644 root-ca.crt \
  /usr/local/share/ca-certificates/tc-jku-internal-root-ca.crt
sudo update-ca-certificates
```

On macOS:

```bash
sudo security add-trusted-cert \
  -d \
  -r trustRoot \
  -k /Library/Keychains/System.keychain \
  root-ca.crt
```

Some browsers, particularly Firefox depending on its configuration, may use a
separate certificate store. Import `root-ca.crt` as a trusted certificate
authority there if the operating-system trust store is not used.

## 9. Verify Hubble HTTPS

Check that Cilium assigned the requested address and accepted all references:

```bash
kubectl -n observability get certificate,secret,gateway,httproute
kubectl -n observability describe gateway internal-web
kubectl -n observability describe httproute hubble
kubectl get service -A | grep cilium-gateway-internal-web
```

The Gateway must report address `192.168.200.221` and conditions `Accepted=True`,
`Programmed=True`, and `ResolvedRefs=True`. The HTTPRoute must report
`Accepted=True` and `ResolvedRefs=True`.

Test first with the root file, which does not depend on system trust:

```bash
curl --cacert /secure/path/root-ca.crt \
  --resolve hubble.tc.jku.internal:443:192.168.200.221 \
  https://hubble.tc.jku.internal/
```

After installing the root into the device trust store:

```bash
curl https://hubble.tc.jku.internal/
```

Open <https://hubble.tc.jku.internal> in a browser and inspect the certificate.
Its DNS SAN must be `hubble.tc.jku.internal`, and its chain must lead to the
verified TC JKU Internal Root CA.

## 10. Deploy and verify the demo service with backend TLS

The demo namespace in `demo-service/manifests/app.yaml` opts into both the
shared Gateway and the trust bundle. Apply the workload resources in dependency
order:

```bash
# Creates the Namespace, Deployment, and Service. The Pod may wait for its
# certificate Secret until the following Certificate becomes Ready.
kubectl apply -f demo-service/manifests/app.yaml
kubectl apply -f demo-service/manifests/certificate.yaml
kubectl wait -n demo-service certificate/demo-service-backend \
  --for=condition=Ready \
  --timeout=2m

# trust-manager creates this after observing the namespace's opt-in label.
kubectl wait -n demo-service configmap/tc-jku-internal-ca \
  --for=create \
  --timeout=2m

kubectl apply -f demo-service/manifests/backend-tls-policy.yaml
kubectl apply -f demo-service/manifests/http-route.yaml
kubectl -n demo-service rollout status deployment/demo-service --timeout=5m
```

Check that the Certificate, trust bundle, backend TLS policy, and Route were
accepted:

```bash
kubectl -n demo-service get \
  certificate,secret,configmap,backendtlspolicy,httproute
kubectl -n demo-service get configmap tc-jku-internal-ca \
  --output="jsonpath={.data['ca\.crt']}" \
  | openssl x509 -noout -subject -fingerprint -sha384
kubectl -n demo-service describe backendtlspolicy demo-service
kubectl -n demo-service describe httproute demo-service
```

The `BackendTLSPolicy` must report `Accepted=True`. The HTTPRoute must report
`Accepted=True` and `ResolvedRefs=True`. The distributed root fingerprint must
match the independently recorded value. Test the complete client-to-Gateway
and Gateway-to-backend path:

```bash
curl --cacert cert-manager/pki/root-ca.crt \
  --resolve demo.tc.jku.internal:443:192.168.200.221 \
  https://demo.tc.jku.internal/certificate
```

The response describes the cluster-internal certificate served by the demo
Pod, not the separate Gateway certificate presented to curl.

## Add another internal UI

Each additional UI reuses `192.168.200.221` but receives its own hostname and
certificate:

1. Add an DNS record, such as `grafana.tc.jku.internal`, pointing to
   `192.168.200.221`.
2. Label the namespace containing the application's HTTPRoute:

   ```bash
   kubectl label namespace grafana internal-web-gateway-access=true
   ```

3. Add an HTTPS listener to `cert-manager/manifests/gateway.yaml` with a unique
   listener name, hostname, and Secret name. For example:

   ```yaml
   - name: grafana
     hostname: grafana.tc.jku.internal
     port: 443
     protocol: HTTPS
     allowedRoutes:
       namespaces:
         from: Selector
         selector:
           matchLabels:
             internal-web-gateway-access: "true"
     tls:
       mode: Terminate
       certificateRefs:
         - group: ""
           kind: Secret
           name: grafana-tls
   ```

4. Apply the Gateway. cert-manager creates `Certificate/grafana-tls` and
   `Secret/grafana-tls` in `observability` automatically:

   ```bash
   kubectl apply -f cert-manager/manifests/gateway.yaml
   kubectl wait -n observability certificate/grafana-tls \
     --for=create \
     --timeout=2m
   kubectl wait -n observability certificate/grafana-tls \
     --for=condition=Ready \
     --timeout=2m
   ```

5. Put the application's HTTPRoute in its application namespace. Its
   `parentRefs` must identify the shared Gateway and listener:

   ```yaml
   parentRefs:
     - group: gateway.networking.k8s.io
       kind: Gateway
       name: internal-web
       namespace: observability
       sectionName: grafana
   ```

Keep the HTTPRoute and backend Service in the same namespace where possible.
That avoids a cross-namespace backend reference and the corresponding
`ReferenceGrant`. Hubble retains its grant because its Service is in
`kube-system`.

If the Gateway should use TLS to reach the new backend, also label its namespace
with `trust.tc.jku.internal/internal-ca=true`, issue a cluster-internal
certificate from `tc-jku-cluster-ca`, and attach a `BackendTLSPolicy` to the
HTTPS Service port.

## Renewal and CA rotation

cert-manager renews the 90-day Hubble certificate approximately 30 days before
expiry and rotates its private key. Check it with:

```bash
kubectl -n observability get certificate hubble-tls
kubectl -n observability describe certificate hubble-tls
```

The CA issuers do not rotate the ten-year intermediates automatically. Rotate
the Gateway-facing and cluster-internal intermediates independently before
either expires:

1. Create a replacement intermediate with the same name constraints, signed by
   the offline root.
2. Replace only that issuer's signing Secret with the new full chain and private
   key.
3. Wait for the corresponding `ClusterIssuer` to report `Ready=True`.
4. Trigger renewal of every leaf issued by that ClusterIssuer. For the
   Gateway-facing issuer, this includes
   `cmctl renew -n observability hubble-tls`. For the cluster-internal issuer,
   renew each affected workload Certificate in its application namespace.
5. Verify the newly served chains before retiring the old intermediate.

Updating an issuer Secret does not automatically reissue its existing leaf
certificates. Maintain an inventory of Certificates by `issuerRef` so none are
missed during an intermediate rotation.

Root rotation requires an overlap period during which administrator devices
and cluster workloads trust both old and new roots. Before issuing certificates
under a new root, build a PEM file containing both roots and update the source
ConfigMap with the same server-side-safe command used during installation:

```bash
kubectl -n cert-manager create configmap tc-jku-internal-root-ca \
  --from-file=ca.crt=/secure/path/overlapping-root-bundle.crt \
  --dry-run=client \
  --output=yaml \
  | kubectl apply -f -

kubectl get bundle tc-jku-internal-ca
kubectl -n demo-service get configmap tc-jku-internal-ca
```

Wait for trust-manager to publish the overlapping bundle everywhere before
deploying intermediates signed by the new root. Remove the old root from the
source only after every client has received the new trust anchor and every old
leaf and intermediate has been retired. Root rotation should be planned well
before the 20-year root expires.

## Removal

Remove only the Stage 03 resources, leaving Cilium and Hubble themselves intact:

```bash
kubectl delete -f demo-service/manifests/http-route.yaml --ignore-not-found
kubectl delete -f demo-service/manifests/backend-tls-policy.yaml --ignore-not-found
kubectl delete -f demo-service/manifests/certificate.yaml --ignore-not-found
kubectl delete -f demo-service/manifests/app.yaml --ignore-not-found

kubectl delete -f cert-manager/manifests/hubble/http-route.yaml
kubectl delete -f cert-manager/manifests/hubble/reference-grant.yaml
kubectl delete -f cert-manager/manifests/gateway.yaml
kubectl -n observability delete certificate hubble-tls --ignore-not-found
kubectl -n observability delete secret hubble-tls --ignore-not-found
kubectl -n observability delete certificate demo-service-tls --ignore-not-found
kubectl -n observability delete secret demo-service-tls --ignore-not-found

kubectl delete -f trust-manager/manifests/internal-ca-bundle.yaml
kubectl -n cert-manager delete configmap tc-jku-internal-root-ca
helm uninstall trust-manager --namespace cert-manager

kubectl delete -f cert-manager/manifests/cluster-issuer.yaml
kubectl -n cert-manager delete secret tc-jku-web-intermediate-ca
kubectl -n cert-manager delete secret tc-jku-cluster-intermediate-ca
kubectl delete -f cert-manager/manifests/namespace.yaml
helm uninstall cert-manager --namespace cert-manager
```

The cert-manager and trust-manager charts retain their installed CRDs. Keep them
if either component will be reinstalled. Removing the CRDs also removes all
corresponding custom resources and is intentionally outside this cleanup
procedure.
