---
page_title: "routeros_interface_bridge_port (Resource)"
description: "Manage a static port of a RouterOS bridge."
---

# routeros_interface_bridge_port (Resource)

```hcl
resource "routeros_interface_bridge_port" "agent" {
  bridge    = "bridge"
  interface = routeros_interface_veth.agent.name
  edge      = "yes"
}
```

`bridge` and `interface` are required. `edge` (default `auto`; `auto`, `no`, `no-discover`, `yes` or `yes-discover`), `comment` and `disabled` (default `no`) are optional. Set `edge = "yes"` for a container veth: with `auto`, a restarted container could not reach the LAN for about 15 s while (R)STP brought the port back to forwarding. Other port settings (PVID, STP priority, horizon, hardware offload) are not managed and keep their RouterOS values. Dynamic ports, for example from wireless interfaces, cannot be managed or imported. Import by API ID, for example a port added by hand before the resource existed.
