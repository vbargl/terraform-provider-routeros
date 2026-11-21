# RouterOS Terraform provider

Manage a scoped set of RouterOS 7 network objects over certificate-verified API-SSL. Managed WireGuard interfaces generate their keys on the router and expose the public identity. A separate opt-in ephemeral resource can read a private key through a dedicated RouterOS account.

This provider is under development. The initial CHR qualification target is RouterOS 7.24.6. Consult the [design](docs/design.md) for scope and qualification status and the [provider documentation](docs/index.md) for configuration, imports and recovery.

## Verification

With Go 1.25+, Terraform 1.11+, and Nushell 0.116.1 available, run:

```sh
CGO_ENABLED=0 go test ./...
nu --no-config-file tests/run-native.nu
```

The [CHR acceptance instructions](integration/README.md) describe the disposable-router tests. The [release notes](docs/release.md) describe packaging and signing.

CI runs `golangci-lint` on pull requests and pushes to `main`. Run
`golangci-lint run` locally with v2.15.0 to match CI.

## Repository layout

- `cmd/terraform-provider-routeros/main.go` starts the Terraform plugin server. Build it with `go build ./cmd/terraform-provider-routeros`.
- `internal/provider/` contains the provider, RouterOS client and Go unit tests.
- `tests/` contains native Terraform tests and their fixtures.
- `integration/` contains the Terraform lifecycle test against a local API-SSL test double and the disposable CHR acceptance suite.
- `examples/` contains user-facing Terraform configurations. `docs/` contains Registry documentation and project notes.
