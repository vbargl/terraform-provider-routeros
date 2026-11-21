# Release preparation

The Registry source address is `vbargl/routeros`. GoReleaser builds protocol 6
binaries, ZIP archives and a checksum file covering the archives and versioned
Registry manifest. The manual Publish workflow signs the checksum file
with the approved OpenPGP key and attaches the binary detached signature
as `terraform-provider-routeros_<version>_SHA256SUMS.sig`.

Dispatch the workflow on `main` with a version such as `0.1.0`. After the
build succeeds, the workflow creates the matching Git tag and a GitHub
Release. Before creating the tag, the publishing job verifies the downloaded
assets against their SHA-256 sums, signs the checksum file and verifies the
signature. It uses the `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE` Actions secrets
only in the signing step. Use the ASCII-armored private key export for
`GPG_PRIVATE_KEY`. The expected primary fingerprint is
`76963B622D6FE389EB3F1C94EDEF61DC66116445`. The step imports the key into a
temporary GPG home and deletes that home after signing. An existing tag,
draft or release stops publication.

If publishing fails after tag creation, use the artifacts from that workflow
run. Verify their checksums and confirm the tag points to that run's commit.
Inspect any existing draft and its assets before completing it. The build
artifacts contain the unsigned checksum file. For a manual recovery, verify
the checksums and sign that exact file with the approved OpenPGP key in the
release operator's environment. Verify the signature against the expected
fingerprint before attaching it. If there is no draft, create the release
from the verified tag with `gh release create --verify-tag` and attach the
verified assets and signature.

```nu
let checksum = $env.CHECKSUM_FILE
^sha256sum -c $checksum
^gpg --batch --local-user $env.RELEASE_GPG_FINGERPRINT --detach-sign --output $"($checksum).sig" $checksum
^gpg --verify $"($checksum).sig" $checksum
```

The Registry consumes the platform ZIPs, versioned manifest, checksum file
and detached signature from the GitHub release. For Registry publication, the release operator verifies repository
eligibility, license and tag and registers the matching public key through
the Registry account.
