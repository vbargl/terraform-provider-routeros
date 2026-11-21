#!/usr/bin/env nu

let root = ($env.FILE_PWD | path dirname)
let terraform = ($env.TERRAFORM_BIN? | default terraform)
let work = (^mktemp -d | str trim)

try {
    cp ($root | path join integration versions.tf) $work
    cp -r ($root | path join tests) ($work | path join tests)

    let os = (^go env GOOS | str trim)
    let arch = (^go env GOARCH | str trim)
    let platform = $"($os)_($arch)"
    let mirror = ($work | path join mirror)
    let provider = ($mirror | path join registry.terraform.io vbargl routeros 0.0.1 $platform)
    mkdir $provider
    do {
        cd $root
        $env.CGO_ENABLED = '0'
        ^go build -o ($provider | path join terraform-provider-routeros_v0.0.1) ./cmd/terraform-provider-routeros
    }

    $env.TF_CLI_CONFIG_FILE = ($work | path join terraform.rc)
    let path_literal = ($mirror | to json --raw)
    ['provider_installation {' '  filesystem_mirror {' $"    path = ($path_literal)" '  }' '}']
    | str join (char nl)
    | save $env.TF_CLI_CONFIG_FILE

    ^$terraform $"-chdir=($work)" init -no-color
    ^$terraform $"-chdir=($work)" test -no-color
} catch {|failure|
    rm -rf $work
    error make {msg: $failure.msg}
}
rm -rf $work
