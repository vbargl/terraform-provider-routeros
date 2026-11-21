---
page_title: "routeros_wireguard_peer (Resource)"
description: "Manage a peer on the named interface with a public identity and explicit allowed addresses. Allowed addresses may overlap across separate interfaces."
---

# routeros_wireguard_peer (Resource)

Manage a peer on the named interface with a public identity and explicit allowed addresses. Allowed addresses may overlap across separate interfaces.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `interface` | Required |
| `public_key` | Required |
| `allowed_address` | Required |
| `endpoint_address` | Optional |
| `endpoint_port` | Optional, defaults to `0` |
| `persistent_keepalive` | Optional, defaults to `0s` |
| `comment` | Optional |
| `disabled` | Optional, defaults to `no` |
| `id` | Computed |

## Import

```sh
terraform import routeros_wireguard_peer.example '*A'
```
