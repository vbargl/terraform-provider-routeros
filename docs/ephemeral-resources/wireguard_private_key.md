---
page_title: "routeros_wireguard_private_key (Ephemeral Resource)"
description: "Read a WireGuard private key for an ephemeral consumer."
---

# routeros_wireguard_private_key (Ephemeral Resource)

Enable `ephemeral_access` on a separate provider alias and inject `ROUTEROS_EPHEMERAL_USERNAME` and `ROUTEROS_EPHEMERAL_PASSWORD` at runtime. The dedicated account needs `api,read,sensitive`. Enable it only when needed.

```hcl
provider "routeros" {
  alias            = "key_reader"
  address          = "router.example.net:8729"
  ca_cert_file     = "/etc/ssl/local/router-ca.pem"
  ephemeral_access = true
}

ephemeral "routeros_wireguard_private_key" "selected" {
  provider     = routeros.key_reader
  interface_id = "*A"
}
```

`private_key` is an ephemeral sensitive result. Terraform 1.11+ can pass it to a write-only argument of a compatible consumer. The key leaves the device when the ephemeral resource opens. Only use it with destinations that meet your secret handling requirements and with logging and tracing disabled in that execution environment.
