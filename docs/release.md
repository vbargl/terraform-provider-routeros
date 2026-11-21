# Release preparation

The Registry source address is `vbargl/routeros`. GoReleaser builds protocol 6
binaries, ZIP archives and a checksum file covering the archives and versioned
Registry manifest. The manual Publish workflow builds these unsigned
artifacts.

Dispatch the workflow on `main` with a version such as `0.1.0`. After the
build succeeds, the workflow creates the matching Git tag and a GitHub
Release. The publishing job verifies the downloaded assets against their
SHA-256 sums, creates the tag on the exact build commit and publishes against
that tag. An existing tag, draft or release stops publication.

If publishing fails after tag creation, use the artifacts from that workflow
run. Verify their checksums and confirm the tag points to that run's commit.
Inspect any existing draft and its assets before completing it. If there
is no draft, create the release from the verified tag with
`gh release create --verify-tag` and attach the verified assets.

GitHub assets from this workflow are unsigned.

For an approved release, verify repository eligibility, license and tag.
Verify all artifact checksums and sign the checksum manifest with an existing
authorized OpenPGP key in the release operator's environment.

```nu
let checksum = $env.CHECKSUM_FILE
^sha256sum -c $checksum
^gpg --batch --local-user $env.RELEASE_GPG_FINGERPRINT --detach-sign --output $"($checksum).sig" $checksum
^gpg --verify $"($checksum).sig" $checksum
```

The Registry consumes the platform ZIPs, versioned manifest, checksum file
and detached signature from the GitHub release. The release operator registers
the public key before publishing through the Registry account.
