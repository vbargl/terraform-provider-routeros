---
page_title: "routeros_wireguard_interface (Resource)"
description: "Create a WireGuard interface with a device-generated identity. Create a separate interface for each generation and coordinate cutover before destroying the old interface."
---

# routeros_wireguard_interface (Resource)

Create a WireGuard interface with a device-generated identity. Create a separate interface for each generation and coordinate cutover before destroying the old interface.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `name` | Required |
| `listen_port` | Required |
| `mtu` | Required |
| `comment` | Optional |
| `disabled` | Optional, defaults to `no` |
| `public_key` | Computed |
| `id` | Computed |

## Import

```sh
terraform import routeros_wireguard_interface.example '*A'
```
