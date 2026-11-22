---
page_title: "routeros_interface_veth (Resource)"
description: "Manage a veth interface for a RouterOS container."
---

# routeros_interface_veth (Resource)

```hcl
resource "routeros_interface_veth" "agent" {
  name    = "veth-agent"
  address = "192.168.88.20/24"
  gateway = "192.168.88.1"
}
```

`name` and `address` (one or more comma-separated prefixes with the host address) are required. `gateway` is an optional IPv4 address. `comment` and `disabled` (default `no`) are optional. To place the container on a LAN segment, for example to send broadcasts there, add the veth as a port of the LAN bridge with [`routeros_interface_bridge_port`](interface_bridge_port.md). Import by API ID.
