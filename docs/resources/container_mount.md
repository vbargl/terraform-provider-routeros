---
page_title: "routeros_container_mount (Resource)"
description: "Manage one mount in a RouterOS container mount list."
---

# routeros_container_mount (Resource)

```hcl
resource "routeros_container_mount" "kubeconfig" {
  list = "agent"
  src  = "/usb1/agent/kubeconfig"
  dst  = "/etc/agent"
  mode = "ro"
}
```

`list`, `src`, `dst` and `mode` (`ro`, `ro,noexec`, `rw` or `rw,noexec`) are required. Paths are absolute. `comment` and `disabled` (default `no`) are optional. Reference the list from `routeros_container.mountlists`. Import by API ID.
