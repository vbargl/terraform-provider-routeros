# Release preparation

The Registry source address is `vbargl/routeros`. The manifest advertises plugin protocol 6 and the GoReleaser configuration builds versioned binaries, ZIP archives and checksums. Test the configuration locally with `goreleaser release --snapshot --clean --skip=publish`.
Run **Publish** in GitHub Actions and enter the version without the leading `v`. The default run uploads unsigned ZIPs, the Registry manifest and checksums as workflow artifacts.

To publish those files as a GitHub Release, select `main` and enable **Publish a GitHub Release and tag from main**. After the build and tests pass, the workflow verifies the uploaded checksums, creates `v<version>` at the exact commit that was built and attaches the files to the release. An existing tag that points to a different commit causes the run to fail. The workflow produces an unsigned GitHub Release.

## Signing for Terraform Registry

Before a Registry release, verify repository eligibility and approve the license and release tag. Sign the checksum file using an existing authorized OpenPGP signing key:

```sh
gpg --batch --local-user "$RELEASE_GPG_FINGERPRINT" --detach-sign \
  --output "${CHECKSUM_FILE}.sig" "$CHECKSUM_FILE"
```

Verify the detached signature with the corresponding public key. The Registry consumes the platform ZIPs, versioned manifest, checksum file and checksum signature from the GitHub release. Register the existing public signing key before publishing through the Registry operator account. Keep the private signing key outside the repository and build outputs.
