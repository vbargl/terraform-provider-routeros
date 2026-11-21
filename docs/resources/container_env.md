---
page_title: "routeros_container_env (Resource)"
description: "Manage one environment variable in a RouterOS container env list."
---

# routeros_container_env (Resource)

```hcl
resource "routeros_container_env" "namespace" {
  list  = "agent"
  key   = "POD_NAMESPACE"
  value = "agents"
}
```

`list`, `key` and `value` are required; `comment` and `disabled` (default `no`) are optional. Reference the list from `routeros_container.envlists`. `value` is stored in Terraform state and is readable on the router, so use it for non-secret values only. Import by API ID.
