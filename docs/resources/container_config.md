---
page_title: "routeros_container_config (Resource)"
description: "Manage the RouterOS container configuration."
---

# routeros_container_config (Resource)

```hcl
variable "registry_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "routeros_container_config" "this" {
  registry_url        = "https://registry.example.net"
  username            = "reader"
  tmpdir              = "tmpfs1/pull"
  password_wo         = var.registry_token
  password_wo_version = "1"
}
```

Manages the `/container/config` singleton: `registry_url`, `username`, `tmpdir` and `layer_dir`. Omit an attribute to leave the router's value unmanaged.

`password_wo` is a write-only attribute (Terraform 1.11+ or OpenTofu 1.11+). It never enters plan or state, and the provider never reads the password back. It is sent on create and whenever `password_wo_version` changes, so change the version when you rotate the token. Use a read-only registry token. Anyone with `sensitive` access to the router can still read it there.

Destroy removes the resource from state only. The router keeps its configuration, including the password. Import with the ID `container-config`.
