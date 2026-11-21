# Release preparation

The Registry source address is `vbargl/routeros`. The manifest advertises plugin protocol 6 and the GoReleaser configuration builds versioned binaries, ZIP archives and checksums. Test the configuration locally with `goreleaser release --snapshot --clean --skip=publish`.
The manually dispatched release-artifacts workflow builds the same ZIPs, manifest and checksums and uploads them as unsigned workflow artifacts.

A release operator must first verify repository eligibility and approve the license and release tag. On an approved release, build the release ZIPs and checksum file, including the versioned manifest in the checksums. Sign the checksum file using an existing authorized OpenPGP signing key:

```sh
gpg --batch --local-user "$RELEASE_GPG_FINGERPRINT" --detach-sign \
  --output "${CHECKSUM_FILE}.sig" "$CHECKSUM_FILE"
```

Verify the detached signature with the corresponding public key. The Registry consumes the platform ZIPs, versioned manifest, checksum file and checksum signature from the GitHub release. Register the existing public signing key before publishing through the Registry operator account. Keep the private signing key outside the repository and build outputs.
