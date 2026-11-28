# Ghidra Headless REST API - builds from source
FROM eclipse-temurin:21-jdk-jammy AS builder

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    DEBIAN_FRONTEND=noninteractive \
    GHIDRA_VERSION=11.3.2 \
    GHIDRA_ZIP=ghidra_11.3.2_PUBLIC_20250415.zip \
    GHIDRA_SHA256=99d45035bdcc3d6627e7b1232b7b379905a9fad76c772c920602e2b5d8b2dac2

# Install build dependencies (Java already in base)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl python3 python3-pip unzip \
    && rm -rf /var/lib/apt/lists/*

# Download and verify Ghidra
RUN curl -L -o /tmp/ghidra.zip "https://github.com/NationalSecurityAgency/ghidra/releases/download/Ghidra_${GHIDRA_VERSION}_build/${GHIDRA_ZIP}" \
    && echo "${GHIDRA_SHA256}  /tmp/ghidra.zip" | sha256sum -c - \
    && unzip -q /tmp/ghidra.zip -d /opt \
    && rm /tmp/ghidra.zip

# Install Python dependencies
COPY sidecars/ghidra-headless-rest/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt

# Copy webui source
COPY sidecars/ghidra-headless-rest/webui /app/webui

# Verify vendor integrity (skip on Windows line endings)
# RUN python /app/webui/verify_vendor.py

# Runtime stage - use eclipse-temurin which has Java 21
FROM eclipse-temurin:21-jre-jammy

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    CHATS_DIR=/data/chats \
    HOST=0.0.0.0 \
    PORT=9090 \
    DEBUG=false \
    LOG_LEVEL=INFO \
    GHIDRA_HOME=/opt/ghidra_11.3.2_PUBLIC \
    GHIDRA_BIN=/opt/ghidra_11.3.2_PUBLIC/support/analyzeHeadless \
    GHIDRA_SCRIPTS=/opt/ghidra_11.3.2_PUBLIC/ghidra_scripts \
    JAVA_HOME=/opt/java/openjdk \
    DATA_DIR=/data/ghidra_projects \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    GUNICORN_BIND=0.0.0.0:9090

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl python3 python3-pip \
    && rm -rf /var/lib/apt/lists/*

# Install Python packages
COPY sidecars/ghidra-headless-rest/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt

# Create non-root user
RUN groupadd --system --gid 10001 ghidra \
    && useradd --system --uid 10001 --gid ghidra --home-dir /home/ghidra --create-home ghidra \
    && mkdir -p /data/ghidra_projects /data/chats /app \
    && chown -R ghidra:ghidra /data/ghidra_projects /data/chats /app /home/ghidra

# Copy Ghidra from builder
COPY --from=builder /opt/ghidra_11.3.2_PUBLIC /opt/ghidra_11.3.2_PUBLIC

# Copy Python app from builder
COPY --from=builder /app/webui /app/webui

WORKDIR /app/webui

EXPOSE 9090
USER 10001:10001

VOLUME ["/data/ghidra_projects"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:9090/healthz', timeout=3).status==200 else 1)"

CMD ["gunicorn", "-c", "gunicorn.conf.py", "wsgi:app"]