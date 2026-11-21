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
