---
page_title: "routeros_firewall_rule (Resource)"
description: "Manage an IPv4 filter rule with explicit source and destination prefixes and an accept, drop or reject action. Use TCP or UDP to match a destination port. Set place_before to the ID of the immediate successor in the same menu. Omit the attribute to leave ordering unmanaged. An explicit empty string keeps the rule at the end. Rules default to disabled."
---

# routeros_firewall_rule (Resource)

Manage an IPv4 filter rule with explicit source and destination prefixes and an accept, drop or reject action. Use TCP or UDP to match a destination port. Set `place_before` to the ID of the immediate successor in the same menu. Omit the attribute to leave ordering unmanaged. An explicit empty string keeps the rule at the end. Rules default to disabled.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `chain` | Required |
| `action` | Required |
| `src_address` | Required |
| `dst_address` | Required |
| `protocol` | Optional |
| `dst_port` | Optional |
| `in_interface` | Optional |
| `out_interface` | Optional |
| `comment` | Optional |
| `disabled` | Optional, defaults to `yes` |
| `place_before` | Optional |
| `id` | Computed |

## Import

```sh
terraform import routeros_firewall_rule.example '*A'
```
