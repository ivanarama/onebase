#!/usr/bin/env bash
# Run against a Linux OneBase binary. Only the package installation needs internet.
set -euo pipefail
binary=$(realpath "${1:?Usage: e2e-ubuntu-kerberos.sh /path/to/linux/onebase}")
test -x "$binary"
fixtures=$(cd "$(dirname "$0")/../e2e/kerberos" && pwd)
image=$(docker build --quiet "$fixtures")
network=$(docker network create --internal "onebase-kerberos-$(openssl rand -hex 8)")
server=''
cleanup() {
    if [[ -n "$server" ]]; then docker rm --force "$server" >/dev/null; fi
    docker network rm "$network" >/dev/null
}
trap cleanup EXIT
export TEST_AD_PASSWORD="Aa1!$(openssl rand -hex 20)"
export TEST_ADMIN_PASSWORD="Bb2!$(openssl rand -hex 20)"
server=$(docker run -d --network "$network" --hostname dc \
    --network-alias dc.onebase.test --network-alias erp.onebase.test \
    --mount "type=bind,src=$binary,dst=/usr/local/bin/onebase,readonly" \
    -e TEST_AD_PASSWORD -e TEST_ADMIN_PASSWORD "$image" bash /test/server.sh)

for ((i=0; i<90; i++)); do
    if docker exec "$server" test -e /tmp/domain-ready &&
        docker exec "$server" curl -fsS http://127.0.0.1:8080/healthz >/dev/null; then
        break
    fi
    if [[ "$(docker inspect --format '{{.State.Running}}' "$server")" != true || $i -eq 89 ]]; then
        docker logs "$server" >&2
        exit 1
    fi
    sleep 1
done

# The client receives only the public TLS certificate and its test credentials.
docker exec "$server" cat /tmp/erp.crt | docker run --rm -i \
    --network "$network" --hostname workstation \
    -e TEST_AD_PASSWORD -e TEST_ADMIN_PASSWORD "$image" python3 /test/client.py
