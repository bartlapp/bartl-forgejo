# Bartlized Forgejo

Native Forgejo with SQLite/filesystem state, HTTPS/SSH access, stopped capture and Restic publication. One application controller coordinates native work; the administrator selects target, release and promotion.

New deployment: follow the recipe below. Existing deployment: [resume access](#resume-an-existing-deployment), then choose an operation. Use workstation Bash, keep tracing off and stop at a failed command. Creation is not a replay or repair program.

- [Install](#supported-profile-and-release) and [resume](#resume-an-existing-deployment)
- [Capture](#capture-and-retry-publication) and [stop/start](#stop-and-start)
- [Replace or upgrade](#replacement-upgrade-and-promotion), [change endpoints](#change-the-selected-endpoint) and [remove/reinstall](#removal-and-retained-state-reinstall)

## Supported profile and release

Candidate **0.3.0-dev.3** targets Linux amd64, Kubernetes 1.30+, one SQLite process and native Forgejo 16.0.7 or 17.0.1. Its corrected controller build, exact-artifact qualification and publication remain pending; the source pin identifies dev.2 until the new build is retained. The supported forward upgrade is **replacement from 16.0.7 to 17.0.1**. Native preparation owns initialization/reconstruction/migration; ordinary restart serves retained state without backup access. Failed/ambiguous mutation stays stopped; retry uses another independent empty target. Actions, mail, mirrors, federation and external webhooks are disabled. This planned-interruption profile does not implement provider-loss fencing.

Supply the [operator tools](https://github.com/bartlapp/bartl-app/blob/main/docs/operator-workstation.md#check-the-workstation), an existing version-1 installation record with complete cluster/resource references and retained access, enforced NetworkPolicy, Traefik, cert-manager and state/staging/reconstruction capacity. [Base](https://github.com/bartlapp/base-k3s/blob/main/README.md) supplies a single-node profile; an equivalent cluster suffices. Compatible external OIDC/Restic services work without another Bartl package or company tooling.

Use direnv-hooked Bash and the prepared cluster directory. Equivalent external clusters use the shared [supplied cluster access](https://github.com/bartlapp/bartl-app/blob/main/docs/operator-workstation.md#use-supplied-cluster-access) procedure, without Hetzner machine fields.

```bash
cd "${WORK_DIR_ROOT:-$HOME/bartl}/acme/acme-installation/clusters/cluster-1"
```

At the next prompt, acquire the explicitly selected prerelease once published. Until then qualification uses the retained digest-pinned chart produced by `build.sh`, not an older published chart. Package and native Forgejo versions remain separate selections:

```bash
set -euo pipefail
umask 077
kubectl --context "$KUBE_CONTEXT" get nodes,storageclass
mkdir -p "$WORK_DIR/packages"
CHART_STAGE=$(mktemp -d "$WORK_DIR/packages/.forgejo.XXXXXX")
helm pull oci://ghcr.io/bartlapp/bartl-forgejo --version 0.3.0-dev.3 --untar --untardir "$CHART_STAGE"
export PACKAGE_VERSION
PACKAGE_VERSION=$(helm show chart "$CHART_STAGE/bartl-forgejo" | yq -er '.version')
```

These procedures require `forgejo.bartl.sh/v1alpha2`. Published 0.2.3 remains a historical release with its own [release evidence](https://github.com/bartlapp/bartl-forgejo/releases/tag/v0.2.3) and replica-through-Helm interface; it does not implement these instructions. There is no migration or adoption of old application resources. Use a separate target cluster if an existing cluster-wide CRD/controller cannot safely coexist with this API. Candidate qualification and publication identities belong in its release record, not historical results.

The registry supplies immutable charts and images; local caches are disposable. Retain configuration, native identity, repository history/keys and routing/SSH host-key records independently of the covered failure domain. Agree interruption, final-write boundary, source retirement, protection/retention, monitoring and capacity before relying on the installation.

## Configure a new deployment

Choose unused deployment and service keys and a fresh namespace. Reuse a package key only when its recorded chart and version match the acquired artifact; otherwise choose an unused package key. These assignments preserve unrelated installation entries. Use the replacement sequence for another deployment of the same logical instance.

```bash
{
export DEPLOYMENT=forgejo
export INSTANCE=forgejo
export NS=forgejo-blue
export RELEASE=forgejo
export PACKAGE=forgejo
yq -e '.packages[strenv(PACKAGE)] == null or
  (.packages[strenv(PACKAGE)].version == strenv(PACKAGE_VERSION) and
   .packages[strenv(PACKAGE)].chart == "oci://ghcr.io/bartlapp/bartl-forgejo")' "$VALUES" >/dev/null
yq -i '.deployments[strenv(DEPLOYMENT)] = {
  "instance": strenv(INSTANCE), "cluster": strenv(CLUSTER_ID),
  "namespace": strenv(NS), "release": strenv(RELEASE), "package": strenv(PACKAGE)
} | .services[strenv(INSTANCE)] = {"bootstrapSecret": "forgejo-bootstrap"} |
  .packages[strenv(PACKAGE)] = (.packages[strenv(PACKAGE)] // {
    "version": strenv(PACKAGE_VERSION), "chart": "oci://ghcr.io/bartlapp/bartl-forgejo"
  })' "$VALUES"
DEPLOYMENT_DIR="$WORK_DIR/clusters/$CLUSTER_ID/applications/$INSTANCE"
CLUSTER_ENV=../../.envrc
if [ "$DEPLOYMENT" != "$INSTANCE" ]; then
  DEPLOYMENT_DIR="$DEPLOYMENT_DIR/deployments/$DEPLOYMENT"
  CLUSTER_ENV=../../../../.envrc
fi
mkdir -p "$DEPLOYMENT_DIR" "$WORK_DIR/packages/$PACKAGE"
(
  set -o noclobber
  printf 'source_env %s\nload_deployment "%s"\n' "$CLUSTER_ENV" "$DEPLOYMENT" > "$DEPLOYMENT_DIR/.envrc"
)
if [ ! -d "$WORK_DIR/packages/$PACKAGE/$PACKAGE_VERSION" ]; then
  mv "$CHART_STAGE/bartl-forgejo" "$WORK_DIR/packages/$PACKAGE/$PACKAGE_VERSION"
fi
rm -r "$CHART_STAGE"
direnv allow "$DEPLOYMENT_DIR"
cd "$DEPLOYMENT_DIR"
}
```

Wait for the next prompt: the selector loads the recorded deployment and chart path. The service defaults to native 17.0.1 and a hostname derived from `dns.root`. Set optional hostname, native version, claim, SSH/TLS or dependency choices before rendering:

```bash
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
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

The chart applies only this deployment's static ingressEnabled, native version and hostname overrides. Runtime intent belongs solely to `ForgejoApplication.spec.running`; service values, top-level values and deployment overrides cannot set running or replicas. SQLite uses one process when running, with no independent replica control. Selecting another target never imports source runtime intent; standalone charts without a deployment selector still accept `--set-string instance="$INSTANCE"`.

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
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
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
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
```

Put reader/writer directories under INSTALL_DIR/repository/, each containing password (shared repository encryption key), username, restPassword and optional ca.crt. Restore the retained inputs for existing history; never regenerate existing-history keys. Reader may manage native locks but not backup contents; writer is append-only. Admin/retention/replication authority stays with the backup service.

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

First operate the selected providers through their own release READMEs. Do not create their deployments, credentials or storage here. Consume current declarations from the selected provider releases, not guessed endpoints or provider CRs.

### Compose with an existing Keycloak

For Keycloak instead of direct oidc.issuer, select its retained deployment and declaration file:

```bash
export KC_DEPLOYMENT=keycloak
export KC_CONNECTIONS="$WORK_DIR/clusters/cluster-1/applications/keycloak/connections.yaml"
KC_INSTANCE=$(yq -er '.deployments[strenv(KC_DEPLOYMENT)].instance' "$VALUES")
export KC_INSTANCE
yq -i '.services[strenv(INSTANCE)].identityProvider = strenv(KC_INSTANCE)' "$VALUES"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
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

### Provision the native Keycloak client

The provider binding must match identityProvider. Use a current authorized master-realm administrator with admin-cli password grant and realm/client creation/read/secret access. Supply its files under protected/keycloak-admin. It is preparation-only, never a Forgejo runtime credential.

If this provider's HTTPS endpoint needs a supplied private/staging root, select its complete trusted PEM bundle for native curl in this shell before the commands below:

```bash
export CURL_CA_BUNDLE=/absolute/path/to/supplied/operator-ca-bundle.pem
```

Otherwise keep curl's default trust. This does not configure Forgejo's runtime trust; supply that separately under [custom client CA trust](#custom-client-ca-trust). Never disable TLS verification. Inspect the realm and declared client:

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

Configure users and MFA through native Keycloak administration. Provider registration does not automatically link native Forgejo accounts. Continue with the optional Restic composition if selected; otherwise proceed to [native identity and configuration](#prepare-native-identity-and-configuration).

### Compose with an existing Restic

Starting condition: no direct repository override, scoped inputs already supplied through [Restic's handoff](https://github.com/bartlapp/bartl-restic/blob/main/README.md#connect-an-application), and a dedicated admitted repository. Select the provider's retained deployment and current declarations:

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
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
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

Preserve keys and transport passwords. Refresh derived bindings after provider choices change; declarations neither create a repository nor prove protection. Continue with [native identity and configuration](#prepare-native-identity-and-configuration); backup-only composition does not require Keycloak administration.

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

### Custom client CA trust

Leave this unset for ordinary production trust. For a private CA or an explicitly isolated staging-ACME test, supply a PEM bundle containing the intended public roots and additional CA certificates. This is outbound Forgejo client trust, not an ingress certificate, an OIDC identity or a backup member. Retain the supplied bundle and selection independently under `protected/client-ca/`; replacement and reinstall recreate the same configuration Secret from those files. Do not put staging roots into normal production inputs.

With the newly built candidate image selected, or historical 0.2.3 when following its matching released procedure, select the supplied bundle once:

```bash
export CLIENT_CA_BUNDLE=/absolute/path/to/supplied/ca-bundle.pem
mkdir -p "$INSTALL_DIR/protected/client-ca"
cp "$CLIENT_CA_BUNDLE" "$INSTALL_DIR/protected/client-ca/ca-bundle.pem"
printf '%s' /configuration/ca-bundle.pem > "$INSTALL_DIR/protected/client-ca/SSL_CERT_FILE"
```

The existing configuration Secret mounts these files read-only at `/configuration`. Its optional `SSL_CERT_FILE` key becomes the native process environment variable, including for `kubectl exec` native `admin auth` commands. The worker explicitly preserves it for Forgejo web/preparation processes and rejects empty, unreadable or malformed supplied bundles before state mutation. An absent key retains the native default environment. Never bypass an untrusted issuer or a wrong TLS hostname.

For [Forgejo 16.0.7](https://codeberg.org/forgejo/forgejo/src/tag/v16.0.7/services/auth/source/oauth2/providers_openid.go) and [17.0.1](https://codeberg.org/forgejo/forgejo/src/tag/v17.0.1/services/auth/source/oauth2/providers_openid.go), OIDC uses Goth's `openidConnect.New`, not a Forgejo `ROOT_CA_CERTS` app.ini setting. Both pin [Goth 1.82.0](https://github.com/markbates/goth/blob/v1.82.0/providers/openidConnect/openidConnect.go), whose discovery, token and userinfo clients use Go TLS; on Linux, [Go's native root loader](https://github.com/golang/go/blob/go1.26.10/src/crypto/x509/root_unix.go) reads `SSL_CERT_FILE`. Certificate and hostname verification remain enabled. This does not configure browser, workstation, Git or Restic trust.

After changing or removing the supplied trust, recreate the configuration Secret through the following recipe, then run `kubectl --context "$KUBE_CONTEXT" -n "$NS" rollout restart deployment/forgejo` and `kubectl --context "$KUBE_CONTEXT" -n "$NS" rollout status deployment/forgejo --timeout=5m`. Do not mutate only a mounted file and assume the running Go process reloaded cached roots. Remove both retained client-CA files to return to default trust, not just the PEM file. Verify a fresh real-browser OIDC login through discovery, token exchange and userinfo; successful startup or auth-source registration alone is insufficient.

### Render configuration

Starting condition: the selected chart and current declarations are available, together with the deployment's retained native identity and any client-CA inputs. For an existing deployment, [prepare the chart-backed operation](#prepare-a-chart-backed-operation) first. Render native configuration and append retained identity keys without printing protected content; this procedure does not generate identity or bootstrap credentials:

```bash
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
  --set-string renderMode=configuration --show-only templates/configuration.yaml > "$INSTALL_DIR/forgejo-configuration.yaml"
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
yq -er '.configuration[strenv(INSTANCE)].appIni' "$CONFIGURATION" > "$INSTALL_DIR/protected/app.ini"
{
  printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/SECRET_KEY")" "$(cat "$INSTALL_DIR/protected/identity/INTERNAL_TOKEN")"
  printf '\n[server]\nLFS_JWT_SECRET = %s\n' "$(cat "$INSTALL_DIR/protected/identity/LFS_JWT_SECRET")"
} >> "$INSTALL_DIR/protected/app.ini"
CONFIG_SECRET=$(yq -er '.configuration[strenv(INSTANCE)].secretName' "$CONFIGURATION")
mkdir -p "$INSTALL_DIR/protected/client-ca"
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$CONFIG_SECRET" \
  --from-file="app.ini=$INSTALL_DIR/protected/app.ini" \
  --from-file="$INSTALL_DIR/protected/client-ca/" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply --server-side --field-manager=forgejo-preparation -f -
```

Rendering starts from the chart template, not an old app.ini. It supplies native paths, URLs/ports and disabled-effect settings. Retain configuration/identity independently before installation. Backup captures native state and identity/version/integrity metadata; target configuration, TLS and repository credentials remain separate recovery inputs.

Retain `protected/identity/` and `repository/{reader,writer}` with the logical instance across replacements. Preserve supplied `protected/client-ca/` inputs initially; the target deployment's approved trust policy may differ without changing instance identity. Bootstrap credentials are for first initialization, not resetting restored users. Retain the OIDC client secret and provider binding independently; Keycloak administrator credentials are preparation-only. `protected/app.ini` is regenerated from retained configuration and identity. Externally supplied `tls/` files belong to the target deployment. Keep all plaintext inputs outside Git.

### Retain independently usable inputs

| Files relative to `INSTALL_DIR` | Scope and lifetime |
| --- | --- |
| `protected/identity/{SECRET_KEY,INTERNAL_TOKEN,LFS_JWT_SECRET}` | Logical instance; preserve unchanged across replacement/recovery. |
| `protected/client-ca/{ca-bundle.pem,SSL_CERT_FILE}` | Optional deployment-specific native client trust; preserve supplied inputs initially, then change only under the target's approved trust policy. Both files are supplied together. |
| `repository/{reader,writer}/{password,username,restPassword,ca.crt}` | Selected repository's encryption key and scoped transport access; CA optional. A secondary has its own credentials and point identity. |
| `protected/oidc-client-secret` and retained provider/client binding | Logical-instance login dependency; preserve with restored native auth records. |
| `protected/bootstrap/{username,password,email}` | First initialization only; never a restored-user reset mechanism. |
| `protected/keycloak-admin/{username,password}` | Optional client preparation only; never Forgejo runtime or recovery credentials. |
| `protected/app.ini` | Regenerated deployment configuration containing retained identity; protect its bytes, but do not reuse source URLs blindly. |
| `tls/{fullchain.pem,key.pem}` | External deployment certificate/key only; managed TLS is issued on target. |

Keep independently decryptable custody of the required instance inputs, static installation record, exact artifacts, cluster access and authorized DNS/routing access outside the covered failure and administrative domain. Use the installation's authorized encrypted store or offline medium; no company tooling or `pass` is required. Record how a successor retrieves these files to the exact paths above without relying on this workstation, cluster or Keycloak. Before handover, have that successor retrieve inputs without displaying values, restore a retained point with its reader/key, and verify useful HTTP/SSH behavior plus new work and a subsequent usable point. Chart caches, temporary Keycloak token/client work directories, bootstrap-only inputs and generated configuration are not substitutes for retained keys/history.

For installation-wide custody, keep repository encryption keys and transport roles distinct from the backup service's administrator/retention authority. Only copy the instance inputs needed by a replacement; do not copy preparation administrator or first-initialization credentials into every target.

## Install and observe

### Create the application once

After claims and Secrets are ready, install the CRD and create the full native application before installing the release. This initializes runtime intent once and supplies Helm ownership metadata. Never apply this creation manifest to an existing resource; ordinary operation patches the live CR instead. Do not adopt an older API or management resource.

```bash
helm show crds "$CHART" |
  kubectl --context "$KUBE_CONTEXT" apply --server-side --field-manager=forgejo-crd -f -
kubectl --context "$KUBE_CONTEXT" wait crd/forgejoapplications.forgejo.bartl.sh \
  --for=condition=Established --timeout=1m
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --show-only templates/application.yaml |
  RELEASE="$RELEASE" NS="$NS" yq '
    .metadata.labels."app.kubernetes.io/managed-by" = "Helm" |
    .metadata.annotations."meta.helm.sh/release-name" = strenv(RELEASE) |
    .metadata.annotations."meta.helm.sh/release-namespace" = strenv(NS) |
    .spec.running = true' |
  kubectl --context "$KUBE_CONTEXT" create -f -
```

The controller is installed next. Helm renders static fields, never `running`. Explicit true is new target intent, not inherited source state. If creation succeeded but installation failed, inspect that CR and release before continuing; do not replay `create`.

### Install the release

```bash
helm install "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
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

Require current-generation Ready and the intended native version, trusted HTTPS and actual SSH reachability. Verify the installation's agreed useful behavior through its real endpoints: authenticated clone/push and the LFS, collaboration, artifact and permission features actually used. Check retained SSH host identity. LFS returns absolute object URLs, so use the real configured hostname, not client-side URL rewriting. The broad synthetic feature matrix belongs to package qualification; a successful Helm invocation alone is not acceptance.

### Register Forgejo's native authentication source

For selected OIDC, configuration rendering declares native OAuth arguments, discovery URL and callback but does not mutate Forgejo's database. After successful installation, read those declarations and inspect native auth sources. When returning in another session, load retained context and [prepare the chart-backed operation](#prepare-a-chart-backed-operation) first:

The commands run in the serving container, so they inherit its optional native `SSL_CERT_FILE` environment without a second CA flag. Use the ready, restarted Pod after trust changes; do not use an old Pod or a workstation binary with different roots.

```bash
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
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

### Load retained context

In direnv-hooked Bash, enter the actual retained deployment directory; a replacement uses its nested `deployments/<deployment>` directory.

```bash
cd "${WORK_DIR_ROOT:-$HOME/bartl}/acme/acme-installation/clusters/cluster-1/applications/forgejo"
```

At the next prompt, the selector has loaded this deployment's recorded cluster access, namespace, release and installation paths:

```bash
set -euo pipefail
umask 077
```

Context alone is sufficient for status, stop/start, capture/publication retry and native restart. These operations neither download a chart nor render declarations or recreate credentials.

### Inspect an installed deployment

For status inspection of an application expected to exist:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o yaml
```

### Prepare a chart-backed operation

Starting condition: retained context loaded. Use this preparation only before a Helm operation or configuration/connection rendering. Reacquire the recorded chart when absent and refresh declarations; restore protected inputs only when the selected operation needs them. No running application is required:

```bash
if [ ! -d "$CHART" ]; then
  mkdir -p "$WORK_DIR/packages/$PACKAGE"
  CHART_STAGE=$(mktemp -d "$WORK_DIR/packages/.forgejo.XXXXXX")
  helm pull "$CHART_REF" --version "$PACKAGE_VERSION" --untar --untardir "$CHART_STAGE"
  mv "$CHART_STAGE/bartl-forgejo" "$CHART"
  rm -r "$CHART_STAGE"
fi
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
```

Do not generate identity/bootstrap keys, recreate claims or re-register OIDC to resume. For native auth inspection after resumption, render nonsecret configuration from the same selected chart/deployment first; it does not change an authentication source.

Continue with the selected operation.

## Capture and retry publication

Load [retained context](#load-retained-context) only; no chart preparation is needed. Start from a deployment with backup configured. Complete pending publication with its original state/staging claims and native version before changing those inputs or uninstalling. A new capture may use running or already-prepared stopped state; it never initializes empty stopped state merely to make a backup. The controller owns drainage, stopped copy, permitted resumption and publication. For final writes before replacement or removal use [atomic capture-and-stop](#capture-final-writes-before-source-stop).

```bash
REQUEST="manual-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 8)"
kubectl --context "$KUBE_CONTEXT" -n "$NS" annotate forgejoapplication/forgejo \
  "forgejo.bartl.sh/backup-request=$REQUEST" --overwrite
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.backup.request}=$REQUEST" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for=jsonpath='{.status.backup.success}'=true --timeout=30m -o json > "$INSTALL_DIR/backup-result.json"
jq -e --arg request "$REQUEST" '.status.backup |
  .request == $request and .success == true and (.snapshot | test("^[a-f0-9]{64}$"))' "$INSTALL_DIR/backup-result.json"
```

The receipt is the object that satisfied the success wait, not a later read of mutable backup status. Keep the request-ID check so a different capture cannot be mistaken for this request.

Require this request's success and full snapshot identity. When only publication fails, BackupFailed retains complete staging separately from application health. Correct publication inputs and issue another unique request using the same annotation and waits above, including while `spec.running` is false. Retry publishes retained capture without reopening writers; its original boundary does not advance. Request a subsequent fresh capture for later writes. Never delete staging or change state/staging/version while publication is pending. Capture failure is not a usable point and does not itself establish source damage; explicit start remains available for a proven-intact source once execution has ended. Uncertain state or incomplete mutation still refuses start.

For a failed read-only capture, inspect its bound result before deciding whether to resume the source:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json |
  jq -e '.status.operation |
    .kind == "capture" and .phase == "Failed" and .sourceIntact == true'
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods,jobs
```

Require no executing serving/native-worker Pods before an explicit [Start](#start). `sourceIntact` is established by the worker only after graceful shutdown, intact native state and matching version; it is not permission to resume uncertain mutation. Keep failed diagnostics and do not clear markers. Starting does not turn the failed capture into a usable point.

After correcting the capture failure, submit another unique request through the ordinary capture interface above, including while stopped. No chart preparation, claim replacement, source reconstruction or marker removal is required for an owned incomplete capture. Under source and staging locks, the worker independently verifies the stopped source and reclaims only its own uncommitted partial capture using the existing ownership manifest.

Unknown or foreign partial staging remains refused; retain its diagnostics and resolve ownership rather than deleting markers. A complete protected capture is never reclaimed by this path: publication retry reuses that capture on its original claim and does not advance its boundary. Neither retry nor a stale result overrides stopped runtime intent.

Monitor absent/stale/failed scheduled backups, staging capacity and required secondary protection. The CronJob submits this same request interface. Native locking remains enabled; never blindly unlock. Damaged latest history fails visibly rather than selecting an older point or initializing.

## Apply static configuration

Starting condition: publication complete and no native work executing. Edit intended retained static configuration, then [prepare the chart-backed operation](#prepare-a-chart-backed-operation). Apply scheduling changes without altering native version, hostname or routing intent. Helm must preserve live runtime choice, including deliberate stop:

```bash
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o yaml
```

Native version and endpoint changes retain their dedicated procedures below. Applying configuration does not repair failed state or finish pending publication.

## Stop and start

### Stop

Load [retained context](#load-retained-context) only; no chart preparation is needed. Ordinary stop is allowed during pending/failed publication. Export needed diagnostics and persist native intent; do not scale generated Deployments:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" patch forgejoapplication/forgejo \
  --type=merge -p '{"spec":{"running":false}}'
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Stopped --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Stopped" and .status == "True" and .observedGeneration == $g)'
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods,jobs,deployments
```

Require confirmed drainage. Serving can continue through API/controller observation loss; a stop request or unreachable source is not retirement.

### Start

After an intact stop, or a failed read-only capture with the [established intact-source result](#capture-and-retry-publication) and no executing workers, load [retained context](#load-retained-context) only and explicitly start the same deployment:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" patch forgejoapplication/forgejo \
  --type=merge -p '{"spec":{"running":true}}'
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
```

Verify useful HTTP/SSH behavior without backup access. Failed initialization, reconstruction or migration remains failed across runtime intent; never clear markers to restart uncertain state.

### Restart

Load [retained context](#load-retained-context) only. For an ordinary restart of a running deployment with no executing capture or failed transition:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" rollout restart deployment/forgejo
kubectl --context "$KUBE_CONTEXT" -n "$NS" rollout status deployment/forgejo --timeout=10m
```

Verify the installation's useful HTTP/SSH behavior. Retained-state restart does not require repository access or OIDC re-registration.

## Replacement upgrade and promotion

Use this sequence for same-release reconstruction or supported native 16.0.7 → 17.0.1 replacement. Supply the verified package, independent empty state/staging claims, retained native identity, usable repository/reader access, and an authorized final point. Source and target share VALUES and logical instance but have separate deployment intent and storage.

Arrange the interruption/data-loss boundary first. The environment must restrict target **HTTP and SSH** to acceptance operators without alternate-route bypass. No production effects or ordinary writes are allowed during validation. This package has no cross-provider fencing; if predecessor authority cannot be withdrawn, do not promote. DNS changes alone are insufficient.

Before interrupting the source, obtain the explicitly selected replacement candidate chart after real build/publication. Reuse a matching package entry; select an unused key only for a different artifact. This does not change the source package or native version:

```bash
export TARGET_PACKAGE=forgejo-replacement
mkdir -p "$WORK_DIR/packages"
TARGET_STAGE=$(mktemp -d "$WORK_DIR/packages/.forgejo.XXXXXX")
helm pull oci://ghcr.io/bartlapp/bartl-forgejo --version 0.3.0-dev.3 --untar --untardir "$TARGET_STAGE"
export TARGET_PACKAGE_VERSION
TARGET_PACKAGE_VERSION=$(helm show chart "$TARGET_STAGE/bartl-forgejo" | yq -er '.version')
MATCHING_PACKAGE=$(yq -o=json '.packages // {}' "$VALUES" |
  jq -r --arg version "$TARGET_PACKAGE_VERSION" '[to_entries[] |
    select(.value.chart == "oci://ghcr.io/bartlapp/bartl-forgejo" and .value.version == $version) | .key][0] // empty')
if [ -n "$MATCHING_PACKAGE" ]; then TARGET_PACKAGE="$MATCHING_PACKAGE"; fi
```

### Capture final writes before source stop

Load the reachable source's retained context and [prepare the chart-backed operation](#prepare-a-chart-backed-operation) for this deliberate scheduling/routing change. Withdraw external HTTP/API and **direct SSH** through enforced routing/firewall controls, drain clients and prevent concurrent operators/scheduled captures. Keep both protocols closed through capture and publication retry. Disable ingress and capture scheduling, preserving its prior schedule:

```bash
export SOURCE_DEPLOYMENT="$DEPLOYMENT"
export SOURCE_DIR="$INSTALL_DIR"
export SOURCE_HOST="$FORGEJO_HOST"
if [ ! -e "$SOURCE_DIR/backup-schedule-before-operation" ]; then
  (set -o noclobber; yq -r '.services[strenv(INSTANCE)].backup.schedule // ""' "$VALUES" \
    > "$SOURCE_DIR/backup-schedule-before-operation")
fi
if [ ! -e "$SOURCE_DIR/ingress-before-operation.json" ]; then
  (set -o noclobber
    yq -o=json '.operations.deployments[strenv(DEPLOYMENT)] // {}' "$VALUES" |
      jq '{ingressEnabled} | with_entries(select(.value != null))' \
        > "$SOURCE_DIR/ingress-before-operation.json")
fi
BACKUP_SCHEDULE=$(cat "$SOURCE_DIR/backup-schedule-before-operation")
export BACKUP_SCHEDULE
yq -i '.operations.deployments[strenv(DEPLOYMENT)].ingressEnabled = false |
  .services[strenv(INSTANCE)].backup.schedule = ""' "$VALUES"
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o yaml
```

Let admitted work finish. If publication is pending/failed, complete it through [Capture and retry publication](#capture-and-retry-publication) with original claims/version while intake stays closed. That publishes retained capture, not later writes. Only after pending publication completes, request fresh final capture and durable stop in **one** merge patch:

```bash
REQUEST="final-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 8)"
kubectl --context "$KUBE_CONTEXT" -n "$NS" patch forgejoapplication/forgejo --type=merge \
  -p "$(jq -nc --arg request "$REQUEST" \
    '{metadata:{annotations:{"forgejo.bartl.sh/backup-request":$request}},spec:{running:false}}')"
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.backup.request}=$REQUEST" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for=jsonpath='{.status.backup.success}'=true --timeout=30m -o json > "$INSTALL_DIR/backup-result.json"
jq -e --arg request "$REQUEST" '.status.backup |
  .request == $request and .success == true and (.snapshot | test("^[a-f0-9]{64}$"))' "$INSTALL_DIR/backup-result.json"
```

Require successful publication of the fresh final capture, inclusion of final writes and required secondary protection. The atomic request prevents writer restart between capture and stop. Failure postpones replacement; retry publication through the ordinary interface while keeping running false and original claims/version. Then verify drainage:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Stopped --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Stopped" and .status == "True" and .observedGeneration == $g)'
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pods,jobs
kubectl --context "$KUBE_CONTEXT" -n "$NS" get pvc -o json > "$SOURCE_DIR/source-pvc.json"
```

Prevent source restart and direct volume writes. Keep its release, CR, claims and Secrets stopped and retained through target acceptance and activation. The stopped controller removes its managed HTTP ingress; the static SSH Service remains but must have no serving endpoints. Keep enforced external restrictions on both protocols. Finalization belongs after activation, so an abandoned switch can explicitly resume an intact source.

For an already retired/lost source, skip source API calls and select `SOURCE_DEPLOYMENT`, `SOURCE_DIR` and `SOURCE_HOST` from independently retained configuration/declarations. Save the original schedule in `"$SOURCE_DIR/backup-schedule-before-operation"` using the save-if-absent block above before target preparation clears it. Preserve that file across retries/shell changes. Independent enforced retirement and usable final history remain prerequisites.

### Prepare the replacement

Choose an unused target deployment, an existing supplied cluster and a fresh namespace. TARGET_VERSION is the genuine choice: retain the source version for same-release recovery or select 17.0.1 for the supported forward transition. Retain a separate package key if adopting another chart; never overwrite the source package selection.

```bash
{
export DEPLOYMENT=forgejo-replacement
export TARGET_CLUSTER=cluster-2
export TARGET_NAMESPACE=forgejo-green
export TARGET_VERSION=17.0.1
export SOURCE_DEPLOYMENT SOURCE_DIR SOURCE_HOST
INSTANCE=$(yq -er '.deployments[strenv(SOURCE_DEPLOYMENT)].instance' "$VALUES")
export INSTANCE
yq -e '.packages[strenv(TARGET_PACKAGE)] == null or
  (.packages[strenv(TARGET_PACKAGE)].version == strenv(TARGET_PACKAGE_VERSION) and
   .packages[strenv(TARGET_PACKAGE)].chart == "oci://ghcr.io/bartlapp/bartl-forgejo")' "$VALUES" >/dev/null
yq -i '.deployments[strenv(DEPLOYMENT)] = {
  "instance": strenv(INSTANCE), "cluster": strenv(TARGET_CLUSTER),
  "namespace": strenv(TARGET_NAMESPACE), "release": "forgejo", "package": strenv(TARGET_PACKAGE)
} | .packages[strenv(TARGET_PACKAGE)] = (.packages[strenv(TARGET_PACKAGE)] // {
  "version": strenv(TARGET_PACKAGE_VERSION), "chart": "oci://ghcr.io/bartlapp/bartl-forgejo"
}) | .operations.deployments[strenv(DEPLOYMENT)] = {
  "ingressEnabled": true, "version": strenv(TARGET_VERSION), "hostname": strenv(SOURCE_HOST)
} | .services[strenv(INSTANCE)].backup.schedule = ""' "$VALUES"
TARGET_DIR="$WORK_DIR/clusters/$TARGET_CLUSTER/applications/$INSTANCE/deployments/$DEPLOYMENT"
mkdir -p "$TARGET_DIR" "$WORK_DIR/packages/$TARGET_PACKAGE"
(
  set -o noclobber
  printf 'source_env ../../../../.envrc\nload_deployment "%s"\n' "$DEPLOYMENT" > "$TARGET_DIR/.envrc"
)
if [ ! -d "$WORK_DIR/packages/$TARGET_PACKAGE/$TARGET_PACKAGE_VERSION" ]; then
  mv "$TARGET_STAGE/bartl-forgejo" "$WORK_DIR/packages/$TARGET_PACKAGE/$TARGET_PACKAGE_VERSION"
fi
rm -r "$TARGET_STAGE"
direnv allow "$TARGET_DIR"
cd "$TARGET_DIR"
}
```

Wait for the next prompt. Keep the source-input directory and saved backup schedule available until activation succeeds. The selected deployment supplies the target's native-version and hostname intent without reusing source settings:

```bash
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
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
mkdir -p "$INSTALL_DIR/protected"
cp -R "$SOURCE_DIR/protected/identity" "$INSTALL_DIR/protected/identity"
if [ -d "$SOURCE_DIR/protected/client-ca" ]; then
  cp -R "$SOURCE_DIR/protected/client-ca" "$INSTALL_DIR/protected/client-ca"
fi
if [ -f "$SOURCE_DIR/protected/oidc-client-secret" ]; then
  cp "$SOURCE_DIR/protected/oidc-client-secret" "$INSTALL_DIR/protected/oidc-client-secret"
fi
cp -R "$SOURCE_DIR/repository" "$INSTALL_DIR/repository"
```

Restore the original native identity, not fresh keys or source-mounted state. For secondary recovery, select its surviving repository URL and supply that repository's own reader/writer files under target repository/; use its destination snapshot identity, not assumed primary IDs. Preserve any composed binding form instead of introducing a conflicting direct URL. Native auth records come from capture; retain the matching provider/client independently.

Refresh declarations and native configuration after the selected repository/endpoint choices:

```bash
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
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
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
  --set-string renderMode=configuration --show-only templates/configuration.yaml > "$INSTALL_DIR/forgejo-configuration.yaml"
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
yq -er '.configuration[strenv(INSTANCE)].appIni' "$CONFIGURATION" > "$INSTALL_DIR/protected/app.ini"
{
  printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/SECRET_KEY")" "$(cat "$INSTALL_DIR/protected/identity/INTERNAL_TOKEN")"
  printf '\n[server]\nLFS_JWT_SECRET = %s\n' "$(cat "$INSTALL_DIR/protected/identity/LFS_JWT_SECRET")"
} >> "$INSTALL_DIR/protected/app.ini"
CONFIG_SECRET=$(yq -er '.configuration[strenv(INSTANCE)].secretName' "$CONFIGURATION")
mkdir -p "$INSTALL_DIR/protected/client-ca"
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$CONFIG_SECRET" \
  --from-file="app.ini=$INSTALL_DIR/protected/app.ini" \
  --from-file="$INSTALL_DIR/protected/client-ca/" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply --server-side --field-manager=forgejo-preparation -f -
```

For external TLS only, supply the target certificate/key and retain external ownership:

```bash
TLS_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret tls "$TLS_SECRET" \
  --cert="$INSTALL_DIR/tls/fullchain.pem" --key="$INSTALL_DIR/tls/key.pem"
```

Source durable stop and target HTTP/SSH acceptance restrictions must now be enforced. For slots sharing a cluster/node, supply nonconflicting SSH LoadBalancer/NodePort assignments before installing; do not delete the retained source to free a port. Point authorized hostnames to target ingress/SSH and confirm authoritative/public answers before managed certificates:

```bash
kubectl --context "$KUBE_CONTEXT" -n kube-system get service traefik -o wide
printf 'HTTPS: %s; SSH: %s:%s\n' "$FORGEJO_HOST" "$SSH_HOST" "$SSH_PORT"
dig "$FORGEJO_HOST" A +trace
dig @1.1.1.1 +short "$FORGEJO_HOST" A
dig @8.8.8.8 +short "$FORGEJO_HOST" A
```

### Reconstruct and accept

Run [Create the application once](#create-the-application-once) in the selected target context. This explicitly selects new running intent without inspecting or adopting a predecessor. Then install:

```bash
helm install "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
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

Through restricted real HTTP/SSH, verify the selected point's expected repositories/content, grants, SSH host identity and the installation's agreed useful behavior. Check ordinary sources are denied. Verify fresh OIDC login if selected without rewriting restored auth sources. Push permitted test work, verify its contents and publish another usable recovery point:

```bash
REQUEST="manual-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 8)"
kubectl --context "$KUBE_CONTEXT" -n "$NS" annotate forgejoapplication/forgejo \
  "forgejo.bartl.sh/backup-request=$REQUEST" --overwrite
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for="jsonpath={.status.backup.request}=$REQUEST" --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo \
  --for=jsonpath='{.status.backup.success}'=true --timeout=30m -o json > "$INSTALL_DIR/backup-result.json"
jq -e --arg request "$REQUEST" '.status.backup |
  .request == $request and .success == true and (.snapshot | test("^[a-f0-9]{64}$"))' "$INSTALL_DIR/backup-result.json"
```

Require this capture to include the new work. The maintainer's exhaustive synthetic feature fixtures are not required for each recovery; useful restored behavior, new work and a subsequent usable backup are.

The replacement is now accepted under restricted HTTP/SSH access with a successful subsequent backup. In a composed installation, pause here until cross-service acceptance is complete; keep the source retired and target access restricted.

### Activate the replacement

After standalone acceptance or the coordinator's activation decision, restore the approved capture schedule and remove environment restrictions to activate production HTTP and SSH:

```bash
BACKUP_SCHEDULE=$(cat "$SOURCE_DIR/backup-schedule-before-operation")
export BACKUP_SCHEDULE
yq -i '.services[strenv(INSTANCE)].backup.schedule = strenv(BACKUP_SCHEDULE)' "$VALUES"
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
```

Verify ordinary HTTP/SSH access and the restored schedule, then remove `"$SOURCE_DIR/backup-schedule-before-operation"`. In another shell, select the same source-input directory before activation; never restore the temporarily empty schedule as the original. Keep active deployment, accepted final-write boundary and selected recovery point in the installation record, not a success diary. Keep source stopped/retained until disposition is authorized. If target writes occurred, never automatically reactivate the predecessor; establish authoritative state and reconcile explicitly. Failed/ambiguous mutation remains stopped: retain diagnostics and reconstruct on another independent empty target, not by deleting markers.

An earlier isolated rehearsal may use a deployment-local candidate hostname and restricted real HTTP/SSH, but must not publish competing history or cause production effects. It does not include later source writes. After the final source capture, reconstruct a fresh target through this sequence; do not silently promote the earlier rehearsal.

### Abandon before activation

Only if the target accepted no production work or caused production effects, [Stop](#stop) it and confirm current-generation Stopped with no serving/native-worker Pods. Keep target HTTP/SSH restrictions enforced. Target publication into the production repository is an effect: after the subsequent acceptance backup, resolve authoritative recovery history before considering source resumption. Never publish competing history from an abandoned isolated target. If production effects occurred, determine authoritative state and reconcile; do not simply restart the predecessor.

Enter the retained source deployment directory (the original `SOURCE_DIR`) and wait for the next direnv prompt. Confirm source state intact, no mutation unresolved and target unable to accept work. Then restore its saved schedule and static ingress override while it remains stopped:

```bash
cd "$SOURCE_DIR"
```

At the next prompt, [prepare the source chart-backed operation](#prepare-a-chart-backed-operation), then:

```bash
BACKUP_SCHEDULE=$(cat "$INSTALL_DIR/backup-schedule-before-operation")
SOURCE_INGRESS=$(cat "$INSTALL_DIR/ingress-before-operation.json")
export BACKUP_SCHEDULE SOURCE_INGRESS
yq -i '.services[strenv(INSTANCE)].backup.schedule = strenv(BACKUP_SCHEDULE) |
  .operations.deployments[strenv(DEPLOYMENT)] =
    ((.operations.deployments[strenv(DEPLOYMENT)] | del(.ingressEnabled)) * env(SOURCE_INGRESS))' "$VALUES"
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json |
  jq -e '.spec.running == false'
```

Explicitly [Start](#start) the source. Verify useful HTTP/SSH behavior before restoring production DNS/routes to the source and reopening access. Remove the saved schedule and ingress files only after this succeeds.

After successful target activation and authorized retention/data disposition, select the stopped source context and follow [Remove the release](#remove-the-release). Confirm its HTTP route and SSH Service are gone. Retain claims and supplied Secrets until separately authorized disposition.

## Change the selected endpoint

Starting condition: retained context loaded, [chart-backed operation prepared](#prepare-a-chart-backed-operation), approved endpoint change, current publication complete and access restricted during the change. Set the one new hostname; retain existing identity. Prepare its authorized DNS, matching external certificate if used, and provider callbacks before applying the change below. Managed issuance needs public port 80. Update any separately overridden ssh.hostname deliberately. If removing an override, delete that field to restore package derivation rather than recording the derived hostname as a permanent choice.

```bash
export NEW_HOST=forgejo.acme.fke.systems
yq -i '.operations.deployments[strenv(DEPLOYMENT)].hostname = strenv(NEW_HOST)' "$VALUES"
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
  --set-string renderMode=connections --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
SSH_HOST=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.hostname' "$CONNECTIONS")
SSH_PORT=$(yq -er '.services[strenv(INSTANCE)].endpoints.ssh.port' "$CONNECTIONS")
helm template "$RELEASE" "$CHART" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" \
  --set-string renderMode=configuration --show-only templates/configuration.yaml > "$INSTALL_DIR/forgejo-configuration.yaml"
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
yq -er '.configuration[strenv(INSTANCE)].appIni' "$CONFIGURATION" > "$INSTALL_DIR/protected/app.ini"
{
  printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/SECRET_KEY")" "$(cat "$INSTALL_DIR/protected/identity/INTERNAL_TOKEN")"
  printf '\n[server]\nLFS_JWT_SECRET = %s\n' "$(cat "$INSTALL_DIR/protected/identity/LFS_JWT_SECRET")"
} >> "$INSTALL_DIR/protected/app.ini"
CONFIG_SECRET=$(yq -er '.configuration[strenv(INSTANCE)].secretName' "$CONFIGURATION")
mkdir -p "$INSTALL_DIR/protected/client-ca"
kubectl --context "$KUBE_CONTEXT" -n "$NS" create secret generic "$CONFIG_SECRET" \
  --from-file="app.ini=$INSTALL_DIR/protected/app.ini" \
  --from-file="$INSTALL_DIR/protected/client-ca/" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply --server-side --field-manager=forgejo-preparation -f -
helm upgrade "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" --reset-values -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
if yq -e '.services[strenv(INSTANCE)].endpoints.https.tlsManaged' "$CONNECTIONS" >/dev/null; then
  TLS_SECRET=$(yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
  kubectl --context "$KUBE_CONTEXT" -n "$NS" wait "certificate/$TLS_SECRET" --for=condition=Ready --timeout=10m
fi
```

Prepare authorized DNS and a matching external certificate, if used, before applying this change; managed issuance needs public port 80. Verify real HTTPS/SSH and LFS at the newly advertised endpoint and approved OIDC callbacks, then reopen access. Never use URL rewriting as LFS acceptance.

## Removal and retained-state reinstall

### Remove the release

Starting condition: retained context loaded and authorized removal. Close and drain external HTTP and SSH. If final writes must be preserved, complete [atomic final capture and stop](#capture-final-writes-before-source-stop) first; an already-prepared stopped source uses the same request without starting. If publication is pending/failed, retry retained capture while keeping controller, CR, original claims and native version; failed publication postpones planned uninstall, not ordinary stop. Do not change these inputs to escape pending work. Removal needs no chart preparation.

Persist stop, wait for the current generation and inspect drainage:

```bash
kubectl --context "$KUBE_CONTEXT" -n "$NS" patch forgejoapplication/forgejo \
  --type=merge -p '{"spec":{"running":false}}'
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

### Reinstall retained state

For authorized reinstall after successful finalization use the same identity, claims, configuration and supplied Secrets with no competing predecessor or failed marker. First [load retained context](#load-retained-context), then [prepare the chart-backed operation](#prepare-a-chart-backed-operation); do not inspect the deliberately removed application. If configuration was lost, restore retained identity/client-CA inputs and repeat [configuration rendering](#render-configuration), not fresh key/bootstrap generation. Run [Create the application once](#create-the-application-once) to explicitly select the new CR's running intent; do not recreate storage or credentials. Then:

```bash
helm install "$RELEASE" "$CHART" --kube-context "$KUBE_CONTEXT" -n "$NS" -f "$VALUES" \
  --set-string deployment="$DEPLOYMENT" --wait=legacy --timeout=5m
kubectl --context "$KUBE_CONTEXT" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=30m
kubectl --context "$KUBE_CONTEXT" -n "$NS" get forgejoapplication/forgejo -o json | \
  jq -e '.metadata.generation as $g | .status.observedGeneration == $g and
    any(.status.conditions[]; .type == "Ready" and .status == "True" and .observedGeneration == $g)'
```

Verify retained native identity/content, agreed useful behavior and new work, then [capture that work](#capture-and-retry-publication) when backup is configured. Uninstall cannot repair a failed transition or preserve pending publication automatically.

## Handover and reference

Hand over exact artifacts, effective installation/native configuration, native identity, volume inventory, protected keys, repository history, reader/writer/admin role boundaries, HTTPS/SSH routing/host-key records, identity dependencies, monitoring/retention responsibility and these procedures. Recovery inputs must be usable without the lost cluster or its Keycloak.

The chart's [interface schema](chart/interface.schema.json) and [defaults](chart/defaults.yaml) define static service choices. Select `--set-string deployment="$DEPLOYMENT"` to validate release, namespace and package version and apply only its static overrides; direct `instance` selection supports standalone values without deployment records. Runtime running/replicas inputs are rejected: patch `ForgejoApplication.spec.running` instead. Omitted static fields derive/default, maps merge and lists replace. Unknown selected-service fields fail; unrelated installation content is ignored. Public connections expose HTTPS/SSH, OIDC/backup requests, credential requirements/bindings and state/staging claims. Configuration provides native app.ini and optional OAuth arguments; protected identity remains external. Refresh resolved bindings after provider changes.

[Maintainer procedures and historical evidence](AGENTS.md#historical-evidence) retain exact runtime qualification and build/publication instructions. Historical application reconstruction on one node does not qualify this rewritten sequence, provider-loss activation or production-scale interruption limits.

Authored code is Apache-2.0. Forgejo is GPL-3.0-or-later; upstream notices/licenses and corresponding source are retained separately in the combined image. See [NOTICE](NOTICE) and [LICENSE](LICENSE).
