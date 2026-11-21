---
page_title: "routeros_container_env_list (Resource)"
description: "Manage a RouterOS container environment list."
---

# routeros_container_env_list (Resource)

```hcl
resource "routeros_container_env_list" "agent" {
  name = "agent"
  env = {
    KNOCKERUP_NAMESPACE = "knockerup"
    HOME                = "/"
  }
}

resource "routeros_container" "agent" {
  # ...
  envlists = [routeros_container_env_list.agent.name]
}
```

RouterOS stores each variable as its own `/container/envs` entry with a list name. This resource owns every entry with its `name`:

- `env` maps variable names to values. Map keys make duplicate names impossible. Names use letters, digits and underscores and do not start with a digit. At least one entry is required, because a RouterOS list exists only through its entries.
- Applies converge entry by entry: changed values are set in place, new variables are added and removed ones are deleted. Unchanged entries keep their RouterOS IDs.
- Entries added on the router under this list name show as drift and are removed on the next apply. So are duplicate or disabled entries, which apply re-enables or deduplicates. Do not manage the same list name with two resources.
- Creating a list whose name already has entries on the router fails; import it instead. Changing `name` replaces the resource.
- Per-entry `comment` and `disabled` are not managed.

Values are stored in Terraform state and are readable on the router. Use non-secret values only, and mount secret files read-only with `routeros_container_mount_list`.

A running container picks up changes only after it restarts; see `routeros_container`.

## Import

```shell
terraform import routeros_container_env_list.agent agent
```
