# Bartlized Forgejo

Native Forgejo with SQLite/filesystem state, HTTPS/SSH access, stopped capture and Restic publication. One application controller coordinates native work; the administrator selects target, release and promotion.

New deployment: follow the recipe below. Existing deployment: [resume access](#resume-an-existing-deployment), then choose an operation. Use workstation Bash, keep tracing off and stop at a failed command. Creation is not a replay or repair program.

## Supported profile and release

Published **0.2.2** supports Linux amd64, Kubernetes 1.30+, one application replica and native Forgejo 16.0.7 or 17.0.1. The supported forward upgrade is **replacement from 16.0.7 to 17.0.1**. Native preparation owns initialization/reconstruction/migration; ordinary restart serves retained state without backup access. Failed/ambiguous mutation stays stopped; retry uses another independent empty target. Actions, mail, mirrors, federation and external webhooks are disabled. This planned-interruption profile does not implement provider-loss fencing.

Supply the [operator tools](https://github.com/bartlapp/bartl-app/blob/main/docs/operator-workstation.md#check-the-workstation), an existing version-1 installation record with complete cluster/resource references and retained access, enforced NetworkPolicy, Traefik, cert-manager and state/staging/reconstruction capacity. [Base](https://github.com/bartlapp/base-k3s/blob/main/README.md) supplies a single-node profile; an equivalent cluster suffices. Compatible external OIDC/Restic services work without another Bartl package or company tooling.

Select the existing installation directory. Acquire the archive or restore its independently retained bytes to the same path:

```bash
set -euo pipefail
umask 077
export WORK_DIR="$HOME/bartl/acme/acme-installation"
export VALUES="$WORK_DIR/installation.yaml"
mkdir -p "$WORK_DIR/packages"
helm pull oci://ghcr.io/bartlapp/bartl-forgejo --version 0.2.2 --destination "$WORK_DIR/packages"
CHART="$WORK_DIR/packages/bartl-forgejo-0.2.2.tgz"
CHART_SHA256=16405a6d485138d16ebe4afca0c7a3d9a68705194a39ed430311ea16d9bf0664
printf '%s  %s\n' "$CHART_SHA256" "$CHART" | shasum -a 256 --check -
```

The chart retains the 0.1.0 runtime image. [0.2.2 release evidence](https://github.com/bartlapp/bartl-forgejo/releases/tag/v0.2.2) identifies exact artifacts and exercised behavior; this rewritten sequence is not renewed live qualification. The source-only backup-declaration eligibility correction is unreleased and requires a new chart version. These commands use explicit backup selection and do not depend on it. Old flat values are not accepted by 0.2.2.

Retain chart/image, configuration, native identity, repository history/keys and routing/SSH host-key records independently of the covered failure domain. Agree interruption, final-write boundary, source retirement, protection/retention, monitoring and capacity before relying on the installation.

## Configure a new deployment

Choose unused deployment, service and package keys and a fresh namespace. These assignments preserve unrelated installation entries. Use the replacement sequence for another deployment of the same logical instance.

```bash
export DEPLOYMENT=forgejo
export INSTANCE=forgejo
export CLUSTER_ID=cluster-1
export NS=forgejo-blue
export RELEASE=forgejo
export PACKAGE=forgejo
export CHART_SHA256
yq -i '.deployments[strenv(DEPLOYMENT)] = {
  "instance": strenv(INSTANCE), "cluster": strenv(CLUSTER_ID),
  "namespace": strenv(NS), "release": strenv(RELEASE), "package": strenv(PACKAGE)
} | .services[strenv(INSTANCE)] = {"bootstrapSecret": "forgejo-bootstrap"} |
  .packages[strenv(PACKAGE)] = {
    "version": "0.2.2", "chart": "oci://ghcr.io/bartlapp/bartl-forgejo", "chartSha256": strenv(CHART_SHA256)
  }' "$VALUES"
```

The service defaults to native 17.0.1 and a hostname derived from dns.root. Set optional hostname, claim, SSH/TLS or dependency choices before rendering. Read the selected access and declare connections:

```bash
INSTANCE=$(yq -er '.deployments[strenv(DEPLOYMENT)].instance' "$VALUES")
CLUSTER_ID=$(yq -er '.deployments[strenv(DEPLOYMENT)].cluster' "$VALUES")
NS=$(yq -er '.deployments[strenv(DEPLOYMENT)].namespace' "$VALUES")
RELEASE=$(yq -er '.deployments[strenv(DEPLOYMENT)].release' "$VALUES")
PACKAGE=$(yq -er '.deployments[strenv(DEPLOYMENT)].package' "$VALUES")
export INSTANCE CLUSTER_ID PACKAGE
HELM_INSTANCE=${INSTANCE//./\\.}
KUBE_CONTEXT=$(yq -er '.clusters[strenv(CLUSTER_ID)].kubeconfig.context' "$VALUES")
export KUBECONFIG="$WORK_DIR/clusters/$CLUSTER_ID/base/kubeconfig"
# Replace only with an access file supplied for this selected cluster.
VERSION=$(yq -er '.packages[strenv(PACKAGE)].version' "$VALUES")
test "$VERSION" = 0.2.2
CHART="$WORK_DIR/packages/bartl-forgejo-$VERSION.tgz"
CHART_SHA256=$(yq -er '.packages[strenv(PACKAGE)].chartSha256' "$VALUES")
printf '%s  %s\n' "$CHART_SHA256" "$CHART" | shasum -a 256 --check -
INSTALL_DIR="$WORK_DIR/clusters/$CLUSTER_ID/applications/$INSTANCE"
if [ "$DEPLOYMENT" != "$INSTANCE" ]; then INSTALL_DIR="$INSTALL_DIR/deployments/$DEPLOYMENT"; fi
mkdir -p "$INSTALL_DIR"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
mkdir -p "$INSTALL_DIR/protected/identity" "$INSTALL_DIR/protected/bootstrap"
kubectl --context "$KUBE_CONTEXT" get nodes,storageclass
kubectl --context "$KUBE_CONTEXT" create namespace "$NS"
```

Never inherit source access when selecting another target. The four optional deployment intents are replicas, ingressEnabled, native version and hostname; 0.2.2 consumes instance and explicit Helm overrides, not a deployment selector. Absent intent leaves chart/service settings effective.

Create two independent operator-owned claims, writable by UID/GID and fsGroup 1000, supporting SQLite locks, atomic rename and fsync. Adjust the example capacities once before creation:

```bash
for role in state staging; do
  CLAIM=$(ROLE="$role" yq -er '.storage.claims[strenv(INSTANCE)][strenv(ROLE)].claimName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" create -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $CLAIM
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 20Gi
EOF
done
```

State contains SQLite, Git/LFS, attachments, packages, releases, queues, indexes and logs. Staging holds a complete independent capture and integrity metadata plus failed-publication headroom. WaitForFirstConsumer claims may remain Pending until scheduling. Never share a claim between active writers; the package does not delete claims or supplied Secrets.

## DNS, TLS and SSH exposure

Inspect the selected ingress address and create/update only the authorized hostname record. Correct conflicting AAAA records, then require authoritative/public answers before ACME:

```bash
kubectl --context "$KUBE_CONTEXT" -n kube-system get service traefik -o wide
printf 'HTTPS: %s; SSH: %s:%s\n' "$FORGEJO_HOST" "$SSH_HOST" "$SSH_PORT"
dig "$FORGEJO_HOST" A +trace
dig @1.1.1.1 +short "$FORGEJO_HOST" A
dig @8.8.8.8 +short "$FORGEJO_HOST" A
```

Managed TLS needs cert-manager and public ports 80/443; it creates a namespace-local ACME Issuer/Certificate and renews it. Staging ACME is not browser-trusted. For **external TLS only**, set tls.managed: false before rendering, then create the declared Secret from the supplied certificate/key:

```bash
TLS_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret tls "$TLS_SECRET" \
  --cert="$INSTALL_DIR/tls/fullchain.pem" --key="$INSTALL_DIR/tls/key.pem"
```

Preserve external ownership on later upgrades. The install command below derives the binding independently in managed mode; it never depends on this optional block.

HTTP ingress does not expose SSH. The default forgejo-ssh Service is LoadBalancer port 2222; the environment must supply the load balancer and firewall reachability. K3s ServiceLB needs that node port free. Point the declared SSH hostname to that address. NodePort requires an explicit ssh.nodePort and firewall/DNS routing; ClusterIP requires a separately supplied TCP route. Native configuration advertises the selected external port. Verify actual SSH connectivity and retained host identity after installation, not only a Service object.

## Optional external dependencies

For external OIDC, choose the provider-supplied issuer and render its client/callback requirements:

```bash
export OIDC_ISSUER=https://login.example.org/realms/acme
yq -i '.services[strenv(INSTANCE)].oidc.issuer = strenv(OIDC_ISSUER)' "$VALUES"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
yq '.oidc.requests[strenv(INSTANCE)]' "$CONNECTIONS"
```

Register a confidential authorization-code client with the declared callbacks/origins, S256 PKCE and no implicit/password grant. Retain its secret without a newline at INSTALL_DIR/protected/oidc-client-secret. No OIDC selection means local-only access. For composed Keycloak use the dedicated section below instead; native Forgejo authentication-source registration follows installation.

For backup, supply a dedicated initialized HTTPS Restic repository and independently retained scoped inputs. [Restic's handoff](https://github.com/bartlapp/bartl-restic/blob/main/README.md#connect-an-application) supplies compatible files; an external service is sufficient:

```bash
export REPOSITORY_URL=rest:https://backup.example.org/forgejo/
yq -i '.services[strenv(INSTANCE)].backup.repository = strenv(REPOSITORY_URL)' "$VALUES"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
```

Put reader/writer directories under INSTALL_DIR/repository/, each containing password (shared repository encryption key), username, restPassword and optional ca.crt. Restore older protected/backup inputs here if needed; never regenerate existing-history keys. Reader may manage native locks but not backup contents; writer is append-only. Admin/retention/replication authority stays with the backup service.

Create runtime copies only when backup is selected:

```bash
for pair in reader:backupReader writer:backupWriter; do
  directory=${pair%%:*}; role=${pair#*:}
  name=$(ROLE="$role" yq -er '.credentials.bindings[strenv(INSTANCE)][strenv(ROLE)].secretName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$name" \
    --from-file="$INSTALL_DIR/repository/$directory/"
done
```

Omission means no backup; explicit backup.enabled: false disables it. Schedule is empty by default (manual-only), timeZone UTC. Set an agreed schedule under services.<instance>.backup.schedule. Lookup or integrity errors never mean absent history. Serving processes receive no repository access.

## Optional composition with Keycloak and Restic

First operate the selected providers through their own release READMEs. Do not create their deployments, credentials or storage here. Historical composition used Keycloak 0.6.1 and Restic 0.3.0; the current Keycloak candidate has a different deployment interface. Consume current declaration files from those procedures, not guessed endpoints or provider CRs.

For **Keycloak instead of direct oidc.issuer**, select its retained deployment and declaration file:

```bash
export KC_DEPLOYMENT=keycloak
export KC_CONNECTIONS="$WORK_DIR/clusters/cluster-1/applications/keycloak/connections.yaml"
KC_INSTANCE=$(yq -er '.deployments[strenv(KC_DEPLOYMENT)].instance' "$VALUES")
export KC_INSTANCE
yq -i '.services[strenv(INSTANCE)].identityProvider = strenv(KC_INSTANCE)' "$VALUES"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
KC_URL=$(yq -er '.services[strenv(KC_INSTANCE)].endpoints.https.url' "$KC_CONNECTIONS")
OIDC_ISSUER=$(yq -er '.services[strenv(KC_INSTANCE)].oidc.issuer' "$KC_CONNECTIONS")
OIDC_CLIENT_ID=$(yq -er '.oidc.requests[strenv(INSTANCE)].clientId' "$INSTALL_DIR/forgejo-connections.yaml")
export OIDC_ISSUER OIDC_CLIENT_ID
yq -i '.resolved.oidc.bindings[strenv(INSTANCE)] = {
  "provider": strenv(KC_INSTANCE), "issuer": strenv(OIDC_ISSUER), "clientId": strenv(OIDC_CLIENT_ID)
}' "$VALUES"
```

The provider binding must match identityProvider. For **composed backup instead of direct backup.repository**, use the selected Restic deployment's declarations and an already admitted dedicated repository:

```bash
export RESTIC_DEPLOYMENT=restic
export RESTIC_CONNECTIONS="$WORK_DIR/clusters/cluster-1/applications/restic/restic-connections.yaml"
export REPOSITORY_NAME=forgejo
RESTIC_INSTANCE=$(yq -er '.deployments[strenv(RESTIC_DEPLOYMENT)].instance' "$VALUES")
export RESTIC_INSTANCE
BACKUP_REPOSITORY=$(yq -er '.backup.providers[strenv(RESTIC_INSTANCE)].repositories[strenv(REPOSITORY_NAME)].repository' "$RESTIC_CONNECTIONS")
export BACKUP_REPOSITORY
yq -i '.services[strenv(INSTANCE)].backup.endpoint = strenv(RESTIC_INSTANCE) |
  .resolved.backup.bindings[strenv(INSTANCE)] = {
    "endpoint": strenv(RESTIC_INSTANCE), "repository": strenv(BACKUP_REPOSITORY)
  }' "$VALUES"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
for pair in reader:backupReader writer:backupWriter; do
  directory=${pair%%:*}; role=${pair#*:}
  name=$(ROLE="$role" yq -er '.credentials.bindings[strenv(INSTANCE)][strenv(ROLE)].secretName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$name" \
    --from-file="$INSTALL_DIR/repository/$directory/"
done
```

Starting condition: no direct repository override for this composed consumer, and scoped repository input files already supplied through Restic's handoff. Preserve keys and transport passwords. Refresh derived bindings after provider choices change; declarations neither create a repository nor prove protection.

### Provision the native Keycloak client

Use a current authorized master-realm administrator with admin-cli password grant and realm/client creation/read/secret access. Supply its files under protected/keycloak-admin. It is preparation-only, never a Forgejo runtime credential. Inspect the realm and declared client first:

```bash
ADMIN_DIR="$INSTALL_DIR/protected/keycloak-admin"
CLIENT_WORK=$(mktemp -d "$INSTALL_DIR/protected/keycloak-client.XXXXXX")
curl --fail-with-body --silent --show-error "$KC_URL/realms/master/protocol/openid-connect/token" \
  --data-urlencode client_id=admin-cli --data-urlencode grant_type=password \
  --data-urlencode "username@$ADMIN_DIR/username" --data-urlencode "password@$ADMIN_DIR/password" > "$CLIENT_WORK/token.json"
TOKEN=$(jq -er '.access_token' "$CLIENT_WORK/token.json")
REALM=${OIDC_ISSUER##*/}
REALM_PATH=$(jq -nr --arg realm "$REALM" '$realm | @uri')
ADMIN_URL="$KC_URL/admin/realms/$REALM_PATH"
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" |
  curl --config - --fail-with-body --silent --show-error "$KC_URL/admin/realms" | jq '.[].realm'
yq -o=json '.oidc.requests[strenv(INSTANCE)]' "$INSTALL_DIR/forgejo-connections.yaml" |
  jq '{clientId, redirectUris, webOrigins, protocol: "openid-connect", enabled: true,
    publicClient: false, standardFlowEnabled: true, directAccessGrantsEnabled: false,
    implicitFlowEnabled: false, serviceAccountsEnabled: false,
    attributes: {"pkce.code.challenge.method": "S256"}}' > "$CLIENT_WORK/client.json"
cat "$CLIENT_WORK/client.json"
```

For an absent authorized realm only, create it once; otherwise preserve the enabled realm/users:

```bash
jq -n --arg realm "$REALM" '{realm: $realm, enabled: true}' > "$CLIENT_WORK/realm.json"
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" |
  curl --config - --fail-with-body --silent --show-error --request POST "$KC_URL/admin/realms" \
    --header 'Content-Type: application/json' --data-binary "@$CLIENT_WORK/realm.json"
```

Inspect the client by its exact ID:

```bash
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" |
  curl --config - --fail-with-body --silent --show-error --get "$ADMIN_URL/clients" \
    --data-urlencode "clientId=$OIDC_CLIENT_ID" > "$CLIENT_WORK/clients.json"
jq . "$CLIENT_WORK/clients.json"
```

If absent and authorized, create it. Otherwise inspect the existing callbacks/origins, enabled confidential standard flow, S256 PKCE and disabled implicit/direct grants/service accounts. Reconcile differences deliberately, never reset a restored client:

```bash
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" |
  curl --config - --fail-with-body --silent --show-error --request POST "$ADMIN_URL/clients" \
    --header 'Content-Type: application/json' --data-binary "@$CLIENT_WORK/client.json"
```

Retrieve its existing secret with GET, not rotating POST. The retained destination must be new; for an existing file, compare retrieved bytes and coordinate mismatches rather than overwrite:

```bash
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" |
  curl --config - --fail-with-body --silent --show-error --get "$ADMIN_URL/clients" \
    --data-urlencode "clientId=$OIDC_CLIENT_ID" > "$CLIENT_WORK/clients.json"
CLIENT_UUID=$(jq -er --arg id "$OIDC_CLIENT_ID" '[.[] | select(.clientId == $id)] |
  if length == 1 then .[0].id else error("Expected exactly one client") end' "$CLIENT_WORK/clients.json")
printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" |
  curl --config - --fail-with-body --silent --show-error "$ADMIN_URL/clients/$CLIENT_UUID/client-secret" > "$CLIENT_WORK/secret.json"
jq -erj '.value | select(length > 0)' "$CLIENT_WORK/secret.json" > "$CLIENT_WORK/secret"
(set -o noclobber; cat "$CLIENT_WORK/secret" > "$INSTALL_DIR/protected/oidc-client-secret")
unset TOKEN
rm -r "$CLIENT_WORK"
```

Configure users and MFA through native Keycloak administration. Provider registration does not automatically link native Forgejo accounts.

## Prepare native identity and configuration

For a fresh installation only, create identity keys once in the empty directory. For recovery, restore the retained identity instead:

```bash
(
  set -o noclobber
  for key in SECRET_KEY INTERNAL_TOKEN LFS_JWT_SECRET; do
    openssl rand -hex 32 | tr -d '\n' > "$INSTALL_DIR/protected/identity/$key"
  done
)
```

For fresh bootstrap, choose the local recovery administrator and retain its password:

```bash
export ADMIN_USER=admin
export ADMIN_EMAIL=admin@example.org
(
  set -o noclobber
  printf '%s' "$ADMIN_USER" > "$INSTALL_DIR/protected/bootstrap/username"
  printf '%s' "$ADMIN_EMAIL" > "$INSTALL_DIR/protected/bootstrap/email"
  openssl rand -hex 32 | tr -d '\n' > "$INSTALL_DIR/protected/bootstrap/password"
)
BOOTSTRAP_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].bootstrap.secretName' "$CONNECTIONS")
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$BOOTSTRAP_SECRET" --from-file="$INSTALL_DIR/protected/bootstrap/"
```

Bootstrap applies only to genuinely empty state. Render native configuration and append retained identity keys without printing protected content:

```bash
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=configuration --show-only templates/configuration.yaml > "$INSTALL_DIR/forgejo-configuration.yaml"
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
yq -er '.configuration[strenv(INSTANCE)].appIni' "$CONFIGURATION" > "$INSTALL_DIR/protected/app.ini"
{
  printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/SECRET_KEY")" "$(cat "$INSTALL_DIR/protected/identity/INTERNAL_TOKEN")"
  printf '\n[server]\nLFS_JWT_SECRET = %s\n' "$(cat "$INSTALL_DIR/protected/identity/LFS_JWT_SECRET")"
} >> "$INSTALL_DIR/protected/app.ini"
CONFIG_SECRET=$(yq -er '.configuration[strenv(INSTANCE)].secretName' "$CONFIGURATION")
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$CONFIG_SECRET" \
  --from-file="app.ini=$INSTALL_DIR/protected/app.ini" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply --server-side --field-manager=forgejo-preparation -f -
```

Rendering starts from the chart template, not an old app.ini. It supplies native paths, URLs/ports and disabled-effect settings. Retain configuration/identity independently before installation. Backup captures native state and identity/version/integrity metadata; target configuration, TLS and repository credentials remain separate recovery inputs.

## Install and observe

```bash
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm install "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Ready\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
if yq -e '.services[strenv(INSTANCE)].endpoints.https.tlsManaged' "$CONNECTIONS" >/dev/null; then
  TLS_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" wait "certificate/$TLS_SECRET" --for=condition=Ready --timeout=10m
fi
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o yaml
kubectl --context "$KUBE_CONTEXT" -n "$NS" get service forgejo-ssh -o wide
curl --fail --silent --show-error --output /dev/null "$FORGEJO_URL"
```

Require current-generation Ready and the intended native version, trusted HTTPS and actual SSH reachability. Through authenticated HTTP/SSH, clone and push a synthetic repository; verify LFS, issues, pull requests, attachments, packages/releases, permissions and preserved SSH identity as used. LFS returns absolute object URLs, so use the real configured hostname, not client-side URL rewriting. A successful Helm invocation does not establish application acceptance.

### Register Forgejo's native authentication source

For selected OIDC, configuration rendering declares native OAuth arguments, discovery URL and callback but does not mutate Forgejo's database. After successful installation, read those declarations and inspect native auth sources:

```bash
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=configuration --show-only templates/configuration.yaml > "$CONFIGURATION"
NAME=$(yq -er '.configuration[strenv(INSTANCE)].oidc.name' "$CONFIGURATION")
CLIENT=$(yq -er '.configuration[strenv(INSTANCE)].oidc.clientId' "$CONFIGURATION")
DISCOVERY=$(yq -er '.configuration[strenv(INSTANCE)].oidc.discoveryUrl' "$CONFIGURATION")
NATIVE_VERSION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.spec.version}')
BIN="/usr/local/lib/forgejo/$NATIVE_VERSION/forgejo"
POD=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods \
  -l app.kubernetes.io/name=forgejo,forgejo.bartl.sh/role=app -o json |
  jq -er '[.items[] | select(.status.phase == "Running")] | if length == 1 then .[0].metadata.name else error("Expected one serving pod") end')
kubectl --context "$KUBE_CONTEXT" -n "$NS" exec "$POD" -c forgejo -- \
  "$BIN" --work-path /data --custom-path /data/custom --config /data/app.ini admin auth list
```

For an absent source with this name, create it once. The retained secret travels through protected stdin, not local command arguments:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" exec -i "$POD" -c forgejo -- /bin/sh -c '
  SECRET=$(cat)
  exec "$1" --work-path /data --custom-path /data/custom --config /data/app.ini admin auth add-oauth \
    --name "$2" --provider openidConnect --key "$3" --secret "$SECRET" \
    --auto-discover-url "$4" --scopes openid --scopes profile --scopes email
' sh "$BIN" "$NAME" "$CLIENT" "$DISCOVERY" < "$INSTALL_DIR/protected/oidc-client-secret"
```

For a deliberately approved update to an existing unique source, select its ID from native auth list and update that source instead. Do not run add-oauth again or overwrite restored native account-linking decisions:

```bash
AUTH_ID=REPLACE_WITH_INSPECTED_SOURCE_ID
kubectl --context "$KUBE_CONTEXT" -n "$NS" exec -i "$POD" -c forgejo -- /bin/sh -c '
  SECRET=$(cat)
  exec "$1" --work-path /data --custom-path /data/custom --config /data/app.ini admin auth update-oauth \
    --id "$5" --name "$2" --provider openidConnect --key "$3" --secret "$SECRET" \
    --auto-discover-url "$4" --scopes openid --scopes profile --scopes email
' sh "$BIN" "$NAME" "$CLIENT" "$DISCOVERY" "$AUTH_ID" < "$INSTALL_DIR/protected/oidc-client-secret"
```

Native auth records survive capture; independently retain their provider/client. Keep tested local-admin access for identity outages. Verify fresh browser authorization-code login, reviewed MFA/account behavior, logout and another login. Automatic account linking is disabled; unverified matching email is not identity.

## Resume an existing deployment

Restore the verified archive and protected inputs, then select the retained deployment in fresh Bash. This reads configuration and refreshes local declarations only:

```bash
set -euo pipefail
umask 077
export WORK_DIR="$HOME/bartl/acme/acme-installation"
export VALUES="$WORK_DIR/installation.yaml"
export DEPLOYMENT=forgejo
INSTANCE=$(yq -er '.deployments[strenv(DEPLOYMENT)].instance' "$VALUES")
CLUSTER_ID=$(yq -er '.deployments[strenv(DEPLOYMENT)].cluster' "$VALUES")
NS=$(yq -er '.deployments[strenv(DEPLOYMENT)].namespace' "$VALUES")
RELEASE=$(yq -er '.deployments[strenv(DEPLOYMENT)].release' "$VALUES")
PACKAGE=$(yq -er '.deployments[strenv(DEPLOYMENT)].package' "$VALUES")
export INSTANCE CLUSTER_ID PACKAGE
HELM_INSTANCE=${INSTANCE//./\\.}
KUBE_CONTEXT=$(yq -er '.clusters[strenv(CLUSTER_ID)].kubeconfig.context' "$VALUES")
export KUBECONFIG="$WORK_DIR/clusters/$CLUSTER_ID/base/kubeconfig"
# Replace only with an access file supplied for this selected cluster.
VERSION=$(yq -er '.packages[strenv(PACKAGE)].version' "$VALUES")
test "$VERSION" = 0.2.2
CHART="$WORK_DIR/packages/bartl-forgejo-$VERSION.tgz"
CHART_SHA256=$(yq -er '.packages[strenv(PACKAGE)].chartSha256' "$VALUES")
printf '%s  %s\n' "$CHART_SHA256" "$CHART" | shasum -a 256 --check -
INSTALL_DIR="$WORK_DIR/clusters/$CLUSTER_ID/applications/$INSTANCE"
if [ "$DEPLOYMENT" != "$INSTANCE" ]; then INSTALL_DIR="$INSTALL_DIR/deployments/$DEPLOYMENT"; fi
mkdir -p "$INSTALL_DIR"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o yaml
```

Do not generate identity/bootstrap keys, recreate claims or re-register OIDC to resume. For native auth inspection after resumption, render nonsecret configuration from the same retained chart/intent first; it does not change an authentication source.

## Capture and retry publication

Start from a resumed deployment with backup configured. Complete pending publication with its original state/staging claims and native version before changing those inputs or uninstalling. For a new capture, the writer must still be running; the controller owns drainage, stopped copy, permitted resumption and publication:

```bash
REQUEST="manual-$(date -u +%Y%m%dT%H%M%SZ)"
kubectl --context "$KUBE_CONTEXT" -n "$NS" annotate forgejoapplication/forgejo \
  "forgejo.bartl.sh/backup-request=$REQUEST" --overwrite
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.backup.request}=$REQUEST" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for=jsonpath='{.status.backup.success}'=true --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json > "$INSTALL_DIR/backup-result.json"
jq -e --arg request "$REQUEST" '.status.backup |
  .request == $request and .success == true and (.snapshot | test("^[a-f0-9]{64}$"))' "$INSTALL_DIR/backup-result.json"
```

Require this request's success and full snapshot identity. BackupFailed retains a complete staging capture and is separate from application health. Correct publication inputs and issue another unique request to retry that capture. Its original capture boundary does not advance; request a subsequent fresh capture for later writes. Never delete staging or change state/staging/version while publication is pending. Failed/ambiguous capture does not authorize restart.

Monitor absent/stale/failed scheduled backups, staging capacity and required secondary protection. The CronJob submits this same request interface. Native locking remains enabled; never blindly unlock. Damaged latest history fails visibly rather than selecting an older point or initializing.

## Stop and start

Ordinary stop is allowed even during pending/failed publication. Export needed diagnostics and persist intent; do not scale generated Deployments:

```bash
yq -i '.operations.deployments[strenv(DEPLOYMENT)].replicas = 0' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Stopped\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Stopped --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Stopped" and .status == "True" and .observedGeneration == $g)'
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods,jobs,deployments
```

Require confirmed drainage. Serving can continue through API/controller observation loss; a stop request or unreachable source is not retirement. After a nonfailed stop, start the same retained deployment:

```bash
yq -i '.operations.deployments[strenv(DEPLOYMENT)].replicas = 1' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Ready\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
```

Verify useful HTTP/SSH behavior without backup access. Failed transitions remain failed across run intent; never clear markers to restart them.

## Replacement upgrade and promotion

Use this sequence for same-release reconstruction or supported native 16.0.7 → 17.0.1 replacement. Supply the verified package, independent empty state/staging claims, retained native identity, usable repository/reader access, and an authorized final point. Source and target share VALUES and logical instance but have separate deployment intent and storage.

Arrange the interruption/data-loss boundary first. The environment must restrict target **HTTP and SSH** to acceptance operators without alternate-route bypass. No production effects or ordinary writes are allowed during validation. This package has no cross-provider fencing; if predecessor authority cannot be withdrawn, do not promote. DNS changes alone are insufficient.

### Capture final writes before source stop

Resume the reachable source. Withdraw external HTTP/API and **direct SSH** access using the environment's enforced routing/firewall controls, drain existing clients and prevent concurrent operators/scheduled captures. Keep the source writer running so the package can capture it; do not run the stop block first. Disable ingress and automatic capture scheduling, preserving its prior schedule:

```bash
export SOURCE_DEPLOYMENT="$DEPLOYMENT"
export SOURCE_DIR="$INSTALL_DIR"
export SOURCE_HOST="$FORGEJO_HOST"
BACKUP_SCHEDULE=$(yq -r '.services[strenv(INSTANCE)].backup.schedule // ""' "$VALUES")
export BACKUP_SCHEDULE
yq -i '.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled = false |
  .services[strenv(INSTANCE)].backup.schedule = ""' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Ready\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
REQUEST="manual-$(date -u +%Y%m%dT%H%M%SZ)"
kubectl --context "$KUBE_CONTEXT" -n "$NS" annotate forgejoapplication/forgejo \
  "forgejo.bartl.sh/backup-request=$REQUEST" --overwrite
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.backup.request}=$REQUEST" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for=jsonpath='{.status.backup.success}'=true --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json > "$INSTALL_DIR/backup-result.json"
jq -e --arg request "$REQUEST" '.status.backup |
  .request == $request and .success == true and (.snapshot | test("^[a-f0-9]{64}$"))' "$INSTALL_DIR/backup-result.json"
```

Require success for this final request and any required secondary protection. Finish pending publication using original claims/version; failure postpones planned replacement. Then stop and verify drainage:

```bash
yq -i '.operations.deployments[strenv(DEPLOYMENT)].replicas = 0' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Stopped\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Stopped --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Stopped" and .status == "True" and .observedGeneration == $g)'
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods,jobs
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pvc -o json > "$SOURCE_DIR/source-pvc.json"
```

Prevent source restart and direct volume writes. With final publication complete and all writers stopped, finalize the source and remove its routing objects before reusing the hostname/SSH port:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" delete forgejoapplication/forgejo --wait=true --timeout=20m
helm uninstall "$RELEASE" --kube-context "$KUBE_CONTEXT" -n "$NS" --wait --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get ingress,service,pvc,secrets
```

Claims/Secrets stay retained. Confirm no retired HTTP route or forgejo-ssh Service remains; stopped processes alone do not remove those objects. For an already retired/lost source, skip source API calls, select SOURCE_DEPLOYMENT/SOURCE_DIR and SOURCE_HOST from independently retained configuration/declarations, and read BACKUP_SCHEDULE from its service settings. Independent enforced retirement and usable final history remain prerequisites, not assumptions.

### Prepare the replacement

Choose an unused target deployment, an existing supplied cluster and a fresh namespace. TARGET_VERSION is the genuine choice: retain the source version for same-release recovery or select 17.0.1 for the supported forward transition. Retain a separate package key if adopting another chart; never overwrite the source package selection.

```bash
export DEPLOYMENT=forgejo-replacement
export TARGET_CLUSTER=cluster-2
export TARGET_NAMESPACE=forgejo-green
export TARGET_VERSION=17.0.1
export SOURCE_DEPLOYMENT SOURCE_DIR SOURCE_HOST
INSTANCE=$(yq -er '.deployments[strenv(SOURCE_DEPLOYMENT)].instance' "$VALUES")
PACKAGE=$(yq -er '.deployments[strenv(SOURCE_DEPLOYMENT)].package' "$VALUES")
export INSTANCE PACKAGE
yq -i '.deployments[strenv(DEPLOYMENT)] = {
  "instance": strenv(INSTANCE), "cluster": strenv(TARGET_CLUSTER),
  "namespace": strenv(TARGET_NAMESPACE), "release": "forgejo", "package": strenv(PACKAGE)
} | .operations.deployments[strenv(DEPLOYMENT)] = {
  "replicas": 1, "ingressEnabled": true, "version": strenv(TARGET_VERSION), "hostname": strenv(SOURCE_HOST)
} | .services[strenv(INSTANCE)].backup.schedule = ""' "$VALUES"
INSTANCE=$(yq -er '.deployments[strenv(DEPLOYMENT)].instance' "$VALUES")
CLUSTER_ID=$(yq -er '.deployments[strenv(DEPLOYMENT)].cluster' "$VALUES")
NS=$(yq -er '.deployments[strenv(DEPLOYMENT)].namespace' "$VALUES")
RELEASE=$(yq -er '.deployments[strenv(DEPLOYMENT)].release' "$VALUES")
PACKAGE=$(yq -er '.deployments[strenv(DEPLOYMENT)].package' "$VALUES")
export INSTANCE CLUSTER_ID PACKAGE
HELM_INSTANCE=${INSTANCE//./\\.}
KUBE_CONTEXT=$(yq -er '.clusters[strenv(CLUSTER_ID)].kubeconfig.context' "$VALUES")
export KUBECONFIG="$WORK_DIR/clusters/$CLUSTER_ID/base/kubeconfig"
# Replace only with an access file supplied for this selected cluster.
VERSION=$(yq -er '.packages[strenv(PACKAGE)].version' "$VALUES")
test "$VERSION" = 0.2.2
CHART="$WORK_DIR/packages/bartl-forgejo-$VERSION.tgz"
CHART_SHA256=$(yq -er '.packages[strenv(PACKAGE)].chartSha256' "$VALUES")
printf '%s  %s\n' "$CHART_SHA256" "$CHART" | shasum -a 256 --check -
INSTALL_DIR="$WORK_DIR/clusters/$CLUSTER_ID/applications/$INSTANCE"
if [ "$DEPLOYMENT" != "$INSTANCE" ]; then INSTALL_DIR="$INSTALL_DIR/deployments/$DEPLOYMENT"; fi
mkdir -p "$INSTALL_DIR"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
kubectl --context "$KUBE_CONTEXT" get nodes,storageclass
kubectl --context "$KUBE_CONTEXT" create namespace "$NS"
for role in state staging; do
  CLAIM=$(ROLE="$role" yq -er '.storage.claims[strenv(INSTANCE)][strenv(ROLE)].claimName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" create -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $CLAIM
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 20Gi
EOF
done
cp -R "$SOURCE_DIR/protected" "$INSTALL_DIR/protected"
```

Restore the original native identity, not fresh keys or source-mounted state. For secondary recovery, select its surviving repository URL and supply that repository's own reader/writer files under target repository/; use its destination snapshot identity, not assumed primary IDs. Preserve any composed binding form instead of introducing a conflicting direct URL. Native auth records come from capture; retain the matching provider/client independently.

Refresh declarations and native configuration after the selected repository/endpoint choices:

```bash
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
for pair in reader:backupReader writer:backupWriter; do
  directory=${pair%%:*}; role=${pair#*:}
  name=$(ROLE="$role" yq -er '.credentials.bindings[strenv(INSTANCE)][strenv(ROLE)].secretName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$name" \
    --from-file="$INSTALL_DIR/repository/$directory/"
done
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=configuration --show-only templates/configuration.yaml > "$INSTALL_DIR/forgejo-configuration.yaml"
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
yq -er '.configuration[strenv(INSTANCE)].appIni' "$CONFIGURATION" > "$INSTALL_DIR/protected/app.ini"
{
  printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/SECRET_KEY")" "$(cat "$INSTALL_DIR/protected/identity/INTERNAL_TOKEN")"
  printf '\n[server]\nLFS_JWT_SECRET = %s\n' "$(cat "$INSTALL_DIR/protected/identity/LFS_JWT_SECRET")"
} >> "$INSTALL_DIR/protected/app.ini"
CONFIG_SECRET=$(yq -er '.configuration[strenv(INSTANCE)].secretName' "$CONFIGURATION")
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$CONFIG_SECRET" \
  --from-file="app.ini=$INSTALL_DIR/protected/app.ini" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply --server-side --field-manager=forgejo-preparation -f -
```

For external TLS only, supply the target certificate/key and retain external ownership:

```bash
TLS_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret tls "$TLS_SECRET" \
  --cert="$INSTALL_DIR/tls/fullchain.pem" --key="$INSTALL_DIR/tls/key.pem"
```

Source retirement and target HTTP/SSH acceptance restrictions must now be enforced. Point the authorized hostnames to the target ingress/SSH route and confirm authoritative/public answers before managed certificates:

```bash
kubectl --context "$KUBE_CONTEXT" -n kube-system get service traefik -o wide
printf 'HTTPS: %s; SSH: %s:%s\n' "$FORGEJO_HOST" "$SSH_HOST" "$SSH_PORT"
dig "$FORGEJO_HOST" A +trace
dig @1.1.1.1 +short "$FORGEJO_HOST" A
dig @8.8.8.8 +short "$FORGEJO_HOST" A
```

### Reconstruct and accept

```bash
helm install "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Ready\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
if yq -e '.services[strenv(INSTANCE)].endpoints.https.tlsManaged' "$CONNECTIONS" >/dev/null; then
  TLS_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" wait "certificate/$TLS_SECRET" --for=condition=Ready --timeout=10m
fi
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json > "$INSTALL_DIR/recovery-result.json"
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods,jobs,service
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pvc -o json > "$INSTALL_DIR/target-pvc.json"
curl --fail --silent --show-error --output /dev/null "$FORGEJO_URL"
```

Inspect native preparation logs, selected repository/snapshot and operation result. Require the authorized final point, intended native version and independent PVC UIDs/backing volumes. Native preparation restores the captured version, flushes native queue state and performs the supported forward migration. An empty initialization is not recovery of expected history; lookup/integrity failure does not permit it.

Through restricted real HTTP/SSH, verify clone/push, LFS, issues/PRs, attachment/package/release bytes, grants and SSH host identity. Check ordinary sources are denied. Verify fresh OIDC login if used without rewriting restored auth sources. Produce new synthetic work and another usable recovery point:

```bash
REQUEST="manual-$(date -u +%Y%m%dT%H%M%SZ)"
kubectl --context "$KUBE_CONTEXT" -n "$NS" annotate forgejoapplication/forgejo \
  "forgejo.bartl.sh/backup-request=$REQUEST" --overwrite
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.backup.request}=$REQUEST" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for=jsonpath='{.status.backup.success}'=true --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json > "$INSTALL_DIR/backup-result.json"
jq -e --arg request "$REQUEST" '.status.backup |
  .request == $request and .success == true and (.snapshot | test("^[a-f0-9]{64}$"))' "$INSTALL_DIR/backup-result.json"
```

After acceptance, restore the approved capture schedule and remove environment restrictions to activate production HTTP and SSH:

```bash
yq -i '.services[strenv(INSTANCE)].backup.schedule = strenv(BACKUP_SCHEDULE)' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Ready\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
```

Record active deployment, accepted final-write boundary, selected point, observed recovery time and results in existing operating records. Keep source stopped/retained until disposition is authorized. If target writes occurred, never automatically reactivate the predecessor; establish authoritative state and reconcile explicitly. Failed/ambiguous mutation remains stopped: retain diagnostics and reconstruct on another independent empty target, not by deleting markers.

An earlier isolated rehearsal may use a deployment-local candidate hostname and restricted real HTTP/SSH, but must not publish competing history or cause production effects. It does not include later source writes. After the final source capture, reconstruct a fresh target through this sequence; do not silently promote the earlier rehearsal.

## Change the selected endpoint

Starting condition: resumed deployment, approved endpoint change, current publication complete and access restricted during the change. Set the one new hostname; retain existing identity. Update any separately overridden ssh.hostname and native provider callbacks deliberately. If removing an override, delete that field to restore package derivation rather than recording the derived hostname as a permanent choice.

```bash
export NEW_HOST=forgejo.acme.fke.systems
yq -i '.operations.deployments[strenv(DEPLOYMENT)].hostname = strenv(NEW_HOST)' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" "${HELM_INTENT[@]}" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  --set-string renderMode=configuration --show-only templates/configuration.yaml > "$INSTALL_DIR/forgejo-configuration.yaml"
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
yq -er '.configuration[strenv(INSTANCE)].appIni' "$CONFIGURATION" > "$INSTALL_DIR/protected/app.ini"
{
  printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/SECRET_KEY")" "$(cat "$INSTALL_DIR/protected/identity/INTERNAL_TOKEN")"
  printf '\n[server]\nLFS_JWT_SECRET = %s\n' "$(cat "$INSTALL_DIR/protected/identity/LFS_JWT_SECRET")"
} >> "$INSTALL_DIR/protected/app.ini"
CONFIG_SECRET=$(yq -er '.configuration[strenv(INSTANCE)].secretName' "$CONFIGURATION")
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$CONFIG_SECRET" \
  --from-file="app.ini=$INSTALL_DIR/protected/app.ini" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply --server-side --field-manager=forgejo-preparation -f -
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Ready\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
if yq -e '.services[strenv(INSTANCE)].endpoints.https.tlsManaged' "$CONNECTIONS" >/dev/null; then
  TLS_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" wait "certificate/$TLS_SECRET" --for=condition=Ready --timeout=10m
fi
```

Prepare authorized DNS and a matching external certificate, if used, before this command sequence; managed issuance needs public port 80. Verify real HTTPS/SSH and LFS at the newly advertised endpoint, update approved OIDC callbacks, then reopen access. Never use URL rewriting as LFS acceptance.

## Removal and retained-state reinstall

Starting condition: resumed deployment and completed publication. If publication is pending/failed, retry its retained capture through the capture interface while keeping controller, CR, original state/staging claims and native version; failed publication postpones planned uninstall, not ordinary stop. Do not change those inputs to escape the pending operation.

Persist stop, wait for the current generation and inspect drainage:

```bash
yq -i '.operations.deployments[strenv(DEPLOYMENT)].replicas = 0' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Stopped\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Stopped --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Stopped" and .status == "True" and .observedGeneration == $g)'
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods,jobs
```

With publication complete, no executing writers/native Jobs and removal authorized, finalize while the controller is alive:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" delete forgejoapplication/forgejo --wait=true --timeout=20m
helm uninstall "$RELEASE" --kube-context "$KUBE_CONTEXT" -n "$NS" --wait --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pvc,secrets,ingress,service
```

Never uninstall first, bypass finalizers or issue a Helm upgrade between CR deletion and uninstall. Claims, supplied Secrets and shared CRD remain. Namespace/PVC destruction requires separate data-disposition authorization; infrastructure and owned DNS cleanup remain separate.

For authorized reinstall after successful finalization, use the same identity, claims, configuration and supplied Secrets with no competing predecessor or failed marker. **Reset retained stop intent** before installing; do not recreate storage or credentials:

```bash
yq -i '.operations.deployments[strenv(DEPLOYMENT)].replicas = 1' "$VALUES"
REPLICAS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].replicas, .services[strenv(INSTANCE)].replicas, 1] | map(select(. != null)) | .[0]' "$VALUES")
INGRESS=$(yq -r '[.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled, .services[strenv(INSTANCE)].ingress.enabled, true] | map(select(. != null)) | .[0]' "$VALUES")
NATIVE_VERSION=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].version // ""' "$VALUES")
TARGET_HOST=$(yq -r '.operations.deployments[strenv(DEPLOYMENT)].hostname // ""' "$VALUES")
HELM_INTENT=(--set "services.$HELM_INSTANCE.replicas=$REPLICAS" --set "services.$HELM_INSTANCE.ingress.enabled=$INGRESS")
if [ -n "$NATIVE_VERSION" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.version=$NATIVE_VERSION"); fi
if [ -n "$TARGET_HOST" ]; then HELM_INTENT+=(--set-string "services.$HELM_INSTANCE.hostname=$TARGET_HOST"); fi
helm install "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string "services.$HELM_INSTANCE.release=$VERSION" \
  "${HELM_INTENT[@]}" --wait=legacy --timeout=5m
GENERATION=$(kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o jsonpath='{.metadata.generation}')
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.conditions[?(@.type==\"Ready\")].observedGeneration}=$GENERATION" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
```

Verify retained native identity/content, new work and another usable backup. Uninstall cannot repair a failed transition or preserve pending publication automatically.

## Handover and reference

Hand over exact artifacts, effective installation/native configuration, native identity, volume inventory, protected keys, repository history, reader/writer/admin role boundaries, HTTPS/SSH routing/host-key records, identity dependencies, monitoring/retention responsibility and these procedures. Recovery inputs must be usable without the lost cluster or its Keycloak.

The chart's [interface schema](chart/interface.schema.json) and [defaults](chart/defaults.yaml) define service choices. Omitted fields derive/default, maps merge, lists replace and false/zero remain effective. Unknown selected-service fields fail; unrelated installation content is ignored. Public connections expose HTTPS/SSH endpoints, OIDC/backup requests, credential requirements/bindings and state/staging claims. Configuration output provides native app.ini and optional OAuth arguments; protected identity remains external. Refresh calculated resolved bindings after provider choices change.

[Maintainer procedures and historical evidence](AGENTS.md#historical-evidence) retain exact runtime qualification and build/publication instructions. Historical application reconstruction on one node does not qualify this rewritten sequence, provider-loss activation or production-scale interruption limits.

Authored code is Apache-2.0. Forgejo is GPL-3.0-or-later; upstream notices/licenses and corresponding source are retained separately in the combined image. See [NOTICE](NOTICE) and [LICENSE](LICENSE).
