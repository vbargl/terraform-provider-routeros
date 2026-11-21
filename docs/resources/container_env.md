---
page_title: "routeros_container_env (Resource)"
description: "Manage one environment variable in a RouterOS container env list."
---

# routeros_container_env (Resource)

```hcl
resource "routeros_container_env" "agent" {
  for_each = {
    POD_NAMESPACE = "agents"
    POOL          = "suspendable"
  }
  list  = "agent"
  key   = each.key
  value = each.value
}
```

`list`, `key` and `value` are required; `comment` and `disabled` (default `no`) are optional. Each resource is one entry; a list holds as many entries as share its `list` name. Reference the list from `routeros_container.envlists`. List names cannot contain commas or spaces.

Each key may appear only once in a list. Before writing, the provider reads the list and rejects a second entry with the same key, naming the existing RouterOS object, so the container never gets an ambiguous value. The same key in another list is allowed. `value` is stored in Terraform state and is readable on the router, so use it for non-secret values only. Import by API ID.
