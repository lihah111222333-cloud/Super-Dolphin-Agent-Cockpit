#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../lib/assertions.sh"
source "$SCRIPT_DIR/../lib/string_utils.sh"

assert_equals "$(trim_string '  hello  ')" "hello"
assert_equals "$(upper_string 'hello')" "HELLO"
