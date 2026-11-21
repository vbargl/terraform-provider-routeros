---
page_title: "routeros_dhcp_network (Resource)"
description: "Manage DHCP options for an IPv4 network. Supply multiple DNS server addresses as a comma-separated string."
---

# routeros_dhcp_network (Resource)

Manage DHCP options for an IPv4 network. Supply multiple DNS server addresses as a comma-separated string.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `address` | Required |
| `gateway` | Required |
| `dns_server` | Optional |
| `domain` | Optional |
| `comment` | Optional |
| `id` | Computed |

## Import

```sh
terraform import routeros_dhcp_network.example '*A'
```
