#!/bin/bash
set -eu -o pipefail

: "${BIND_ON_IP:=$(hostname -i | awk '{print $1}')}"
export BIND_ON_IP

if [[ "${BIND_ON_IP}" == "0.0.0.0" || "${BIND_ON_IP}" == "::0" ]]; then
    : "${TEMPORAL_BROADCAST_ADDRESS:=$(hostname -i | awk '{print $1}')}"
    export TEMPORAL_BROADCAST_ADDRESS
fi

if [[ -z "${TEMPORAL_ADDRESS:-}" ]]; then
    if [[ "${BIND_ON_IP}" =~ ":" ]]; then
        export TEMPORAL_ADDRESS="[${BIND_ON_IP}]:7233"
    else
        export TEMPORAL_ADDRESS="${BIND_ON_IP}:7233"
    fi
fi

if [[ -z "${TEMPORAL_CLI_ADDRESS:-}" ]]; then
    export TEMPORAL_CLI_ADDRESS="${TEMPORAL_ADDRESS}"
fi

exec /usr/local/bin/temporal-server "$@"
