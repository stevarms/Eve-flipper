# syntax=docker/dockerfile:1.7

# ---------- Stage 1: build the React/TS frontend ----------
FROM node:24-alpine AS frontend

WORKDIR /src/frontend

RUN corepack enable

COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    corepack pnpm install --frozen-lockfile

COPY frontend/ ./

ARG VITE_APP_VERSION=dev
ENV VITE_APP_VERSION=${VITE_APP_VERSION}
RUN corepack pnpm run build


# ---------- Stage 2: compile the Go binary ----------
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist

ARG VERSION=dev
ARG ESI_CLIENT_ID=""
ARG ESI_CLIENT_SECRET=""

# ESI_CALLBACK_URL is intentionally NOT baked in. Container operators must
# supply it at runtime via the ESI_CALLBACK_URL env var so the SSO redirect
# lands on the host name they're actually accessing the app through
# (e.g. http://unraid.local:13370/api/auth/callback).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    LDFLAGS="-s -w -X main.version=${VERSION}"; \
    if [ -n "${ESI_CLIENT_ID}" ]; then \
        LDFLAGS="${LDFLAGS} -X main.defaultESIClientID=${ESI_CLIENT_ID}"; \
    fi; \
    if [ -n "${ESI_CLIENT_SECRET}" ]; then \
        LDFLAGS="${LDFLAGS} -X main.defaultESIClientSecret=${ESI_CLIENT_SECRET}"; \
    fi; \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -ldflags "${LDFLAGS}" -o /out/eve-flipper .


# ---------- Stage 3: minimal runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/eve-flipper /usr/local/bin/eve-flipper

# /data holds flipper.db, data/sde/, and $HOME/.config/EveFlipper/vault_machine.key.
# Mount a persistent volume here.
WORKDIR /data
ENV HOME=/data
VOLUME ["/data"]

EXPOSE 13370

ENTRYPOINT ["/usr/local/bin/eve-flipper", "--host", "0.0.0.0"]
