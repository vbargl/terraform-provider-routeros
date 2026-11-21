variable "address" {
  type = string
}

resource "routeros_wireguard_interface" "current" {
  name        = "wg-test-current"
  listen_port = "54231"
  mtu         = "1420"
  disabled    = "yes"
}

resource "routeros_wireguard_interface" "next" {
  name        = "wg-test-next"
  listen_port = "54232"
  mtu         = "1420"
  disabled    = "yes"
}

resource "routeros_wireguard_peer" "peer" {
  for_each        = toset(["wg-test-current", "wg-test-next"])
  interface       = each.key
  public_key      = "9/G8edP83dtfwi0aUVlmvBo6m3WRHh1JvOs8XwOPGww="
  allowed_address = "192.0.2.5/32"
}

output "interface_names" {
  value = [routeros_wireguard_interface.current.name, routeros_wireguard_interface.next.name]
}
output "interface_attributes" {
  value = keys(routeros_wireguard_interface.current)
}
output "peer_attributes" {
  value = keys(routeros_wireguard_peer.peer["wg-test-current"])
}
