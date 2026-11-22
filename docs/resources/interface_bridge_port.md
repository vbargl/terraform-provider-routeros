---
page_title: "routeros_interface_bridge_port (Resource)"
description: "Manage a static port of a RouterOS bridge."
---

# routeros_interface_bridge_port (Resource)

```hcl
resource "routeros_interface_bridge_port" "agent" {
  bridge    = "bridge"
  interface = routeros_interface_veth.agent.name
}
```

`bridge` and `interface` are required. `comment` and `disabled` (default `no`) are optional. Other port settings (PVID, STP, horizon, hardware offload) are not managed and keep their RouterOS values. Dynamic ports, for example from wireless interfaces, cannot be managed or imported. Import by API ID, for example a port added by hand before the resource existed.
