#!/usr/bin/env nu

def --wrapped tf [...args: string] {
    let terraform = ($env.TERRAFORM_BIN? | default terraform)
    ^$terraform ...$args -no-color
}

def --wrapped tf-result [...args: string] {
    let terraform = ($env.TERRAFORM_BIN? | default terraform)
    ^$terraform ...$args -no-color | complete
}

def require [condition: bool, message: string] {
    if not $condition { error make {msg: $message} }
}

def check-clean [contents: string] {
    for field in [private-key private_key preshared-key preshared_key] {
        require (not ($contents | str contains $field)) 'A key field appeared in an ordinary Terraform artifact'
    }
    for variable in [ROUTEROS_PASSWORD ROUTEROS_EPHEMERAL_PASSWORD] {
        let value = ($env | get -o $variable | default '')
        if ($value | is-not-empty) {
            require (not ($contents | str contains $value)) 'A credential appeared in an ordinary Terraform artifact'
        }
    }
}

require ($env.ROUTEROS_DISPOSABLE_CHR? == 'yes') 'Use only a dedicated disposable CHR and set ROUTEROS_DISPOSABLE_CHR=yes'
let address = ($env.TF_VAR_address? | default '')
require ($address =~ '^(127\.0\.0\.1|localhost):[0-9]+$') 'Set TF_VAR_address to the loopback CHR endpoint'
for variable in [TF_VAR_ca_cert_file ROUTEROS_USERNAME ROUTEROS_PASSWORD TF_CLI_CONFIG_FILE] {
    require ($env | get -o $variable | default '' | is-not-empty) $"Set ($variable) before running this test"
}
hide-env -i TF_LOG TF_LOG_CORE TF_LOG_PROVIDER TF_LOG_PATH

let fixture = $env.FILE_PWD
let work = (^mktemp -d | str trim)

try {
    cp ($fixture | path join main.tf) $work
    cp ($fixture | path join versions.tf) $work

    do {
        cd $work
        tf plan -out=create.plan
        check-clean (tf show -json create.plan)
        tf apply create.plan
        # Refresh recovers the public identity if the initial read-back failed.
        tf apply -refresh-only -auto-approve
        tf plan -detailed-exitcode
        let id = (tf output -raw interface_id)
        let public_key = (tf output -raw public_key)
        require ($public_key | is-not-empty) 'Expected a public key after creating the interface'
        check-clean (open --raw terraform.tfstate)

        # Forget only the local binding and adopt the same interface.
        tf state rm routeros_wireguard_interface.test
        tf import routeros_wireguard_interface.test $id
        require (tf state show routeros_wireguard_interface.test | str contains $public_key) 'Import changed the public identity'
        with-env {TF_VAR_comment: updated} {
            tf apply -auto-approve
            tf plan -detailed-exitcode
        }
        require ((tf output -raw public_key) == $public_key) 'Update changed the public identity'
        check-clean (open --raw terraform.tfstate)
        check-clean (open --raw terraform.tfstate.backup)

        # Change the managed comment using a separate state copy.
        cp terraform.tfstate external.tfstate
        with-env {TF_VAR_comment: external} { tf apply -state=external.tfstate -auto-approve }
        let drift = (with-env {TF_VAR_comment: updated} { tf-result plan -refresh-only -detailed-exitcode })
        require ($drift.exit_code == 2) 'Expected Terraform to detect out-of-band drift'
        check-clean $drift.stdout
        tf apply -auto-approve
        require ((tf output -raw public_key) == $public_key) 'Reconciliation changed the public identity'

        # Capture the rejected authentication attempt without printing its contents.
        let rejected_credential = 'tf-test-rejected-credential'
        let rejected = (with-env {ROUTEROS_PASSWORD: $rejected_credential} { tf-result plan })
        let log = ($rejected.stdout + $rejected.stderr)
        require ($rejected.exit_code != 0 and ($log | str contains 'API-SSL authentication failed')) 'Expected an authentication failure'
        require (not ($log | str contains $rejected_credential)) 'Terraform printed the rejected credential'

        tf plan -destroy -out=destroy.plan
        check-clean (tf show -json destroy.plan)
        tf apply destroy.plan
        check-clean (open --raw terraform.tfstate)
    }
} catch {|failure|
    rm -rf $work
    error make {msg: $failure.msg}
}
rm -rf $work
