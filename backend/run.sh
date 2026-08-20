#!/bin/bash
set -a
cd "$(dirname "$0")"
source ./.env
set +a
exec ./servergo-backend
