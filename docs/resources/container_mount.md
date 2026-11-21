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

`list`, `src`, `dst` and `mode` (`ro`, `ro,noexec`, `rw` or `rw,noexec`) are required. Paths are absolute. `comment` and `disabled` (default `no`) are optional. Each resource is one entry; a list holds as many entries as share its `list` name. Reference the list from `routeros_container.mountlists`. List names cannot contain commas or spaces.

Each `dst` may appear only once in a list. Before writing, the provider reads the list and rejects a second entry with the same destination, naming the existing RouterOS object. The same destination in another list is allowed. Import by API ID.
