# Repository instructions

Read this repository's [README](README.md) before changing configuration, lifecycle behavior, procedures or qualification.

For coordinated local work, follow the sibling [bartl-app entry point](../bartl-app/AGENTS.md) and its local contract and operating guide. If that checkout is unavailable, use the public [application contract](https://github.com/bartlapp/bartl-app/blob/main/docs/application-contract.md) and [operating guide](https://github.com/bartlapp/bartl-app/blob/main/docs/how-to-bartlize.md). These documents own the requirements; this file does not restate them.

Identify the governing revision and relevant local changes. Honor a requested exact revision, resolve conflicting versions before implementation, and do not switch branches automatically. Unpublished local guidance does not establish public package availability or qualification.

## Historical evidence

Release 0.1.0 was qualified on Hetzner/K3s 1.36.5, Helm 4.3.1, Restic 0.2.1 and Keycloak 0.5.1. It exercised initialization with/without backup, restart without backup access, independent-state reconstruction, native 16.0.7-to-17.0.1 replacement, HTTP/SSH Git, LFS, issues/PRs, attachments/packages, permissions, SSH identity and real-browser Keycloak login. Failure cases covered malformed lookup, corrupt recovery, interrupted preparation, stop precedence, publication retry and downgrade refusal without changing retained inputs.

| Historical artifact | Immutable identity |
| --- | --- |
| Combined image | `ghcr.io/bartlapp/bartl-forgejo-controller@sha256:e6b4eae6d8b832d0465fdfd12efae57f0cce1bb7de61d6b26b6d4ff67db7f929` |
| 0.1.0 chart manifest | `c9d6e7189d29b7b2923a61c5036e38197005912fdd68c6acec6a78654e297f3c` |
| 0.1.0 chart archive | `9cea9c65c1bcd111ea86181103cba22b6e86f36f1b9f0e8778ca3449c256f067` |
| Image source tree | `5093bae8bf1aa30e24f4d9187284aeb2121e3eaf` |
| Image source archive | `9dc5553a50b122055110ebb257def54a1d8cb165ee24ae7a62dd0abb1c83b2f1` |
| Forgejo 16.0.7 binary | `ca8ecb37da9210d5730c878308bf31e25b6b526a7294b263d07f6c8718af07b7` |
| Forgejo 17.0.1 binary | `51a8cdf4a1252e79f4b3855d7a7891af6c6635105420768c460e2f0690385d6f` |

The image source remains reachable at `refs/evidence/candidate-05-source`. `tests/package-proof.mjs` final removal continuation binds fourteen earlier passed cases to that same image/source. The image was not rebuilt; the chart was repackaged only for the consistent controller image name, then published-digest installation/readiness/restored content and anonymous chart/image acquisition were verified.

Planned removal finished publication before uninstall. Retained-state reinstall produced a new backup whose new Git files, issue and package restored independently. That synthetic single-node capture observed 15.651 seconds of serving-Pod readiness interruption, with a 17.087-second polling upper bound; neither is end-user availability or a production guarantee. Independent volumes on one node do not establish provider-loss fencing.

The source-only backup declaration correction makes omission eligible for preparation and explicit false opt out. Published 0.2.2 declares omission ineligible. Standalone operation still needs a destination to enable backup. The correction requires a new chart release and renewed affected qualification; do not overwrite 0.2.2.

## Build and publication identity

Build deliverable images only on an authorized Hetzner Linux amd64 builder with sufficient independently retained artifact capacity. The offline checks below may run on the workstation:

```bash
BARTL_FORGEJO_LAB=hetzner OUT=/srv/retained/forgejo-candidate ./build.sh
```

The build takes an immutable source snapshot through a temporary Git index without changing the user's index. It retains an OCI directory, digest-pinned chart, source archive/hash, source tree, base commit, image reference and checksums; a snapshot tree is not a verified source commit. Existing output directories are refused. For candidates selected for delivery, preserve artifacts and qualification independently before deleting the builder; abandoned candidates follow the operating guide's disposal boundary. Restic 0.19.1 Linux amd64 compressed binary SHA-256 is `f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c`.

Before cluster qualification, follow the [candidate custody boundary](../bartl-app/docs/how-to-bartlize.md#qualify-the-delivered-path). With candidate uploads authorized and isolated registry credentials selected, upload the built image under an unused candidate tag in the existing image repository:

```bash
OUT=/srv/retained/forgejo-candidate
CANDIDATE="candidate-$(cat "$OUT/package-version")-$(cut -d: -f2 "$OUT/image.digest")"
skopeo copy --authfile "$REGISTRY_AUTH_FILE" --preserve-digests \
  "oci:$OUT/image:candidate" "docker://ghcr.io/bartlapp/bartl-forgejo-controller:$CANDIDATE"
```

Verify the remote manifest against `image.digest`. Retain the unchanged chart archive, source archive/identity and checksums in the package's draft release and verify retrieval. The cluster can then pull the chart's pinned image before stable chart publication. Candidate upload does not qualify the artifact or select a stable chart.

## Maintainer qualification

Offline checks: `go test ./...` and `node tests/chart.mjs`. Native recipes remain in `tests/native.sh` and `tests/native-fixture.mjs`. Cluster proof runs only on an authorized/budgeted disposable Hetzner Linux amd64 host, not the workstation.

Supply fresh K3s with enforcing NetworkPolicy, Traefik/cert-manager/default filesystem storage, administrator kubeconfig, Node.js 22+, Helm supporting `--wait=legacy`, kubectl, yq v4, OpenSSL, dig, Git/LFS, OpenSSH and sha256sum. Three distinct deSEC hostnames must resolve only to the lab IPv4, with no AAAA; permit ACME HTTP, HTTPS, SSH NodePorts and pod access to the proof proxy. Setup verifies DNS and checksums Keycloak 0.8.0/Restic 0.3.0 archives. It refuses existing directories/namespaces and is not an existing-installation operator interface.

For `acme.fke.systems`, first prepare the [isolated public-plus-staging CA bundle](../bartl-app/docs/reference-installation/README.md#prepare-test-client-trust) and supply its absolute path as `CLIENT_CA_BUNDLE`. Setup selects LE staging for all public fixture services and supplies native Forgejo client trust. Before setup, export `NODE_EXTRA_CA_CERTS="$CLIENT_CA_BUNDLE"`, `CURL_CA_BUNDLE="$CLIENT_CA_BUNDLE"` and `GIT_SSL_CAINFO="$CLIENT_CA_BUNDLE"`. Keep these exports confined to the qualification shell. The proof's combined bundle adds only its private fault-proxy CA; it does not modify system trust. Fresh real-browser OIDC acceptance with isolated staging trust remains a separate required check.

```bash
export BARTL_FORGEJO_LAB=hetzner BARTL_FORGEJO_DISPOSABLE_CLUSTER=yes
export KUBECONFIG=/home/runner/.kube/config
export BARTL_FORGEJO_HOST=forgejo.lab.example.org
export BARTL_FORGEJO_TARGET_HOST=forgejo-target.lab.example.org
export BARTL_KEYCLOAK_HOST=keycloak.lab.example.org
export BARTL_LAB_IPV4=REPLACE_WITH_LAB_PUBLIC_IPV4
export LAB_QUALIFICATION_DIR=/home/runner/qualification
export BARTL_FORGEJO_CHART=/srv/retained/forgejo-candidate/bartl-forgejo-NEW_VERSION.tgz
bash tests/setup-lab.sh
export NODE_EXTRA_CA_CERTS="$LAB_QUALIFICATION_DIR/client-ca.pem"
node tests/package-proof.mjs --lab "$LAB_QUALIFICATION_DIR/lab.json" \
  --chart "$BARTL_FORGEJO_CHART" --artifacts /srv/retained/forgejo-candidate \
  --work /home/runner/package-proof
```

Proof consumes retained build output, not rebuilt images, and requires a fresh work directory without whitespace. Protect lab.json, credentials and raw evidence. An independently prepared multi-cluster lab may supply `backup.kubeconfig` for endpoint port-forwarding only; application operations retain normal executor access. Preserve all acceptance inputs/source-stop checks. Setup itself remains a fresh single-cluster fixture.

After exact-candidate qualification and with stable publication authorized, publish the same retained bytes. Authorization may cover candidate upload and stable publication together; an explicit reserved gate or material exception still requires a decision.

```bash
skopeo copy --preserve-digests oci:/srv/retained/forgejo-candidate/image:candidate \
  docker://ghcr.io/bartlapp/bartl-forgejo-controller:QUALIFIED_PACKAGE_TAG
helm push "$BARTL_FORGEJO_CHART" oci://ghcr.io/bartlapp
```

Verify the registry digest against image.digest and retain chart checksum, source archive/tree and qualification identities in the release record. Charts select immutable digests, not publication tags. Publication is not part of build.sh. Never overwrite published versions or silently change visibility/access; retain cleanup/budget outcomes.
