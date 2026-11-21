---
page_title: "routeros_nat_rule (Resource)"
description: "Manage WireGuard-scoped IPv4 NAT with explicit source and destination prefixes narrower than /0 and a single IPv4 translation target. For srcnat use src-nat or netmap and set out_interface to the WireGuard interface. For dstnat use dst-nat or netmap and set in_interface to the WireGuard interface. Set place_before to the ID of a rule that must appear later in the same menu. Several rules may share this anchor. Omit the attribute to leave ordering unmanaged. Rules default to disabled."
---

# routeros_nat_rule (Resource)

Manage WireGuard-scoped IPv4 NAT with explicit source and destination prefixes narrower than /0 and a single IPv4 translation target. For srcnat use src-nat or netmap and set out_interface to the WireGuard interface. For dstnat use dst-nat or netmap and set in_interface to the WireGuard interface. Set `place_before` to the ID of a rule that must appear later in the same menu. Several rules may share this anchor. Omit the attribute to leave ordering unmanaged. Rules default to disabled.

The resource uses RouterOS string representations.

| Attribute | Use |
| --- | --- |
| `chain` | Required |
| `action` | Required |
| `src_address` | Required |
| `dst_address` | Required |
| `to_addresses` | Required |
| `in_interface` | Optional |
| `out_interface` | Optional |
| `comment` | Optional |
| `disabled` | Optional, defaults to `yes` |
| `place_before` | Optional |
| `id` | Computed |

## Import

```sh
terraform import routeros_nat_rule.example '*A'
```
