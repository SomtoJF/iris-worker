# Build from repo root: docker build -f docker/Dockerfile .
# Runtime needs Temporal, Redis, OpenRouter, AWS, etc. via environment (use RENDER=true to skip .env file).
# SQLite: $HOME/iris/db/gorm.db — mount /app/iris/db if you need a persistent DB.
#
# Final stage uses Debian (not Ubuntu) so Chromium installs from apt; Ubuntu 24’s Chromium is typically snap-only in Docker.

FROM golang:1.25.3-bookworm AS builder

WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev \
	&& rm -rf /var/lib/apt/lists/*

# Deps are vendored (vendor/ includes the replaced ../iris-api module),
# so no `go mod download` — it would try to resolve the replace path.
COPY . .

ENV CGO_ENABLED=1
RUN go build -mod=vendor -o /out/iris-worker main.go

FROM debian:bookworm-slim

# Unpinned: Debian drops superseded versions from the repo, so pinning breaks builds
# on every chromium security update (147 pin failed 2026-07). The 150.0.7871.46 SIGTRAP
# crash is fixed in 150.0.7871.100 (verified headless in bookworm-slim container).
# If a future version crashes: chromium --headless --no-sandbox --dump-dom about:blank
RUN apt-get update && apt-get install -y --no-install-recommends \
	ca-certificates \
	chromium \
	chromium-common \
	chromium-sandbox \
	fonts-liberation \
	libasound2 \
	libatk-bridge2.0-0 \
	libatk1.0-0 \
	libcups2 \
	libdbus-1-3 \
	libdrm2 \
	libgbm1 \
	libgtk-3-0 \
	libnspr4 \
	libnss3 \
	libxcomposite1 \
	libxdamage1 \
	libxfixes3 \
	libxkbcommon0 \
	libxrandr2 \
	libsqlite3-0 \
	&& rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --home-dir /app --shell /usr/sbin/nologin --uid 1000 appuser

WORKDIR /app

COPY --from=builder /out/iris-worker /app/iris-worker
COPY --from=builder /src/workflow /app/workflow

RUN mkdir -p /app/iris/db && chown -R appuser:appuser /app

USER appuser

ENV HOME=/app
ENV RENDER=true
ENV WORKER_ROOT=/app

# Point Rod at system Chromium (defaults already enable headless + no-sandbox when running in a container).
CMD ["/app/iris-worker", "-rod=bin=/usr/bin/chromium"]
