# Build from repo root: docker build -f docker/Dockerfile .
# Runtime needs Temporal, Redis, OpenRouter, AWS, etc. via environment (use RENDER=true to skip .env file).
# SQLite: $HOME/iris/db/gorm.db — mount /app/iris/db if you need a persistent DB.
#
# Final stage uses Debian (not Ubuntu) so Chromium installs from apt; Ubuntu 24’s Chromium is typically snap-only in Docker.

FROM golang:1.25.3-bookworm AS builder

WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev \
	&& rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=1
RUN go build -o /out/iris-worker main.go

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
	ca-certificates \
	chromium \
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

RUN mkdir -p /app/iris/db && chown -R appuser:appuser /app

USER appuser

ENV HOME=/app
ENV RENDER=true

# Point Rod at system Chromium (defaults already enable headless + no-sandbox when running in a container).
CMD ["/app/iris-worker", "-rod=bin=/usr/bin/chromium"]
