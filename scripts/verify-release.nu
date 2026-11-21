#!/usr/bin/env -S nu --no-config-file

# Verify a prepared release without signing, publishing, or accessing secrets.
def main [directory: path, version: string, --signer: string] {
  if $version !~ '^[0-9]+\.[0-9]+\.[0-9]+$' {
    error make {msg: "Expected a release version in X.Y.Z format"}
  }
  let prefix = $"terraform-provider-routeros_($version)"
  let checksum = ($directory | path join $"($prefix)_SHA256SUMS")
  let manifest = ($directory | path join $"($prefix)_manifest.json")
  let platforms = [linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64]
  let expected = ($platforms | each {|platform| $"($prefix)_($platform).zip" } | append $"($prefix)_manifest.json" | sort)
  let entries = (open --raw $checksum | lines | where {|line| $line != "" } | each {|line|
    let match = ($line | parse -r '^(?<hash>[0-9a-f]{64})  (?<name>[^/\\]+)$')
    if ($match | length) != 1 { error make {msg: "Malformed or unsafe checksum entry"} }
    $match.0.name
  } | sort)
  if $entries != $expected { error make {msg: "Checksum entries must match the supported archives and manifest exactly"} }
  let result = (do { cd $directory; ^sha256sum -c ($checksum | path basename) } | complete)
  if $result.exit_code != 0 { error make {msg: "Release checksum verification failed"} }
  let registry = (open $manifest)
  if $registry.version != 1 or ('6.0' not-in $registry.metadata.protocol_versions) {
    error make {msg: "Unexpected Terraform Registry manifest"}
  }
  for platform in $platforms {
    let archive = ($directory | path join $"($prefix)_($platform).zip")
    let listing = (do { ^unzip -Z -1 $archive } | complete)
    if $listing.exit_code != 0 { error make {msg: $"Cannot inspect ($archive)"} }
    let binary = $"terraform-provider-routeros_v($version)"
    let binary = (if ($platform | str starts-with 'windows_') { $"($binary).exe" } else { $binary })
    if (($listing.stdout | lines) != [$binary]) { error make {msg: $"Unexpected archive contents: ($archive)"} }
  }
  if $signer != null {
    let fingerprint = ($signer | str uppercase)
    if $fingerprint !~ '^[0-9A-F]{40}$' { error make {msg: "Signer must be a full 40-character OpenPGP fingerprint"} }
    let signature = $"($checksum).sig"
    let verification = (do { ^gpg --batch --status-fd 1 --verify $signature $checksum } | complete)
    let matches = ($verification.stdout | lines | any {|line| $line | str starts-with $"[GNUPG:] VALIDSIG ($fingerprint) " })
    if $verification.exit_code != 0 or not $matches { error make {msg: "Signature invalid or signer fingerprint mismatch"} }
  }
  print $"Verified terraform-provider-routeros ($version) artifacts(char space)(if $signer == null { '(unsigned)' } else { 'and signing fingerprint' })"
}
