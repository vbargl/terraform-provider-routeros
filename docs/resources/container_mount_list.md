---
page_title: "routeros_container_mount_list (Resource)"
description: "Manage a RouterOS container mount list."
---

# routeros_container_mount_list (Resource)

```hcl
resource "routeros_container_mount_list" "agent" {
  name = "agent"
  mount = {
    "/etc/knockerup/kubeconfig" = { src = "/knockerup/kubeconfig", mode = "ro" }
  }
}

resource "routeros_container" "agent" {
  # ...
  mountlists = [routeros_container_mount_list.agent.name]
}
```

RouterOS stores each mount as its own `/container/mounts` entry with a list name. This resource owns every entry with its `name`:

- `mount` maps the absolute destination path in the container to an object with `src` (an absolute path on the router) and `mode` (`ro`, `ro,noexec`, `rw` or `rw,noexec`). Map keys make duplicate destinations impossible. At least one entry is required.
- Applies converge entry by entry. Entries added on the router under this list name, and duplicate or disabled entries, show as drift and are removed or re-enabled on the next apply.
- Creating a list whose name already has entries on the router fails; import it instead. Changing `name` replaces the resource.
- Per-entry `comment` and `disabled` are not managed.

A running container picks up changes only after it restarts; see `routeros_container`.

## Import

```shell
terraform import routeros_container_mount_list.agent agent
```
