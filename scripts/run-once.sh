#!/bin/sh
set -eu

go run ./cmd/nightly-snapshot --once --date 2026-09-03 < examples/payment-events.json
