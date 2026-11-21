---
page_title: "routeros_ip_pool (Resource)"
description: "Manage an IPv4 address pool using RouterOS range syntax."
---

# routeros_ip_pool (Resource)

Manage an IPv4 address pool using RouterOS range syntax.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `name` | Required |
| `ranges` | Required |
| `comment` | Optional |
| `id` | Computed |

## Import

```sh
terraform import routeros_ip_pool.example '*A'
```
