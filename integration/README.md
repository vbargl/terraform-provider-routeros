# Isolated RouterOS acceptance

Use a disposable CHR 7.24.6 with a preconfigured API-SSL certificate and restricted `api,read,write` account. Expose API-SSL through a loopback-only tunnel to that VM. Ensure the certificate covers the loopback hostname used for the test. Keep the CHR on an isolated network and take a snapshot first. Use a new lab instance or restore the snapshot between runs.

Build the provider and configure a Terraform CLI development override for `vbargl/routeros` pointing to the directory containing the binary. Set `TF_CLI_CONFIG_FILE` to that override, `TF_VAR_address` to the loopback endpoint, `TF_VAR_ca_cert_file` to the absolute lab CA path, and `ROUTEROS_DISPOSABLE_CHR=yes`. Inject the restricted lab credentials through the environment. Run `nu --no-config-file integration/chr.nu`.

The script creates a disabled WireGuard interface, checks plan and state artifacts, adopts the existing interface, updates and refreshes it, simulates an external writer using a separate state copy, checks an authentication failure and destroys the fixture. The public identity must remain unchanged. If the script stops early, inspect the disposable VM for `wg-tf-acceptance` or restore the snapshot.
