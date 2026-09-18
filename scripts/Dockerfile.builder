# Build/test tools only. This is NOT a custom Bifrost server image.
ARG GO_BUILDER
FROM ${GO_BUILDER}
RUN apk add --no-cache bash make git curl gcc musl-dev binutils
WORKDIR /workspace
ENV GOTOOLCHAIN=local GOWORK=off
