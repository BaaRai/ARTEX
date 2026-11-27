# OpenTRAP Sidecar - builds from source (headless mode)
# Multi-stage: build OpenTRAP Java app, then runtime with REST wrapper

# Stage 1: Build OpenTRAP Java app
FROM eclipse-temurin:21-jdk-jammy AS opentrap-builder

WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends \
    unzip curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy OpenTRAP source
COPY sidecars/opentrap/ /app/opentrap-src/

# Fetch dependencies and build
WORKDIR /app/opentrap-src
RUN find . -name "*.sh" -exec sed -i 's/\r$//' {} \; && sed -i 's/\r$//' tools/versions
RUN ./fetch-jdk.sh && ./fetch-zap.sh && ./fetch-browser.sh
RUN ./build.sh

# Stage 2: Build Go REST wrapper
FROM golang:1.26.11-bookworm AS wrapper-builder

WORKDIR /build
COPY docker/opentrap-wrapper/ ./
RUN go mod download && CGO_ENABLED=0 go build -ldflags="-s -w" -o opentrap-wrapper .

# Stage 3: Runtime
FROM eclipse-temurin:21-jre-jammy

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --system --gid 10001 opentrap \
    && useradd --system --uid 10001 --gid opentrap --home-dir /home/opentrap --create-home opentrap

WORKDIR /app

# Copy OpenTRAP jar and engine
COPY --from=opentrap-builder /app/opentrap-src/dist/opentrap.jar /app/opentrap.jar
COPY --from=opentrap-builder /app/opentrap-src/engine /app/engine

# Copy Go wrapper
COPY --from=wrapper-builder /build/opentrap-wrapper /app/opentrap-wrapper

RUN mkdir -p /app/data && chown -R opentrap:opentrap /app

ENV PORT=8081 \
    DATA_DIR=/app/data \
    JAVA_HOME=/opt/java/openjdk \
    PATH="/opt/java/openjdk/bin:${PATH}"

USER 10001:10001

VOLUME ["/app/data"]
EXPOSE 8081

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD curl --fail --silent http://127.0.0.1:8081/v1/healthz >/dev/null || exit 1

CMD ["/app/opentrap-wrapper"]