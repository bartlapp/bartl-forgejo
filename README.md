# bartl-forgejo

A Helm operating package for native Forgejo, SQLite and filesystem storage. One namespace-scoped application controller coordinates native startup, planned stopped capture and Restic publication. The administrator chooses the target slot, release and production promotion. There is no custom database migration, schema engine or automatic failover.

## Supported profile and evidence

Linux amd64, Kubernetes 1.30 or later, one application replica, native Forgejo 16.0.7 or 17.0.1, forward replacement upgrade 16.0.7 to 17.0.1. Native initialization/migration runs in an isolated finite prepare Job; ordinary pod restart runs native serve without restore or backup access. Failed or interrupted mutation remains stopped; retry uses an independent empty target, not deletion of failure markers. Actions, mail, mirrors, federation and external webhooks are disabled. This is a planned-interruption profile, not a provider-loss fencing implementation.

Release 0.1.0 was qualified on Hetzner with K3s 1.36.5, Helm 4.3.1, Bartlized Restic 0.2.1 and Keycloak 0.5.1. The retained image and chart passed initialization with and without backup configuration, restart without backup access, independent-state restore and native 16.0.7 to 17.0.1 upgrade. Git over HTTP/SSH, LFS, issues, pull requests, attachments, packages, permissions and SSH identity were checked after reconstruction. Native Keycloak login was also exercised through a real browser over HTTPS.

| Release | SHA256 |
| --- | --- |
| 16.0.7 | `ca8ecb37da9210d5730c878308bf31e25b6b526a7294b263d07f6c8718af07b7` |
| 17.0.1 | `51a8cdf4a1252e79f4b3855d7a7891af6c6635105420768c460e2f0690385d6f` |

Failure qualification covered malformed backup lookup, corrupt selected recovery data, interrupted preparation, stop taking precedence, publication failure/retry, and downgrade refusal without changing native files or recovery inputs. Planned removal completed pending publication before uninstall; retained-state reinstall produced another backup whose newly written Git files, issue and package were recovered independently.

In the final retained-state reinstall check, the subsequent backup recorded 15.651 seconds of observed serving-Pod readiness interruption, with a polling upper bound of 17.087 seconds. This measures the synthetic fixture on the qualified single-node placement, not end-user availability or a production interruption guarantee.

Restic 0.19.1 can report snapshot listing errors on stderr while returning exit status zero and an empty or partial JSON list. The package therefore requires both successful exit and a clean diagnostic channel before interpreting backup absence. It retains native filtering and locking rather than adding another enumeration engine.

### Historical release 0.1.0 artifacts

| Artifact | Immutable identity |
| --- | --- |
| Image | `ghcr.io/bartlapp/bartl-forgejo-controller@sha256:e6b4eae6d8b832d0465fdfd12efae57f0cce1bb7de61d6b26b6d4ff67db7f929` |
| Chart | `oci://ghcr.io/bartlapp/bartl-forgejo@sha256:c9d6e7189d29b7b2923a61c5036e38197005912fdd68c6acec6a78654e297f3c` |
| Chart archive SHA256 | `9cea9c65c1bcd111ea86181103cba22b6e86f36f1b9f0e8778ca3449c256f067` |
| Build source tree | `5093bae8bf1aa30e24f4d9187284aeb2121e3eaf` |
| Build source archive SHA256 | `9dc5553a50b122055110ebb257def54a1d8cb165ee24ae7a62dd0abb1c83b2f1` |

The exact image build tree remains reachable at `refs/evidence/candidate-05-source`. Qualification used `tests/package-proof.mjs`; its final removal continuation binds the preceding fourteen passed cases to the same image and source identities. The image was not rebuilt. The chart was repackaged solely to reference the consistent `bartl-forgejo-controller` image name; installation from its published digest, current-generation readiness and access to restored repository content were then verified on Hetzner. Both packages are public. Anonymous chart download and checksum, image manifest digest and access to every image blob were verified.

The qualified placement was one K3s node with independent per-slot volumes and synthetic data. These results establish application reconstruction and planned replacement, not host/provider-loss recovery or production-scale interruption limits. `tests/native.sh` and `tests/native-fixture.mjs` retain the underlying native recipe.

## Obtain and prepare release 0.2.2

The shared-file chart retains the immutable runtime image above. Historical 0.1.0 evidence does not qualify the new preparation procedure; exact 0.2.2 identities and observed qualification belong in its [release record](https://github.com/bartlapp/bartl-forgejo/releases/tag/v0.2.2). The old flat values interface is not accepted. This release omits disabled backup configuration and empty manual-only schedules, matching the existing CRD.

Use Bash, Helm 4, compatible kubectl, Mike Farah yq v4, jq, OpenSSL, curl and shasum, with shell tracing off. The target needs Linux amd64, enforced NetworkPolicy, Traefik, cert-manager and sufficient state/staging/reconstruction capacity. Compatible external OIDC and HTTPS Restic services suffice; no other Bartl package or company-specific tooling is necessary.

```bash
set -euo pipefail
umask 077
mkdir bartl-forgejo
cd bartl-forgejo
helm pull oci://ghcr.io/bartlapp/bartl-forgejo --version 0.2.2
printf '%s  bartl-forgejo-0.2.2.tgz\n' \
  16405a6d485138d16ebe4afca0c7a3d9a68705194a39ed430311ea16d9bf0664 |
  shasum -a 256 --check -
CHART="$PWD/bartl-forgejo-0.2.2.tgz"
CTX=$(kubectl config current-context)
NS=forgejo-blue
INSTANCE=forgejo
INSTALL_DIR="$PWD/installation"
VALUES="$INSTALL_DIR/installation.yaml"
mkdir -p "$INSTALL_DIR/protected/identity" "$INSTALL_DIR/protected/bootstrap"
kubectl --context "$CTX" get nodes,storageclass
kubectl --context "$CTX" create namespace "$NS" --dry-run=client -o yaml |
  kubectl --context "$CTX" apply -f -
```

Retain the verified archive, image, complete values and protected inputs independently of the target's failure domain. Create `"$VALUES"` with your stable installation identity and real DNS root:

```yaml
installation: customer-installation
dns:
  root: example.org
services:
  forgejo:
    release: "0.2.2"
    bootstrapSecret: forgejo-bootstrap
```

This derives the hostname and selects native 17.0.1 with one replica. `services.<instance>.hostname` overrides derivation; omission restores it. Another logical instance uses its own service key and `--set-string instance=...`; Helm release remains `forgejo`. Keep installation/instance identity across replacement, independently of namespace and package version. Selected service fields are strict; unrelated services and infrastructure are ignored. Use complete reviewed values, not `--reuse-values`.

### Declare storage and connections

Offline declarations use package-owned helpers and work before dependencies exist:

```bash
helm template forgejo "$CHART" -n "$NS" -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string renderMode=connections \
  --show-only templates/connections.yaml > "$INSTALL_DIR/forgejo-connections.yaml"
CONNECTIONS="$INSTALL_DIR/forgejo-connections.yaml"
FORGEJO_URL=$(INSTANCE="$INSTANCE" yq -er '.services[strenv(INSTANCE)].endpoints.https.url' "$CONNECTIONS")
FORGEJO_HOST=$(INSTANCE="$INSTANCE" yq -er '.services[strenv(INSTANCE)].endpoints.https.hostname' "$CONNECTIONS")
```

Public paths are `services.<instance>.endpoints.{https,ssh}`, `oidc.requests.<instance>`, `backup.requests.<instance>`, `credentials.{requirements,bindings}.<instance>` and `storage.claims.<instance>.{state,staging}`. They declare requirements, not already issued credentials, clients or repositories. Rerender after changing choices.

Unreleased source correction: `backup.requests.<instance>.enabled` consistently means eligibility for connection preparation: omission is eligible, explicit `backup.enabled: false` opts out. Conditional reader/writer requirements describe what a selected backup needs, not credentials to create for an unbound application. Standalone deployment without a destination still has no backup; `enabled: true` in service choices requires a destination. Released 0.2.2 declares an omitted choice ineligible; the correction needs a new chart release and is not covered by that release's qualification.

Create two distinct operator-owned filesystem PVCs, writable by UID/GID 1000 with fsGroup 1000, supporting SQLite locks, atomic rename and fsync. State holds repositories, LFS, SQLite, attachments, packages, releases, indexes, queues and logs. Staging holds a complete independent capture plus integrity metadata and failed-publication headroom. These example 20Gi capacities are lab choices, not production sizing:

```bash
for role in state staging; do
  CLAIM=$(INSTANCE="$INSTANCE" ROLE="$role" yq -er \
    '.storage.claims[strenv(INSTANCE)][strenv(ROLE)].claimName' "$CONNECTIONS")
  kubectl --context "$CTX" -n "$NS" apply -f - <<EOF
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

Override names through `services.<instance>.claims.{state,staging}` before rendering. WaitForFirstConsumer claims can remain Pending until scheduling. Concurrent writers are forbidden regardless of access mode. The package never deletes claims or supplied Secrets. The staging copy allows service to resume while Restic publishes and preserves failed publication for retry; it trades copy work and capacity against a longer stopped interval.

### TLS and actual SSH exposure

Point the declared HTTPS hostname to the ingress address, confirm authoritative/public DNS and permit ports 80/443. Default `tls.managed: true` creates namespace-local ACME Issuer/Certificate resources using Traefik; cert-manager owns renewal. `tls.acmeServer` may select staging, which is not browser-trusted. `ingress.enabled: false` withholds HTTP routing without disabling certificate preparation.

For externally managed TLS set `services.<instance>.tls.managed: false`, rerender, then create its declared Secret:

```bash
TLS_SECRET=$(INSTANCE="$INSTANCE" yq -er '.credentials.bindings[strenv(INSTANCE)].tls.secretName' "$CONNECTIONS")
# External TLS mode only:
# kubectl --context "$CTX" -n "$NS" create secret tls "$TLS_SECRET" \
#   --cert=/protected/tls/fullchain.pem --key=/protected/tls/key.pem \
#   --dry-run=client -o yaml | kubectl --context "$CTX" apply -f -
```

Do not silently adopt existing manually managed certificates. An HTTP Ingress does not expose SSH: the chart creates native TCP Service `forgejo-ssh`, default type LoadBalancer on port 2222. The target must provide that LoadBalancer and firewall reachability. K3s ServiceLB requires the port available on the node. Point the declared SSH hostname to that reachable address; use `ssh.hostname` if it differs from HTTP. Alternatively set `ssh.exposure: NodePort` and explicit `ssh.nodePort`, expose it on the node firewall and route DNS there; the generated native configuration advertises that node port. `ClusterIP` requires an independently supplied TCP route to the declared Service/port. Inspect `kubectl -n "$NS" get service forgejo-ssh -o wide`, test actual SSH clone/push and retain its host-key fingerprint before activation.

### Standalone external dependencies

For optional OIDC set `services.<instance>.oidc.issuer` to the compatible provider's supplied issuer. Optional `clientId` and source `name` default to the instance and `keycloak`. Rerender and register exactly the declared `oidc.requests.<instance>.{clientId,redirectUris,webOrigins}` as a confidential authorization-code client, with PKCE S256 and no implicit/password grant. Retain its issued secret as `"$INSTALL_DIR/protected/oidc-client-secret"` without a newline; never regenerate it on replay. Native Forgejo registration follows below. Omit OIDC for local-only access.

For optional backup set `services.<instance>.backup.repository` to an initialized compatible HTTPS Restic repository, such as `rest:https://backup.example/forgejo/`. No `resolved` section is needed. Place retained reader/writer inputs in `protected/backup/reader/` and `writer/`; each contains `password` (the same repository encryption key), `username`, `restPassword` and optional `ca.crt`. Readers need native lock writes/removal but no backup-content mutation; writers must be append-only. Keep initialization, retention, destructive maintenance, monitoring and replication authority separate. Never regenerate a key for an existing repository.

After rerendering connections, create the backup Secrets only when backup is selected:

```bash
for role in reader writer; do
  case "$role" in reader) BINDING=backupReader ;; writer) BINDING=backupWriter ;; esac
  SECRET=$(INSTANCE="$INSTANCE" BINDING="$BINDING" yq -er \
    '.credentials.bindings[strenv(INSTANCE)][strenv(BINDING)].secretName' "$CONNECTIONS")
  kubectl --context "$CTX" -n "$NS" create secret generic "$SECRET" \
    --from-file="$INSTALL_DIR/protected/backup/$role/" --dry-run=client -o yaml |
    kubectl --context "$CTX" apply --server-side --field-manager=forgejo-preparation -f -
done
```

Omission means no store; `backup.enabled: false` explicitly disables it. Schedule defaults to empty (manual capture), time zone to UTC; select `backup.schedule`/`timeZone` for scheduled protection. Empty local state initializes when no store or no backup exists; lookup/integrity failure never means absence. Backup access reaches only the Jobs needing it, never the serving process.

### Retained native identity and configuration

For recovery restore the independently retained identity files first: **never generate replacement keys for recovered state**. For genuinely fresh initialization only, create missing values once:

```bash
for key in SECRET_KEY INTERNAL_TOKEN LFS_JWT_SECRET; do
  file="$INSTALL_DIR/protected/identity/$key"
  if [ -e "$file" ]; then test -s "$file"
  else (set -o noclobber; openssl rand -hex 32 | tr -d '\n' > "$file")
  fi
done
```

Place chosen `username`, `email` and a strong retained `password` in `protected/bootstrap/`, each as a separate file. Bootstrap is used for genuinely empty initialization only, not recovered accounts:

```bash
BOOTSTRAP_SECRET=$(INSTANCE="$INSTANCE" yq -er '.credentials.bindings[strenv(INSTANCE)].bootstrap.secretName' "$CONNECTIONS")
kubectl --context "$CTX" -n "$NS" create secret generic "$BOOTSTRAP_SECRET" \
  --from-file="$INSTALL_DIR/protected/bootstrap/" --dry-run=client -o yaml |
  kubectl --context "$CTX" apply -f -
helm template forgejo "$CHART" -n "$NS" -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string renderMode=configuration \
  --show-only templates/configuration.yaml > "$INSTALL_DIR/forgejo-configuration.yaml"
CONFIGURATION="$INSTALL_DIR/forgejo-configuration.yaml"
INSTANCE="$INSTANCE" yq -er '.configuration[strenv(INSTANCE)].appIni' "$CONFIGURATION" \
  > "$INSTALL_DIR/protected/app.ini"
{
  printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/SECRET_KEY")" \
    "$(cat "$INSTALL_DIR/protected/identity/INTERNAL_TOKEN")"
  printf '\n[server]\nLFS_JWT_SECRET = %s\n' \
    "$(cat "$INSTALL_DIR/protected/identity/LFS_JWT_SECRET")"
} >> "$INSTALL_DIR/protected/app.ini"
CONFIG_SECRET=$(INSTANCE="$INSTANCE" yq -er '.configuration[strenv(INSTANCE)].secretName' "$CONFIGURATION")
kubectl --context "$CTX" -n "$NS" create secret generic "$CONFIG_SECRET" \
  --from-file="app.ini=$INSTALL_DIR/protected/app.ini" --dry-run=client -o yaml |
  kubectl --context "$CTX" apply -f -
```

The chart supplies native paths, URLs, ports and disabled-effects settings; no operator-authored `app.ini` machinery is needed. Configuration rendering is offline and nonsecret; selected composed bindings must already match. Regeneration starts from the template and appends retained keys, never accumulates old settings. Keep additional external secret references independently. Capture contains native state and identity/version/inventory/checksum metadata; release, target configuration, TLS, bootstrap and backup credentials remain separate recovery inputs. Restic cache is disposable.

## Install and observe

```bash
helm upgrade --install forgejo "$CHART" --kube-context "$CTX" \
  -n "$NS" -f "$VALUES" --set-string instance="$INSTANCE"
kubectl --context "$CTX" -n "$NS" wait forgejoapplication/forgejo --for=condition=Ready --timeout=20m
kubectl --context "$CTX" -n "$NS" get forgejoapplication forgejo -o yaml
kubectl --context "$CTX" -n "$NS" wait "certificate/$TLS_SECRET" --for=condition=Ready --timeout=10m
```

Skip Certificate waiting only for external TLS. Check HTTPS and actual TCP SSH access. Do not scale generated Deployments or run another writer.

Before accepting a Ready, Stopped or failure condition, require both `status.observedGeneration` and that condition's `observedGeneration` to equal `metadata.generation`; `kubectl wait` alone can accept a stale condition from the preceding generation. Check the current operation phase, then perform authenticated HTTP/SSH clone and push, LFS and the agreed issue/package/release checks. Helm completion alone does not establish application success. Preparation automatically uses existing owned state; only absence selects the latest matching verified backup, otherwise native initialization. Supported migration is native startup. A normal restart does not restore state. Existing service continues during controller/API observation loss; unavailable observation cannot confirm stop or authorize a transition.

## Capture and recovery

```sh
kubectl --context "$CTX" -n "$NS" annotate forgejoapplication forgejo \
  forgejo.bartl.sh/backup-request="$(date -u +%Y%m%dT%H%M%SZ)" --overwrite
kubectl --context "$CTX" -n "$NS" get forgejoapplication forgejo -o yaml
```

Wait for the requested value in `status.backup.request`, `success: true` and its snapshot identity. The controller drains writers, captures into the protected staging claim, resumes when current intent and capture safety permit, and publishes using Restic. Publication failure sets `BackupFailed` and retains the capture; it is separate from service health. The retained capture belongs to its original staging claim. Before replacement, finish publication with the original state claim, staging claim and version inputs; after failure, submit a unique backup request to retry. Pending publication rejects changes to those inputs without disrupting the healthy service. Do not delete the retained staging data or issue competing filesystem work. Failed capture or ambiguous state does not authorize restart. Inspect the result rather than assuming an annotation succeeded. The optional native Kubernetes CronJob submits this same interface; independent monitoring must detect absent, stale or failed backups.

For recovery, provision an independent empty state claim and protected staging claim in a usable target cluster, preserve installation/instance identity, provide the selected compatible release, native configuration, reader access and independent keys, then execute the ordinary install command. Do not copy source-mounted state or set a restore mode. No backup selection fallback is implicit: damaged latest fails visibly. Keep the failed target stopped for diagnostics and retry on a new independent empty target. Before permitting production access, verify application behavior and withdraw predecessor write authority.

## Replacement upgrade and promotion

Prepare an isolated nonproductive slot without production routing or effects. Set its `services.<instance>.hostname` and, where needed, `ssh.hostname`, then rerender declarations and native configuration with the retained identity keys. Validate its endpoint/TLS and restrict validation access; ingress need not be entirely disabled. LFS returns absolute object URLs, so functional checks require the candidate's real native endpoint, not client-side URL rewriting. Select `services.<instance>.version` for the supported 16.0.7 to 17.0.1 forward transition and invoke ordinary installation on independent empty claims. The native procedure restores the captured release, flushes native queue state, then lets the newer native binary perform its supported migration. Downgrades and unsupported transitions must fail rather than initialize over state.

For planned promotion: arrange the accepted interruption and data-loss boundary; withdraw source ingress and direct SSH access; request and verify the final capture; set source `replicas: 0` and await confirmed `Stopped`. Prevent source restart and direct volume access. Reconstruct the candidate from that final point using a fresh independent target where necessary, check useful behavior and preserved host keys/identity, then apply the intended production endpoint configuration and recheck before activating production HTTP and SSH routing. DNS alone is not fencing. The administrator records the active generation and accepted final-write boundary. If the source cannot be confirmed stopped or isolated, do not promote. If a candidate has accepted writes, never automatically reactivate its predecessor.

When reusing the same hostname, complete the predecessor's CR finalization and Helm uninstall below after its final backup and confirmed stop, before enabling target routing. This retains its claims and supplied Secrets. Stop alone can leave an Ingress pointing at an empty backend and the Helm-owned SSH Service in place; two routes for the same hostname can send requests to the retired slot. Do not treat stopped processes as removal of those routing objects.

## Stop and removal

Ordinary stop is always allowed, including during pending or failed backup publication. Declare it through the application interface, not the generated Deployment:

```sh
helm upgrade forgejo "$CHART" --kube-context "$CTX" -n "$NS" \
  -f "$VALUES" --set-string instance="$INSTANCE" --set "services.$INSTANCE.replicas=0"
kubectl --context "$CTX" -n "$NS" wait forgejoapplication/forgejo --for=condition=Stopped --timeout=20m
```

Stop and uninstall are different operations. For planned uninstall, first finish any pending publication while retaining the controller, application CR, original state and staging claims, and version inputs. After publication failure, submit a new unique `forgejo.bartl.sh/backup-request` through the capture interface above. Wait until `status.backup.request` matches that request, `status.backup.success` is `true`, and a snapshot identity is recorded. Failed or unavailable backup publication postpones planned uninstall; it does not prevent ordinary stop. Do not rely on automatic recovery of pending publication across uninstall.

Once publication is complete, declare stop as above. Require `status.observedGeneration` and the `Stopped` condition's `observedGeneration` to equal the current `metadata.generation`, with the condition `True`; the wait command alone is insufficient. Then delete the application CR while leaving the controller running until its finalizer has drained workloads and jobs, and only afterwards uninstall Helm:

```sh
kubectl --context "$CTX" -n "$NS" delete forgejoapplication forgejo --wait=true --timeout=20m
helm uninstall forgejo --kube-context "$CTX" --namespace "$NS"
```

Do not uninstall first or force-remove the finalizer. Claims, supplied Secrets and the cluster-scoped CRD remain. Retain or explicitly dispose of them under the customer's retention decision. Authorized reinstall uses the retained inputs and ordinary install after confirming no predecessor can run. Avoid an intervening Helm upgrade between CR deletion and uninstall, which would recreate intent.

## Optional composition with Keycloak 0.6.1 and Restic 0.3.0

These remain independent releases, possibly on different clusters. Follow the complete [Keycloak 0.6.1 preparation/install procedure](https://github.com/bartlapp/bartl-keycloak/blob/v0.6.1/README.md) and [qualified Restic 0.3.0 procedure](https://github.com/bartlapp/bartl-restic/blob/75b771ebee3389aad0f2cba4cf44d80830920f0f/README.md), including checksummed chart acquisition, their declared storage/Secrets, current-generation readiness, DNS/TLS, protected credential retention and operating limits. Use their documented instance selectors and the same `"$VALUES"` file; choose provider namespaces/contexts independently. Do not install a provider implicitly by selecting it here.

Set `services.<instance>.identityProvider: keycloak` instead of `oidc.issuer`. For backup set `services.<instance>.backup.endpoint: restic` instead of `backup.repository`, and declare the dedicated repository in `services.restic.repositories`. Initialize/admit it through Restic's ordinary Helm upgrade, not manual `restic init`; retain its encryption key and `reader.restPassword`/`writer.restPassword`/`admin.restPassword` separately as its README specifies. Do not pass admin access to Forgejo.

Render the three public declarations with the selected chart archives. `KC_CHART`, `RESTIC_CHART`, `KC_NS` and `RESTIC_NS` below are the verified archives and namespaces from those procedures:

```bash
helm template keycloak "$KC_CHART" -n "$KC_NS" -f "$VALUES" \
  --set-string instance=keycloak --set-string renderMode=connections \
  --show-only templates/connections.yaml > "$INSTALL_DIR/keycloak-connections.yaml"
helm template backup "$RESTIC_CHART" -n "$RESTIC_NS" -f "$VALUES" \
  --set-string instance=restic --set-string renderMode=connections \
  --show-only templates/connections.yaml > "$INSTALL_DIR/restic-connections.yaml"
helm template forgejo "$CHART" -n "$NS" -f "$VALUES" \
  --set-string instance="$INSTANCE" --set-string renderMode=connections \
  --show-only templates/connections.yaml > "$CONNECTIONS"
OIDC_ISSUER=$(yq -er '.services.keycloak.oidc.issuer' "$INSTALL_DIR/keycloak-connections.yaml")
OIDC_CLIENT_ID=$(INSTANCE="$INSTANCE" yq -er '.oidc.requests[strenv(INSTANCE)].clientId' "$CONNECTIONS")
KC_URL=$(yq -er '.services.keycloak.endpoints.https.url' "$INSTALL_DIR/keycloak-connections.yaml")
# Select the repository name you declared in the Restic procedure.
REPOSITORY_NAME=forgejo
BACKUP_REPOSITORY=$(REPOSITORY_NAME="$REPOSITORY_NAME" yq -er \
  '.backup.providers.restic.repositories[strenv(REPOSITORY_NAME)].repository' "$INSTALL_DIR/restic-connections.yaml")
INSTANCE="$INSTANCE" OIDC_ISSUER="$OIDC_ISSUER" OIDC_CLIENT_ID="$OIDC_CLIENT_ID" \
BACKUP_REPOSITORY="$BACKUP_REPOSITORY" yq -i '
  .resolved.oidc.bindings[strenv(INSTANCE)] = {
    "provider": "keycloak", "issuer": strenv(OIDC_ISSUER), "clientId": strenv(OIDC_CLIENT_ID)
  } |
  .resolved.backup.bindings[strenv(INSTANCE)] = {
    "endpoint": "restic", "repository": strenv(BACKUP_REPOSITORY)
  }
' "$VALUES"
```

Bindings are nonsecret connection results, not overrides: provider/endpoint must match selection and issuer/repository must come from the selected declaration. Refresh after changing choices. Deployment/configuration reject missing or mismatched selected bindings; declaration rendering remains offline. Copy Restic's retained repository `password`, role username (`reader` or `writer`), role `*.restPassword` and validating CA into Forgejo's reader/writer directories, then create its declared Secrets using the standalone block. Keep existing repository keys and transport passwords unchanged on replay and recovery.

### Provision the native Keycloak client without credential rotation

This block applies after provider readiness. Supply current authorized Keycloak master-realm `admin-cli` username/password files under `protected/keycloak-admin`; the account needs realm/client creation, read and secret retrieval without interactive required actions. It is preparation-only, never a Forgejo runtime credential. Existing realms/users are not reset, and existing clients with conflicting settings are refused:

```bash
(
  set -euo pipefail
  WORK=$(mktemp -d "$INSTALL_DIR/protected/keycloak-client.XXXXXX")
  trap 'rm -rf "$WORK"' EXIT
  curl --fail-with-body --silent --show-error "$KC_URL/realms/master/protocol/openid-connect/token" \
    --data-urlencode client_id=admin-cli --data-urlencode grant_type=password \
    --data-urlencode "username@$INSTALL_DIR/protected/keycloak-admin/username" \
    --data-urlencode "password@$INSTALL_DIR/protected/keycloak-admin/password" > "$WORK/token.json"
  TOKEN=$(jq -er '.access_token | select(length > 0)' "$WORK/token.json")
  kc() {
    printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" |
      curl --config - --fail-with-body --silent --show-error "$@"
  }
  REALM=${OIDC_ISSUER##*/}
  REALM_PATH=$(jq -nr --arg realm "$REALM" '$realm | @uri')
  ADMIN_URL="$KC_URL/admin/realms/$REALM_PATH"
  kc "$KC_URL/admin/realms" > "$WORK/realms.json"
  COUNT=$(jq --arg realm "$REALM" '[.[] | select(.realm == $realm)] | length' "$WORK/realms.json")
  if [ "$COUNT" = 0 ]; then
    jq -n --arg realm "$REALM" '{realm: $realm, enabled: true}' > "$WORK/realm.json"
    kc --request POST "$KC_URL/admin/realms" --header 'Content-Type: application/json' --data-binary "@$WORK/realm.json"
  else
    jq -e --arg realm "$REALM" '[.[] | select(.realm == $realm)] | length == 1 and .[0].enabled == true' "$WORK/realms.json" >/dev/null
  fi
  INSTANCE="$INSTANCE" yq -o=json '.oidc.requests[strenv(INSTANCE)]' "$CONNECTIONS" |
    jq '{clientId,redirectUris,webOrigins,protocol:"openid-connect",enabled:true,
      publicClient:false,standardFlowEnabled:true,directAccessGrantsEnabled:false,
      implicitFlowEnabled:false,serviceAccountsEnabled:false,
      attributes:{"pkce.code.challenge.method":"S256"}}' > "$WORK/desired.json"
  kc --get "$ADMIN_URL/clients" --data-urlencode "clientId=$OIDC_CLIENT_ID" > "$WORK/clients.json"
  COUNT=$(jq --arg id "$OIDC_CLIENT_ID" '[.[] | select(.clientId == $id)] | length' "$WORK/clients.json")
  if [ "$COUNT" = 0 ]; then
    kc --request POST "$ADMIN_URL/clients" --header 'Content-Type: application/json' --data-binary "@$WORK/desired.json"
    kc --get "$ADMIN_URL/clients" --data-urlencode "clientId=$OIDC_CLIENT_ID" > "$WORK/clients.json"
  fi
  CLIENT_UUID=$(jq -er --arg id "$OIDC_CLIENT_ID" '[.[] | select(.clientId == $id)] |
    if length == 1 then .[0].id else error("Expected exactly one client") end' "$WORK/clients.json")
  kc "$ADMIN_URL/clients/$CLIENT_UUID" > "$WORK/current.json"
  jq -e --slurpfile desired "$WORK/desired.json" '
    . as $actual | $desired[0] | to_entries |
    all(.[]; . as $entry | if $entry.key == "attributes" then
      $entry.value["pkce.code.challenge.method"] == $actual.attributes["pkce.code.challenge.method"]
      else $entry.value == $actual[$entry.key] end)
  ' "$WORK/current.json" >/dev/null || { printf 'Existing client differs; review without resetting it.\n' >&2; exit 1; }
  # GET preserves the existing secret; POST would rotate it.
  kc "$ADMIN_URL/clients/$CLIENT_UUID/client-secret" > "$WORK/secret.json"
  jq -erj '.value | select(type == "string" and length > 0)' "$WORK/secret.json" > "$WORK/client-secret"
  RETAINED="$INSTALL_DIR/protected/oidc-client-secret"
  if [ -e "$RETAINED" ]; then cmp -s "$RETAINED" "$WORK/client-secret"
  else (set -o noclobber; cat "$WORK/client-secret" > "$RETAINED")
  fi
)
```

Reconcile conflicting settings or secrets deliberately with the provider operator; never delete/recreate restored clients to bypass refusal. Assign users and MFA through native Keycloak administration, independently of application startup.

### Register Forgejo's native authentication source

After bindings are prepared, rerun configuration rendering/Secret preparation above and install Forgejo. For standalone external OIDC the same steps apply with the externally issued retained client secret. The chart's configuration output declares native OAuth arguments, discovery URL and callback; it does not mutate the authentication-source database. Use those arguments, not duplicated URL rules:

```bash
(
  set -euo pipefail
  NAME=$(INSTANCE="$INSTANCE" yq -er '.configuration[strenv(INSTANCE)].oidc.name' "$CONFIGURATION")
  CLIENT=$(INSTANCE="$INSTANCE" yq -er '.configuration[strenv(INSTANCE)].oidc.clientId' "$CONFIGURATION")
  DISCOVERY=$(INSTANCE="$INSTANCE" yq -er '.configuration[strenv(INSTANCE)].oidc.discoveryUrl' "$CONFIGURATION")
  VERSION=$(kubectl --context "$CTX" -n "$NS" get forgejoapplication forgejo -o jsonpath='{.spec.version}')
  POD=$(kubectl --context "$CTX" -n "$NS" get pods \
    -l app.kubernetes.io/name=forgejo,forgejo.bartl.sh/role=app -o json |
    jq -er '[.items[] | select(.status.phase == "Running")] | if length == 1 then .[0].metadata.name else error("Expected one serving pod") end')
  # Shell-quote every argument; the secret travels only over protected stdin.
  quote() { printf "'%s'" "${1//\'/\'\\\'\'}"; }
  {
    printf 'set -eu\n'
    printf 'BIN=%s\nNAME=%s\nCLIENT=%s\nDISCOVERY=%s\nSECRET=%s\n' \
      "$(quote "/usr/local/lib/forgejo/$VERSION/forgejo")" "$(quote "$NAME")" \
      "$(quote "$CLIENT")" "$(quote "$DISCOVERY")" \
      "$(quote "$(cat "$INSTALL_DIR/protected/oidc-client-secret")")"
    cat <<'SCRIPT'
auth() { "$BIN" --work-path /data --custom-path /data/custom --config /data/app.ini admin auth "$@"; }
LIST=$(auth list)
IDS=$(printf '%s\n' "$LIST" | while read -r id name rest; do
  [ "$name" != "$NAME" ] || printf '%s\n' "$id"
done)
set -- $IDS
if [ "$#" = 0 ]; then
  auth add-oauth --name "$NAME" --provider openidConnect --key "$CLIENT" \
    --secret "$SECRET" --auto-discover-url "$DISCOVERY" \
    --scopes openid --scopes profile --scopes email
elif [ "$#" = 1 ]; then
  auth update-oauth --id "$1" --name "$NAME" --provider openidConnect --key "$CLIENT" \
    --secret "$SECRET" --auto-discover-url "$DISCOVERY" \
    --scopes openid --scopes profile --scopes email
else
  printf 'Ambiguous native authentication sources; stop and inspect.\n' >&2
  exit 1
fi
SCRIPT
  } | kubectl --context "$CTX" -n "$NS" exec -i "$POD" -c forgejo -- /bin/sh -s
)
```

Replay updates the same named source using the same retained credentials, not a new secret or duplicate source. Capture retains native auth records; independently retain the provider realm/client and credentials. The package permits OIDC registration but disables automatic account linking; do not link by unverified email. Keep tested local administrator access as the recovery path while identity is unavailable. Verify real browser authorization-code login, reviewed MFA/account behavior, logout and new login through the declared HTTPS endpoint.

## Handover

Hand over the exact chart/image identities, effective values and native configuration, application identity, owned-volume inventory, backup repository/history and keys, reader/writer/maintenance roles, endpoint/TLS/SSH routing and host-key records, Keycloak dependencies, monitoring/retention responsibilities and the procedure above. A successor needs the same operational ability, not this development lab. Recovery keys must be usable without the lost cluster or its Keycloak.

## Build and publication identity

Never build on the workstation. On the authorized Hetzner Linux amd64 builder, with sufficient retained artifact capacity:

```sh
BARTL_FORGEJO_LAB=hetzner OUT=/srv/retained/forgejo-candidate ./build.sh
```

The procedure takes an immutable source snapshot through a temporary Git index without changing the user's index or requiring an unverified implementation commit. It builds once from that exact snapshot and retains an OCI directory, digest-pinned chart, source archive/SHA256, source tree identity, base commit, image reference and checksums. A snapshot tree is not claimed as a verified source commit. An existing output directory is refused. Preserve these artifacts and qualification results independently before deleting the builder. Restic 0.19.1 Linux amd64 compressed binary SHA256 is `f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c`.

### Maintainer package qualification

Run setup and proof from the source checkout on an authorized, budgeted Hetzner Linux amd64 host, never on the workstation. Setup requires a fresh disposable K3s cluster with Traefik, cert-manager and default filesystem storage, an administrative `KUBECONFIG`, Node.js 22 or later, Helm supporting `--wait=legacy`, kubectl, Mike Farah yq v4, OpenSSL and dig. Proof also needs Git with Git LFS, OpenSSH client tools and sha256sum. Supply three distinct public DNS names delegated to deSEC, resolving only to the lab IPv4 (no AAAA). Setup checks authoritative/public resolvers; package charts use cert-manager for public certificates without stopping K3s. Permit ACME HTTP, HTTPS, SSH NodePorts and pod access to the host proof proxy. Setup checksums published Keycloak 0.6.1 and Restic 0.3.0 archives, renders Forgejo native configuration from the retained candidate chart, and refuses existing qualification directories/namespaces. It prepares a new single-cluster lab, not an existing customer installation.

Set the installation-specific DNS, address and kubeconfig inputs in the remote shell; the paths below are absolute remote paths, not workstation paths:

```sh
export BARTL_FORGEJO_LAB=hetzner BARTL_FORGEJO_DISPOSABLE_CLUSTER=yes
export KUBECONFIG=/home/runner/.kube/config
export BARTL_FORGEJO_HOST=forgejo.lab.example.org
export BARTL_FORGEJO_TARGET_HOST=forgejo-target.lab.example.org
export BARTL_KEYCLOAK_HOST=keycloak.lab.example.org
export BARTL_LAB_IPV4=REPLACE_WITH_LAB_PUBLIC_IPV4
export LAB_QUALIFICATION_DIR=/home/runner/qualification
export BARTL_FORGEJO_CHART=/srv/retained/forgejo-candidate/bartl-forgejo-0.2.2.tgz
bash tests/setup-lab.sh
export NODE_EXTRA_CA_CERTS="$LAB_QUALIFICATION_DIR/proxy.crt"
node tests/package-proof.mjs \
  --lab "$LAB_QUALIFICATION_DIR/lab.json" \
  --chart "$BARTL_FORGEJO_CHART" \
  --artifacts /srv/retained/forgejo-candidate \
  --work /home/runner/package-proof
```

The proof consumes the retained build output, not a rebuilt image, and requires a new work directory without whitespace. Keep `lab.json`, generated credentials and raw evidence protected; retain the artifact identities and results independently before deleting the host. These scripts prepare and exercise a disposable lab; they are not the customer installation or recovery interface.

For an independently prepared multi-cluster lab, `lab.json` may set `backup.kubeconfig` to an absolute protected kubeconfig path usable by the proof executor; only backup endpoint port-forwarding uses it. Application operations retain the executor's normal kubeconfig. The backup API must be reachable from that executor. Preserve all behavioral acceptance inputs and source-stop checks when supplying such a lab; the setup helper itself remains fresh single-cluster preparation.

After separate publication authorization and successful exact-candidate qualification, publish these same bytes without rebuilding:

```sh
skopeo copy --preserve-digests oci:/srv/retained/forgejo-candidate/image:candidate \
  docker://ghcr.io/bartlapp/bartl-forgejo-controller:QUALIFIED_PACKAGE_TAG
helm push /srv/retained/forgejo-candidate/bartl-forgejo-0.2.2.tgz oci://ghcr.io/bartlapp
```

Verify the registry digest against `image.digest` and retain the chart checksum, source archive/tree identity and qualification identity with the release. The chart continues to select the digest, never the mutable publication tag. Publishing is not part of `build.sh`.

Authored package code is Apache-2.0. Forgejo is GPL-3.0-or-later; upstream notices, licenses and corresponding source remain separate and are retained in the combined image. See [NOTICE](NOTICE) and [LICENSE](LICENSE).
