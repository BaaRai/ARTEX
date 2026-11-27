# OpenTRAP sidecar — wraps the OpenTRAP AppImage behind a REST API.
# This is a placeholder Dockerfile. The actual sidecar implementation needs a Go
# service that starts the OpenTRAP AppImage (Java Swing) in headless mode and
# exposes the REST endpoints: /v1/status, /v1/history, /v1/history/{id},
# /v1/scope (GET/PUT), /v1/replay (POST with X-OpenTrap-* headers).
#
# Build:
#   docker build -t artex-opentrap-sidecar:local ./opentrap-sidecar
#
# The image expects the OpenTRAP AppImage to be available at build time.
# Download it from https://github.com/opentrap/opentrap/releases/latest/download/opentrap-x86_64.appimage
# and place it in this directory, or adjust the ADD line below.

FROM eclipse-temurin:21-jre-alpine AS base

# Install dependencies for the AppImage (FUSE for AppImage extraction, fonts for Swing)
RUN apk add --no-cache \
    fuse3 \
    fontconfig \
    ttf-dejavu \
    libstdc++ \
    ca-certificates \
    curl

# Create non-root user
RUN addgroup -g 10001 -S opentrap && \
    adduser -u 10001 -S -G opentrap -h /home/opentrap opentrap

WORKDIR /app

# Download the OpenTRAP AppImage (pinned to a specific release for reproducibility)
# Replace the version and digest as needed.
ARG OPENTRAP_VERSION=latest
ARG OPENTRAP_URL=https://github.com/opentrap/opentrap/releases/download/${OPENTRAP_VERSION}/opentrap-x86_64.appimage
ARG OPENTRAP_SHA256=

ADD ${OPENTRAP_URL} /app/opentrap.appimage
RUN if [ -n "${OPENTRAP_SHA256}" ]; then \
      echo "${OPENTRAP_SHA256}  /app/opentrap.appimage" | sha256sum -c -; \
    fi && \
    chmod +x /app/opentrap.appimage

# The AppImage needs FUSE to extract. Use --appimage-extract to unpack it.
RUN /app/opentrap.appimage --appimage-extract && \
    mv squashfs-root /app/opentrap && \
    rm /app/opentrap.appimage

# Copy the Go wrapper binary (built separately) or use a script to start the
# OpenTRAP Java app and the REST API. For now this is a placeholder.
# COPY opentrap-wrapper /app/opentrap-wrapper

ENV PORT=8081
ENV DATA_DIR=/app/data
ENV JAVA_HOME=/opt/java/openjdk
ENV PATH="${JAVA_HOME}/bin:${PATH}"

USER 10001:10001

VOLUME ["/app/data"]
EXPOSE 8081

# The wrapper would start the OpenTRAP app and the REST API server.
# CMD ["/app/opentrap-wrapper"]
CMD ["sh", "-c", "echo 'OpenTRAP sidecar placeholder - implement the REST wrapper'; sleep infinity"]