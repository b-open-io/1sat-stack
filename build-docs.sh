#!/bin/bash
# Generates per-service OpenAPI fragments into pkg/<svc>/docs/swagger.json.
# Fragments carry service-relative paths; the registrar rebases them onto
# each service's mount prefix and serves the merged document at
# {base}/api-spec/swagger.json.
set -e

SWAG_VERSION="v1.16.6"
SWAG="$(go env GOPATH)/bin/swag"

if [ ! -x "$SWAG" ] || ! "$SWAG" --version 2>/dev/null | grep -q "${SWAG_VERSION#v}"; then
    echo "Installing swag ${SWAG_VERSION}..."
    go install "github.com/swaggo/swag/cmd/swag@${SWAG_VERSION}"
fi

# service:anchor pairs; anchor is the file holding that package's annotations
SERVICES="
beef:routes.go
pubsub:routes.go
txo:routes.go
owner:routes.go
bsv21:routes.go
bap:routes.go
bsocial:routes.go
opns:routes.go
ordlock:routes.go
ordfs:routes.go
chaintracks:swagger.go
paymail:routes.go
broadcast:routes.go
"

# dir:anchor pairs for packages outside pkg/
EXTRA_SERVICES="
admin:routes.go
"

generate() {
    dir="$1"
    anchor="$2"
    echo "Generating ${dir}/docs/swagger.json"
    name="$(basename "$dir")"
    "$SWAG" init \
        --dir "$dir" \
        --generalInfo "$anchor" \
        --output "${dir}/docs" \
        --outputTypes json \
        --instanceName "$name" \
        --parseDependencyLevel 1 \
        --quiet
    # swag names the file <instanceName>_swagger.json when instanceName != "swagger"
    if [ -f "${dir}/docs/${name}_swagger.json" ]; then
        mv "${dir}/docs/${name}_swagger.json" "${dir}/docs/swagger.json"
    fi
}

for entry in $SERVICES; do
    generate "pkg/${entry%%:*}" "${entry##*:}"
done
for entry in $EXTRA_SERVICES; do
    generate "${entry%%:*}" "${entry##*:}"
done

echo "Done. Fragments are embedded via pkg/<svc>/docs/embed.go."
