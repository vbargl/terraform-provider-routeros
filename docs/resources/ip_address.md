---
page_title: "routeros_ip_address (Resource)"
description: "Manage a static IPv4 address on an existing interface."
---

# routeros_ip_address (Resource)

Manage a static IPv4 address on an existing interface.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `address` | Required |
| `interface` | Required |
| `comment` | Optional |
| `disabled` | Optional, defaults to `no` |
| `id` | Computed |

## Import

```sh
terraform import routeros_ip_address.example '*A'
```
