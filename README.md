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

Restic 0.19.1 can report snapshot listing errors on stderr while returning exit status zero and an empty or partial JSON list. The package therefore requires both successful exit and a clean diagnostic channel before interpreting backup absence. It retains native filtering and locking rather than adding another enumeration engine.

### Release artifacts

| Artifact | Immutable identity |
| --- | --- |
| Image | `ghcr.io/bartlapp/bartl-forgejo-controller@sha256:e6b4eae6d8b832d0465fdfd12efae57f0cce1bb7de61d6b26b6d4ff67db7f929` |
| Chart | `oci://ghcr.io/bartlapp/bartl-forgejo@sha256:c9d6e7189d29b7b2923a61c5036e38197005912fdd68c6acec6a78654e297f3c` |
| Chart archive SHA256 | `9cea9c65c1bcd111ea86181103cba22b6e86f36f1b9f0e8778ca3449c256f067` |
| Build source tree | `5093bae8bf1aa30e24f4d9187284aeb2121e3eaf` |
| Build source archive SHA256 | `9dc5553a50b122055110ebb257def54a1d8cb165ee24ae7a62dd0abb1c83b2f1` |

The exact image build tree remains reachable at `refs/evidence/candidate-05-source`. Qualification used `tests/package-proof.mjs`; its final removal continuation binds the preceding fourteen passed cases to the same image and source identities. The image was not rebuilt. The chart was repackaged solely to reference the consistent `bartl-forgejo-controller` image name; installation from its published digest, current-generation readiness and access to restored repository content were then verified on Hetzner. Registry visibility is currently private pending the authorized public-visibility change.

The qualified placement was one K3s node with independent per-slot volumes and synthetic data. These results establish application reconstruction and planned replacement, not host/provider-loss recovery or production-scale interruption limits. `tests/native.sh` and `tests/native-fixture.mjs` retain the underlying native recipe.

## Inputs and persistence

Install release `forgejo` in a chosen namespace, one logical instance per namespace. The commands below use namespace `forgejo`; independent slots may use other namespaces on the same cluster with separate claims, or another cluster. Supply complete reviewed installation values, not `--reuse-values` across package upgrades:

```yaml
identity:
  installation: customer-installation
  instance: forgejo
version: "17.0.1"
replicas: 1
claim: forgejo-state
stagingClaim: forgejo-capture
configurationSecret: forgejo-configuration
bootstrapSecret: forgejo-bootstrap
backup:
  repository: rest:https://backup.example/forgejo/
  readerSecret: forgejo-backup-reader
  writerSecret: forgejo-backup-writer
  schedule: "0 2 * * *"
  timeZone: Europe/Berlin
ingress:
  enabled: true
  host: git.example.org
  className: nginx
  tlsSecret: forgejo-tls
ssh:
  port: 2222
```

Create the namespace and inputs before installation. Both claims are operator-owned, distinct, filesystem volumes writable by UID/GID 1000 with fsGroup 1000. Allocate state capacity for repositories, LFS, SQLite, attachments, packages, releases, indexes, native queue state and logs. Allocate staging capacity for a complete independent capture plus integrity metadata; retain headroom for reconstruction and native upgrade. The package never deletes either claim or supplied Secrets. Storage must support native SQLite locking and exclusive single-instance writes. Concurrent mounts by another writer are forbidden, regardless of PVC access mode.

The configuration Secret contains `app.ini`. Set native paths under `/data`: work path `/data`, `APP_DATA_PATH=/data/data`, SQLite `/data/data/forgejo.db`, repositories `/data/data/repositories`, SSH keys `/data/data/ssh`; keep every configured mutable path within the owned volume. Configure native HTTP port 3000 and builtin SSH port 2222, public `ROOT_URL`, SSH domain and advertised port, and `RUN_USER=git`. Preserve `SECRET_KEY`, `INTERNAL_TOKEN`, `LFS_JWT_SECRET` and any external secret references independently. Target-specific URLs and configuration are supplied separately from the recovery point. The worker enforces the supported disabled-effects profile and resolves effective native configuration. Do not depend on unsupported paths or effects.

Optional bootstrap Secret keys are `username`, `password`, `email`; it creates a new installation administrator, not a startup dependency for recovered state. Backup reader/writer Secrets each contain `password` (same Restic repository encryption password), `username`, `restPassword`, and optionally `ca.crt`. Credentials are delivered only to the jobs needing them, never to the serving process. Reader access must permit reads and native Restic lock creation/removal; writer access must be append-only. Independently provision repository initialization, retention, destructive maintenance, monitoring and recovery keys. No backup configuration means an empty target initializes natively; a configured empty repository does likewise. Lookup or integrity failure never means absence.

A stopped capture includes the owned native filesystem and identity/version/inventory/checksum metadata; Restic publishes a complete verified point. Public endpoint/TLS, bootstrap and backup credentials, Kubernetes objects, the immutable release and effective target configuration are separate recovery inputs. Keep those available outside the failure domain being covered. Restic cache is disposable. Application consistency requires confirmed native shutdown, not a live SQLite file copy.

## Install and observe

Use the retained digest-pinned chart from the selected package. The source chart deliberately rejects an unpinned image. Provision ingress/TLS, SSH TCP exposure, DNS and backup reachability in the installation infrastructure; an HTTP Ingress does not expose SSH. The controller owns its application Service and optional Ingress. Do not scale generated Deployments or run a second writer.

```sh
CHART=oci://ghcr.io/bartlapp/bartl-forgejo@sha256:c9d6e7189d29b7b2923a61c5036e38197005912fdd68c6acec6a78654e297f3c
kubectl create namespace forgejo
# Provision the two claims and protected Secrets from installation-controlled inputs.
helm upgrade --install forgejo "$CHART" \
  --namespace forgejo --values installation-values.yaml
kubectl -n forgejo wait forgejoapplication/forgejo --for=condition=Ready --timeout=20m
kubectl -n forgejo get forgejoapplication forgejo -o yaml
```

Before accepting a Ready, Stopped or failure condition, require both `status.observedGeneration` and that condition's `observedGeneration` to equal `metadata.generation`; `kubectl wait` alone can accept a stale condition from the preceding generation. Check the current operation phase, then perform authenticated HTTP/SSH clone and push, LFS and the agreed issue/package/release checks. Helm completion alone does not establish application success. Preparation automatically uses existing owned state; only absence selects the latest matching verified backup, otherwise native initialization. Supported migration is native startup. A normal restart does not restore state. Existing service continues during controller/API observation loss; unavailable observation cannot confirm stop or authorize a transition.

## Capture and recovery

```sh
kubectl -n forgejo annotate forgejoapplication forgejo \
  forgejo.bartl.sh/backup-request="$(date -u +%Y%m%dT%H%M%SZ)" --overwrite
kubectl -n forgejo get forgejoapplication forgejo -o yaml
```

Wait for the requested value in `status.backup.request`, `success: true` and its snapshot identity. The controller drains writers, captures into the protected staging claim, resumes when current intent and capture safety permit, and publishes using Restic. Publication failure sets `BackupFailed` and retains the capture; it is separate from service health. The retained capture belongs to its original staging claim. Before replacement, finish publication with the original state claim, staging claim and version inputs; after failure, submit a unique backup request to retry. Pending publication rejects changes to those inputs without disrupting the healthy service. Do not delete the retained staging data or issue competing filesystem work. Failed capture or ambiguous state does not authorize restart. Inspect the result rather than assuming an annotation succeeded. The optional native Kubernetes CronJob submits this same interface; independent monitoring must detect absent, stale or failed backups.

For recovery, provision an independent empty state claim and protected staging claim in a usable target cluster, preserve installation/instance identity, provide the selected compatible release, native configuration, reader access and independent keys, then execute the ordinary install command. Do not copy source-mounted state or set a restore mode. No backup selection fallback is implicit: damaged latest fails visibly. Keep the failed target stopped for diagnostics and retry on a new independent empty target. Before permitting production access, verify application behavior and withdraw predecessor write authority.

## Replacement upgrade and promotion

Prepare an isolated nonproductive slot without production routing or effects. Give it its own native `ROOT_URL` and `SSH_DOMAIN`, with validated endpoint/TLS and restricted validation access; ingress need not be entirely disabled. LFS returns absolute object URLs, so functional checks require the candidate's real native endpoint, not client-side URL rewriting. Preserve recovered identity secrets. Restore with ordinary installation at the desired supported version. The native procedure restores the captured release, flushes native queue state, then lets the newer native binary perform its supported migration. Downgrades and unsupported transitions must fail rather than initialize over state.

For planned promotion: arrange the accepted interruption and data-loss boundary; withdraw source ingress and direct SSH access; request and verify the final capture; set source `replicas: 0` and await confirmed `Stopped`. Prevent source restart and direct volume access. Reconstruct the candidate from that final point using a fresh independent target where necessary, check useful behavior and preserved host keys/identity, then apply the intended production endpoint configuration and recheck before activating production HTTP and SSH routing. DNS alone is not fencing. The administrator records the active generation and accepted final-write boundary. If the source cannot be confirmed stopped or isolated, do not promote. If a candidate has accepted writes, never automatically reactivate its predecessor.

## Stop and removal

Ordinary stop is always allowed, including during pending or failed backup publication. Declare it through the application interface, not the generated Deployment:

```sh
helm upgrade forgejo ./bartl-forgejo-0.1.0.tgz --namespace forgejo \
  --values installation-values.yaml --set replicas=0
kubectl -n forgejo wait forgejoapplication/forgejo --for=condition=Stopped --timeout=20m
```

Stop and uninstall are different operations. For planned uninstall, first finish any pending publication while retaining the controller, application CR, original state and staging claims, and version inputs. After publication failure, submit a new unique `forgejo.bartl.sh/backup-request` through the capture interface above. Wait until `status.backup.request` matches that request, `status.backup.success` is `true`, and a snapshot identity is recorded. Failed or unavailable backup publication postpones planned uninstall; it does not prevent ordinary stop. Do not rely on automatic recovery of pending publication across uninstall.

Once publication is complete, declare stop as above. Require `status.observedGeneration` and the `Stopped` condition's `observedGeneration` to equal the current `metadata.generation`, with the condition `True`; the wait command alone is insufficient. Then delete the application CR while leaving the controller running until its finalizer has drained workloads and jobs, and only afterwards uninstall Helm:

```sh
kubectl -n forgejo delete forgejoapplication forgejo --wait=true --timeout=20m
helm uninstall forgejo --namespace forgejo
```

Do not uninstall first or force-remove the finalizer. Claims, supplied Secrets and the cluster-scoped CRD remain. Retain or explicitly dispose of them under the customer's retention decision. Authorized reinstall uses the retained inputs and ordinary install after confirming no predecessor can run. Avoid an intervening Helm upgrade between CR deletion and uninstall, which would recreate intent.

## Keycloak and handover

Use Forgejo's native OAuth2/OpenID Connect authentication source for Keycloak. Configure it through native Forgejo administration (or its supported `forgejo admin auth add-oauth` interface), with the realm discovery URL, client ID, protected client secret and Forgejo's native callback URL. Configure `[oauth2_client]` in `app.ini` for the reviewed account-linking/signup behavior. Preserve the native authentication-source database records and secret material in capture; independently retain the Keycloak realm/client and credentials. Do not automatically link identities by unverified email. Existing native/local administrator access supplies the recovery path while Keycloak is reconstructed. No custom login bridge or package-created realm is required.

Hand over the exact chart/image identities, effective values and native configuration, application identity, owned-volume inventory, backup repository/history and keys, reader/writer/maintenance roles, endpoint/TLS/SSH routing and host-key records, Keycloak dependencies, monitoring/retention responsibilities and the procedure above. A successor needs the same operational ability, not this development lab. Recovery keys must be usable without the lost cluster or its Keycloak.

## Build and publication identity

Never build on the workstation. On the authorized Hetzner Linux amd64 builder, with sufficient retained artifact capacity:

```sh
BARTL_FORGEJO_LAB=hetzner OUT=/srv/retained/forgejo-candidate ./build.sh
```

The procedure takes an immutable source snapshot through a temporary Git index without changing the user's index or requiring an unverified implementation commit. It builds once from that exact snapshot and retains an OCI directory, digest-pinned chart, source archive/SHA256, source tree identity, base commit, image reference and checksums. A snapshot tree is not claimed as a verified source commit. An existing output directory is refused. Preserve these artifacts and qualification results independently before deleting the builder. Restic 0.19.1 Linux amd64 compressed binary SHA256 is `f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c`.

### Maintainer package qualification

Run setup and proof from the source checkout on an authorized, budgeted Hetzner Linux amd64 host, never on the workstation. Setup requires a fresh disposable K3s cluster with Traefik and default filesystem storage, an administrative `KUBECONFIG`, passwordless `sudo`, Node.js 22 or later, Helm supporting `--wait=legacy`, `kubectl`, OpenSSL, Certbot, `dig`, systemd and `/usr/local/bin/k3s-killall.sh`. Proof also needs Git with Git LFS, OpenSSH client tools and `sha256sum`. Supply three distinct public DNS names delegated to deSEC, resolving only to the lab IPv4 (no AAAA); setup checks authoritative and public resolvers and obtains public TLS certificates. Permit ACME HTTP access, application HTTPS, and pod access to the host's proof proxy. Setup temporarily stops K3s for certificate issuance and refuses existing qualification directories or lab namespaces.

Set the installation-specific DNS, address and kubeconfig inputs in the remote shell; the paths below are absolute remote paths, not workstation paths:

```sh
export BARTL_FORGEJO_LAB=hetzner BARTL_FORGEJO_DISPOSABLE_CLUSTER=yes
export KUBECONFIG=/home/runner/.kube/config
export BARTL_FORGEJO_HOST=forgejo.lab.example.org
export BARTL_FORGEJO_TARGET_HOST=forgejo-target.lab.example.org
export BARTL_KEYCLOAK_HOST=keycloak.lab.example.org
export BARTL_LAB_IPV4=REPLACE_WITH_LAB_PUBLIC_IPV4
export LAB_QUALIFICATION_DIR=/home/runner/qualification
bash tests/setup-lab.sh
node tests/package-proof.mjs \
  --lab "$LAB_QUALIFICATION_DIR/lab.json" \
  --chart /srv/retained/forgejo-candidate/bartl-forgejo-0.1.0.tgz \
  --artifacts /srv/retained/forgejo-candidate \
  --work /home/runner/package-proof
```

The proof consumes the retained build output, not a rebuilt image, and requires a new work directory without whitespace. Keep `lab.json`, generated credentials and raw evidence protected; retain the artifact identities and results independently before deleting the host. These scripts prepare and exercise a disposable lab; they are not the customer installation or recovery interface.

After separate publication authorization and successful exact-candidate qualification, publish these same bytes without rebuilding:

```sh
skopeo copy --preserve-digests oci:/srv/retained/forgejo-candidate/image:candidate \
  docker://ghcr.io/bartlapp/bartl-forgejo-controller:QUALIFIED_PACKAGE_TAG
helm push /srv/retained/forgejo-candidate/bartl-forgejo-0.1.0.tgz oci://ghcr.io/bartlapp
```

Verify the registry digest against `image.digest` and retain the chart checksum, source archive/tree identity and qualification identity with the release. The chart continues to select the digest, never the mutable publication tag. Publishing is not part of `build.sh`.

Authored package code is Apache-2.0. Forgejo is GPL-3.0-or-later; upstream notices, licenses and corresponding source remain separate and are retained in the combined image. See [NOTICE](NOTICE) and [LICENSE](LICENSE).
