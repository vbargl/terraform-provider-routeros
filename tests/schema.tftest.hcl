mock_provider "routeros" {}

variables {
  address = "127.0.0.1:8729"
}

run "separate_generations" {
  command = plan

  module {
    source = "./tests/fixtures/generations"
  }

  assert {
    condition     = join(",", output.interface_names) == "wg-test-current,wg-test-next"
    error_message = "Each generation must have its own interface."
  }

  assert {
    condition     = !contains(output.interface_attributes, "private_key")
    error_message = "Managed interfaces must not expose a private-key attribute."
  }

  assert {
    condition     = !contains(output.peer_attributes, "private_key") && !contains(output.peer_attributes, "preshared_key")
    error_message = "Managed peers must not expose private keys or preshared keys."
  }
}

run "container_prerequisites_met" {
  command = plan

  module {
    source = "./tests/fixtures/prerequisites"
  }

  override_data {
    target = data.routeros_device_mode.current
    values = { mode = "advanced", container = true, flagged = false }
  }

  override_data {
    target = data.routeros_package.container
    values = { installed = true, disabled = false, version = "7.25.1", scheduled = "" }
  }
}

run "container_mode_disabled" {
  command = plan

  module {
    source = "./tests/fixtures/prerequisites"
  }

  override_data {
    target = data.routeros_device_mode.current
    values = { mode = "advanced", container = false, flagged = false }
  }

  override_data {
    target = data.routeros_package.container
    values = { installed = true, disabled = true, version = "7.25.1", scheduled = "" }
  }

  expect_failures = [terraform_data.container_host]
}

run "container_workload" {
  command = plan

  module {
    source = "./tests/fixtures/container"
  }

  variables {
    registry_token = "not-a-real-token"
  }

  assert {
    condition     = routeros_container.agent.interface == "veth-agent" && routeros_container.agent.envlists == "agent" && routeros_container.agent.mountlists == "agent"
    error_message = "The container must reference its veth, environment and mount lists."
  }

  assert {
    condition     = routeros_container_config.this.password_wo == null
    error_message = "The registry password is write-only and must not appear in the plan."
  }
}
