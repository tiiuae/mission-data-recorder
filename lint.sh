#!/bin/bash

set +x -eo pipefail 
if [[ $CI == true ]]; then
    go generate
fi
export lint_dirs=$(
  go list -f '{{.Dir}}' ./... \
  | xargs realpath --relative-to=. \
  | grep -v '^msgs' \
  | sed -e 's/^/.\//' \
  | tr '\n' ' '
)
if [[ $CI == true ]]; then
    env > "$GITHUB_ENV"
    set -x
    go vet -vettool=./checklocks.sh $lint_dirs
else
    set -x
    go vet -vettool=./checklocks.sh $lint_dirs
    golangci-lint run $lint_dirs
fi
