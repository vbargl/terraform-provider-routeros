---
page_title: "routeros_ip_route (Resource)"
description: "Manage a static IPv4 route. Reference an existing routing table if using a table other than main."
---

# routeros_ip_route (Resource)

Manage a static IPv4 route. Reference an existing routing table if using a table other than main.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `dst_address` | Required |
| `gateway` | Required |
| `routing_table` | Optional, defaults to `main` |
| `distance` | Optional, defaults to `1` |
| `comment` | Optional |
| `disabled` | Optional, defaults to `no` |
| `id` | Computed |

## Import

```sh
terraform import routeros_ip_route.example '*A'
```
