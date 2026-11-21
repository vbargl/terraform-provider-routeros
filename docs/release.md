# Prepare a Terraform Registry release

The intended provider address is `vbargl/routeros`. This is an operator
procedure; packaging, signing, GitHub publication and Registry registration
are separate steps. Publishing requires an explicit release decision. Never put
private signing keys, passphrases or provider credentials in the repository,
Nix store, workflow artifacts, command logs or Terraform state.

## Select the release

Choose and record the version, release commit on `main`, license and the
operator authorized to sign. Verify that the commit is the intended reviewed
revision and its CI checks passed. Do not assume a particular version is
approved. The commands below use `VERSION` (without `v`) and `COMMIT` (the
full reviewed Git SHA) supplied by the operator.

The verifier requires Nushell, `sha256sum` and `unzip`; signed verification also
requires GnuPG and the expected public key in the operator keyring.

Verify the source on a clean checkout and ensure GoReleaser reads the exact
commit. For artifact-only review, start the `Prepare release artifacts`
workflow on that commit with its `version` input. Inspect the workflow run's
recorded SHA and test result before downloading its artifacts. The workflow
uses a snapshot build and **does not publish or sign**. Alternatively run the
same snapshot build in a clean checkout:

```sh
RELEASE_VERSION="$VERSION" goreleaser release --snapshot --clean --skip=publish
```

Copy `terraform-registry-manifest.json` to the versioned filename in `dist/`
as the workflow does, then verify from the repository root:

```sh
nu --no-config-file scripts/verify-release.nu dist "$VERSION"
```

Confirm that the archive and binary names, versioned manifest and checksums
match the version and supported platforms. GoReleaser's snapshot version comes
from `RELEASE_VERSION`; verify the versioned binary filename and `COMMIT` before
using those artifacts. The Registry manifest is also included in the checksum
file. Never substitute a different commit merely because its version matches.

## Sign and publish after authorization

Use an existing authorized OpenPGP signing key under operator control. The
key must meet current Terraform Registry requirements; export only its public
part for Registry enrollment. Sign the exact verified checksum file with a
detached signature and verify the signature against the expected public-key
fingerprint. Keep the private key and any passphrase out of CI unless a
separate key-management design has been approved.

```sh
gpg --batch --local-user "$RELEASE_GPG_FINGERPRINT" --detach-sign \
  --output "${CHECKSUM_FILE}.sig" "$CHECKSUM_FILE"
nu --no-config-file scripts/verify-release.nu dist "$VERSION" --signer "$RELEASE_GPG_FINGERPRINT"
```

Create the version tag pointing to the reviewed `COMMIT`. Publish a GitHub
release for that tag with every platform ZIP, the versioned manifest, the
SHA256SUMS file and its `.sig`. Verify that the release's tag and downloadable
assets match the verified files and signature. A checksum generated from the
wrong build only proves consistency, not provenance.

In Terraform Registry, sign in as the intended owner, add the public signing
key and register the public GitHub provider repository. Registry can install a
release webhook to ingest future versions. Registration, key enrollment and
publication are external changes; they are not performed by the artifact-only
workflow. See [HashiCorp's provider publishing guide](https://developer.hashicorp.com/terraform/registry/providers/publishing).

## Verify the published version

Check the Registry listing, version, supported platforms and documentation.
In a temporary directory with no production configuration or credentials,
initialize a minimal Terraform configuration requiring `vbargl/routeros` at the
published version. Verify `terraform init` resolves and verifies the official
registry package and signing key. `init` establishes installation, not RouterOS
or K3s runtime correctness; run integration tests separately in the documented
isolated environment. Keep generated lock and state files out of the repository.
