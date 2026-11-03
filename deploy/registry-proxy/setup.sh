#!/usr/bin/env bash
# Sets up what dependency installation jobs need: an INTERNAL Docker network (no
# route to the host or the internet) and the registry proxy, which is the only
# container on it that can reach the internet, and only for package registries.
#
# Run from the repository root, on the host where the execution worker runs:
#   bash deploy/registry-proxy/setup.sh
# Then give the worker (and the API server) the same two settings:
#   CLOUD_EXEC_INSTALL_NETWORK=cloudexec-install
#   CLOUD_EXEC_INSTALL_PROXY=cloudexec-registry-proxy:3128
# It is safe to run again; it rebuilds the image and replaces the proxy container.
set -euo pipefail

IMAGE="${CLOUD_EXEC_PROXY_IMAGE:-cloudexec-registry-proxy:1}"
NETWORK="${CLOUD_EXEC_INSTALL_NETWORK:-cloudexec-install}"
NAME="cloudexec-registry-proxy"

docker build -f deploy/registry-proxy/Dockerfile -t "$IMAGE" .

if docker network inspect "$NETWORK" >/dev/null 2>&1; then
  if [ "$(docker network inspect --format '{{.Internal}}' "$NETWORK")" != "true" ]; then
    echo "The network $NETWORK exists but is not internal. Remove it (docker network rm $NETWORK) and run this again." >&2
    exit 1
  fi
else
  docker network create --internal "$NETWORK" >/dev/null
fi

docker rm -f "$NAME" >/dev/null 2>&1 || true
# Starts on the internal network only; the default bridge is added afterwards to give
# the proxy (and only the proxy) a way out.
docker run -d --name "$NAME" --restart unless-stopped --network "$NETWORK" \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  --memory 128m --pids-limit 64 "$IMAGE" >/dev/null
docker network connect bridge "$NAME"

echo "Registry proxy $NAME is running on network $NETWORK."
echo "Set CLOUD_EXEC_INSTALL_NETWORK=$NETWORK and CLOUD_EXEC_INSTALL_PROXY=$NAME:3128 for the worker and the API server."
