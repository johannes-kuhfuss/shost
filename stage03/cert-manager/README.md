# Stage 03 - cert-manager and Hubble HTTPS

This stage installs cert-manager with Helm and exposes the existing Hubble UI
through a shared Cilium Gateway at:

```text
https://hubble.tc.jku.internal
```

TLS terminates at the Gateway. Traffic from the Gateway to the `hubble-ui`
Service remains HTTP. Cilium's existing, independently managed Hubble mTLS is
not changed.

## Sources

- <https://cert-manager.io/docs/>
- <https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-57pt1r5.pdf>
- <https://certification.enisa.europa.eu/document/download/a845662b-aee0-484e-9191-890c4cfa7aaa_en?filename=ECCG%20Agreed%20Cryptographic%20Mechanisms%20version%202.pdf>
- <https://smallstep.com/blog/everything-pki/>
- <https://artifacthub.io/packages/helm/cert-manager/cert-manager>

## Design

- `192.168.200.221` is reserved from the Stage 02 Cilium load-balancer pool for
  the shared `internal-web` Gateway.
- An offline ECDSA P-384 root CA signs a constrained ECDSA P-384 intermediate
  CA; both CA certificates use SHA-384 signatures.
- Only the intermediate private key is installed in Kubernetes.
- A cert-manager `ClusterIssuer` uses the intermediate to issue and rotate
  90-day leaf certificates.
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
- Trust is installed manually on administrator devices. trust-manager is not
  needed until cluster workloads need the private CA.

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
the migration procedure in step 5 instead.

The deployment machine needs `kubectl`, Helm, and OpenSSL. The examples below
assume a Linux shell, as used by Stage 02. Run the CA commands on the offline
machine unless a step explicitly says to use the deployment machine.

cert-manager `v1.21.1` is pinned because the Stage 02 example targets Kubernetes
`v1.36.3`, which is within cert-manager 1.21's supported Kubernetes range.

## 1. Create the offline root CA

Copy `pki/root-ca.cnf` and `pki/intermediate-ca.cnf` to an encrypted or otherwise
protected offline machine. Do not generate or retain private keys inside the
Git checkout.

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

## 2. Create and sign the intermediate CA

Still on the offline machine, create an encrypted ECDSA P-384 intermediate key
and CSR:

```bash
# Generate private key, use elliptic curves
openssl genpkey \
  -algorithm EC \
  -pkeyopt ec_paramgen_curve:P-384 \
  -pkeyopt ec_param_enc:named_curve \
  -aes-256-cbc \
  -out intermediate-ca.key

# Generate intermediate CA request
openssl req \
  -new \
  -sha384 \
  -key intermediate-ca.key \
  -config intermediate-ca.cnf \
  -out intermediate-ca.csr
```

Sign it for ten years using SHA-384. The supplied extension restricts it to DNS
names below `tc.jku.internal` and prevents it from creating further intermediate
CAs:

```bash
openssl x509 \
  -req \
  -sha384 \
  -days 3650 \
  -in intermediate-ca.csr \
  -CA root-ca.crt \
  -CAkey root-ca.key \
  -CAcreateserial \
  -extfile intermediate-ca.cnf \
  -extensions intermediate_ca \
  -out intermediate-ca.crt

openssl verify -CAfile root-ca.crt intermediate-ca.crt
openssl x509 -in intermediate-ca.crt -noout -text \
  | grep -E 'Signature Algorithm|Public Key Algorithm|ASN1 OID'
```

The intermediate inspection output should show the same P-384 public-key and
SHA-384 signature algorithms as the root.

Keep the encrypted intermediate key as the recovery copy. cert-manager needs an
unencrypted deployment copy, so create one temporarily:

```bash
openssl pkey \
  -in intermediate-ca.key \
  -out intermediate-ca-deploy.key

# Create chain including intermediate and root CA public keys
cat intermediate-ca.crt root-ca.crt > intermediate-chain.crt
```

Securely transfer only these files to the deployment machine:

- `intermediate-ca-deploy.key`
- `intermediate-chain.crt`
- `root-ca.crt`

Verify the root fingerprint after transfer. Never transfer `root-ca.key`.

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

## 4. Install the intermediate signing key

Create the signing Secret in cert-manager's default cluster-resource namespace:

```bash
kubectl -n cert-manager create secret tls tc-jku-internal-intermediate-ca \
  --cert=cert-manager/pki/intermediate-chain.crt \
  --key=cert-manager/pki/intermediate-ca-deploy.key
```

Confirm that the Secret exists without printing its contents:

```bash
kubectl -n cert-manager get secret tc-jku-internal-intermediate-ca
```

Remove the temporary unencrypted key from the deployment machine after the
Secret is created. Retain the encrypted offline recovery copy.

## 5. Create the issuer and shared Gateway

Apply the namespace and issuer first:

```bash
kubectl apply -f cert-manager/manifests/namespace.yaml
kubectl apply -f cert-manager/manifests/cluster-issuer.yaml
kubectl wait clusterissuer/tc-jku-internal-ca \
  --for=condition=Ready \
  --timeout=2m
```

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
kubectl apply -f cert-manager/manifests/hubble/http-route.yaml
```

The Gateway can exist briefly with `ResolvedRefs=False` while cert-manager
creates `hubble-tls`. It should reconcile automatically when the Secret becomes
available.

## 6. Configure DNS

Create host (A) entry  with these values:

```text
Host:   hubble
Domain: tc.jku.internal
Type:   A
IP:     192.168.200.221
```

Apply the configuration and verify it from an administrator device:

```bash
nslookup hubble.tc.jku.internal
```

The answer must be `192.168.200.221`.

## 7. Install the root CA on administrator devices

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

## 8. Verify Hubble HTTPS

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

## Add another internal UI

Each additional UI reuses `192.168.200.221` but receives its own hostname and
certificate:

1. Add an DNS record, such as `grafana.tc.jku.internal`, pointing to
   `192.168.200.221`.
2. Label the namespace containing the application's HTTPRoute:

   ```bash
   kubectl label namespace grafana internal-web-gateway-access=true
   ```

3. Add an HTTPS listener to `manifests/gateway.yaml` with a unique listener
   name, hostname, and Secret name. For example:

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
   kubectl apply -f manifests/gateway.yaml
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

## Renewal and CA rotation

cert-manager renews the 90-day Hubble certificate approximately 30 days before
expiry and rotates its private key. Check it with:

```bash
kubectl -n observability get certificate hubble-tls
kubectl -n observability describe certificate hubble-tls
```

The CA issuer does not rotate the ten-year intermediate automatically. Before
it expires:

1. Create a replacement intermediate signed by the same offline root.
2. Replace the signing Secret with its new full chain and private key.
3. Wait for the `ClusterIssuer` to report `Ready=True`.
4. Trigger leaf renewal with `cmctl renew -n observability hubble-tls`.
5. Verify the served chain before retiring the old intermediate.

Root rotation requires an overlap period during which administrator devices
trust both old and new roots. It should be planned separately well before the
20-year root expires.

## Removal

Remove only the Stage 03 resources, leaving Cilium and Hubble themselves intact:

```bash
kubectl delete -f manifests/hubble/http-route.yaml
kubectl delete -f manifests/hubble/reference-grant.yaml
kubectl delete -f manifests/gateway.yaml
kubectl -n observability delete certificate hubble-tls --ignore-not-found
kubectl -n observability delete secret hubble-tls --ignore-not-found
kubectl delete -f manifests/cluster-issuer.yaml
kubectl -n cert-manager delete secret tc-jku-internal-intermediate-ca
kubectl delete -f manifests/namespace.yaml
helm uninstall cert-manager --namespace cert-manager
```

Helm does not normally remove installed CRDs. Keep them if cert-manager will be
reinstalled. Removing the CRDs also removes all corresponding custom resources
and is intentionally outside this cleanup procedure.
