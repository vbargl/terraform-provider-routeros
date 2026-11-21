---
page_title: "routeros_dhcp_lease (Resource)"
description: "Manage a static DHCP reservation for a MAC address. Dynamic entries cannot be adopted."
---

# routeros_dhcp_lease (Resource)

Manage a static DHCP reservation for a MAC address. Dynamic entries cannot be adopted.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `address` | Required |
| `mac_address` | Required |
| `server` | Required |
| `comment` | Optional |
| `disabled` | Optional, defaults to `no` |
| `id` | Computed |

## Import

```sh
terraform import routeros_dhcp_lease.example '*A'
```
