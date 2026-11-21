---
page_title: "routeros_dhcp_server (Resource)"
description: "Manage a DHCP server on an existing interface with an existing pool."
---

# routeros_dhcp_server (Resource)

Manage a DHCP server on an existing interface with an existing pool.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `name` | Required |
| `interface` | Required |
| `address_pool` | Required |
| `lease_time` | Required |
| `comment` | Optional |
| `disabled` | Optional, defaults to `no` |
| `id` | Computed |

## Import

```sh
terraform import routeros_dhcp_server.example '*A'
```
