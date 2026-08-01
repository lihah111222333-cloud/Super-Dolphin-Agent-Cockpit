package main

const remoteBaselineSeedBootstrapScript = `#!/bin/sh
set -eu
seed=/input/seed.sh
test -f "$seed"
test "$(wc -c < "$seed" | tr -d ' ')" = "$BASELINE_SEED_SCRIPT_SIZE"
printf '%s  %s\n' "$BASELINE_SEED_SCRIPT_SHA256" "$seed" | sha256sum -c -
exec /bin/sh "$seed"
`
