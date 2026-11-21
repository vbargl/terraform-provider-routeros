variable "address" {
  type = string
}

variable "registry_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

provider "routeros" {
  address = var.address
}

resource "routeros_container_config" "this" {
  registry_url        = "https://registry.example.net"
  username            = "reader"
  tmpdir              = "tmpfs1/pull"
  password_wo         = var.registry_token
  password_wo_version = "1"
}

resource "routeros_interface_veth" "agent" {
  name    = "veth-agent"
  address = "192.168.88.20/24"
  gateway = "192.168.88.1"
}

resource "routeros_container_env" "namespace" {
  list  = "agent"
  key   = "POD_NAMESPACE"
  value = "agents"
}

resource "routeros_container_mount" "kubeconfig" {
  list = "agent"
  src  = "/usb1/agent/kubeconfig"
  dst  = "/etc/agent"
  mode = "ro"
}

resource "routeros_container" "agent" {
  name          = "agent"
  remote_image  = "example/agent:1.0.0"
  interface     = routeros_interface_veth.agent.name
  root_dir      = "tmpfs1/agent"
  envlists      = routeros_container_env.namespace.list
  mountlists    = routeros_container_mount.kubeconfig.list
  start_on_boot = "yes"
  depends_on    = [routeros_container_config.this]
}
