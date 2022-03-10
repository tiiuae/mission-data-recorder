#!/bin/bash
exec go run gvisor.dev/gvisor/tools/checklocks/cmd/checklocks@go "$@"
