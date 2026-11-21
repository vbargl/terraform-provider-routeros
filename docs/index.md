---
page_title: "RouterOS Provider"
description: "Manage RouterOS 7 over certificate-verified API-SSL."
---

# RouterOS Provider

```hcl
terraform {
  required_version = ">= 1.11"
  required_providers {
    routeros = {
      source = "vbargl/routeros"
    }
  }
}

provider "routeros" {
  address      = "router.example.net:8729"
  ca_cert_file = "/etc/ssl/local/router-ca.pem"
}
```

Set `ROUTEROS_USERNAME` and `ROUTEROS_PASSWORD` in the provider process environment using an external secret source. Use an API-SSL certificate with a SAN matching the configured address. `ca_cert_file` adds a PEM CA to system trust. Restrict API-SSL and the automation account to the management network. The provider schema has no password arguments.

## Adoption and recovery

Import static objects by their RouterOS API ID (for example, `*A`), quoted to prevent shell globbing. Obtain IDs through an API `print` projecting `.id` and a nonsecret identifying field. Verify the object and device before importing. Reproduce its observed settings in HCL, including a rule successor if you want Terraform to enforce its position, and inspect the plan before applying. Older state from other providers may contain private keys and should be treated as secret material. Re-import into fresh state and handle any prior copies as a separate recovery operation.

If a write is interrupted, refresh, inspect the object on the router and adopt it if creation succeeded before retrying. If creation returns an ID and the verification read fails, the provider retains the binding and emits a warning. Run a refresh-only apply before using the public key in dependent resources. If an earlier failed creation left an existing interface tainted, verify the router ID and public key and use `terraform untaint` before applying again. If a rule update succeeded but moving it failed, inspect and retry against the refreshed position. Losing configuration, device backups or an interface can require creating a new identity and coordinating its public key with peers.

RouterOS values are strings, including numeric settings and `yes` / `no` flags. Use the canonical values from a filtered read when adopting existing resources. Retain an independent management path during firewall changes.
