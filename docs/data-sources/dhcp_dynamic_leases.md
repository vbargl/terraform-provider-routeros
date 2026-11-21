---
page_title: "routeros_dhcp_dynamic_leases (Data Source)"
description: "Observe dynamic DHCP leases."
---

# routeros_dhcp_dynamic_leases (Data Source)

```hcl
data "routeros_dhcp_dynamic_leases" "observed" {}
```

The `leases` result lists dynamic entries with `id`, `address`, `mac_address`, `server` and `status`. A lease observation reflects the last refresh. Configure a `routeros_dhcp_lease` to manage a separate static reservation.
