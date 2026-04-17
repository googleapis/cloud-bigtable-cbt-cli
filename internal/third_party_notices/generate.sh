#!/bin/bash

cd "$(dirname $0)"
TEMPLATE="$(pwd)/notices.tpl"
ROOT="../../"

# Find the 3rd-party packages.
IGNORE="$(go run . "${ROOT?}/go.mod" | sed 's/^/--ignore /')"

# Download all modules.
cd "${ROOT?}"
go mod download

go install github.com/google/go-licenses@latest

# Check that there are compatible licenses.
TOOL="$(go env GOPATH)/bin/go-licenses"
$TOOL check cloud.google.com/go/cbt \
  ${IGNORE} \
  || exit $?

# Report the licenses into THIRD_PARTY_NOTICES.txt
$TOOL report cloud.google.com/go/cbt \
  --template "${TEMPLATE?}" \
  ${IGNORE} \
  > THIRD_PARTY_NOTICES.txt 2>/dev/null

echo
echo "================================="
echo "= PLEASE VERIFY ERRORS (above!) ="
echo "================================="
