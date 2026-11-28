# OpenTRAP sidecar — wraps OpenTRAP Java app behind a REST API
# Build: docker build -t artex-opentrap-sidecar:local ./opentrap-sidecar

# Stage 1: Build Go wrapper
FROM golang:1.26.11-bookworm AS wrapper-builder
WORKDIR /build
COPY wrapper/go.mod wrapper/go.sum ./
RUN go mod download
COPY wrapper/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o opentrap-wrapper .

# Stage 2: Build OpenTRAP Java app
FROM eclipse-temurin:21-jdk-jammy AS opentrap-builder
WORKDIR /app
# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    unzip curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy OpenTRAP source
COPY opentrap-src/ /app/opentrap-src/

# Fetch dependencies and build
WORKDIR /app/opentrap-src
# Fix line endings from Windows checkout
RUN find . -type f \( -name "*.sh" -o -name "versions" \) -exec sed -i 's/\r$//' {} \;
RUN ./fetch-jdk.sh && ./fetch-zap.sh && ./fetch-browser.sh
RUN ./build.sh

# Stage 3: Runtime image
FROM eclipse-temurin:21-jre-jammy AS runtime
# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd --system --gid 10001 opentrap \
    && useradd --system --uid 10001 --gid opentrap --home-dir /home/opentrap --create-home opentrap

WORKDIR /app

# Copy OpenTRAP jar and engine
COPY --from=opentrap-builder /app/opentrap-src/dist/opentrap.jar /app/opentrap.jar
COPY --from=opentrap-builder /app/opentrap-src/engine /app/engine

# Copy Go wrapper
COPY --from=wrapper-builder /build/opentrap-wrapper /app/opentrap-wrapper

# Data directory
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