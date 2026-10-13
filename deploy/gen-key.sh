#!/usr/bin/env sh
# Prints a fresh NEXMESH_MASTER_KEY value (32 random bytes, base64).
set -eu
openssl rand -base64 32
