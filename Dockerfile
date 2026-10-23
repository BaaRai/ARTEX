# syntax=docker/dockerfile:1
#
# Multi-stage build: `docker compose build` produces the complete image from
# source. No prebuilt binary and no published image are required.
#
#   frontend -> Go binary (embedded UI) -> runtime image
#
# Third-party tools are pinned and verified against the checksums published in
# their GitHub releases. The checksum comes from the same release channel as the
# artifact, so it detects corrupted or tampered downloads and moved tags; it does
# not protect against a compromised publisher.

# ---------------------------------------------------------------- frontend ---
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build:static

# ----------------------------------------------------------------- backend ---
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/out ./server/webui/dist
ARG ARTEX_VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -tags embedui \
      -trimpath \
      -ldflags "-s -w -buildid= -X main.version=${ARTEX_VERSION}" \
      -o /out/artex ./cmd/artex

# ------------------------------------------------------- third-party tools ---
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS tools
ARG TARGETARCH

ARG NUCLEI_VERSION=v3.11.1
ARG HTTPX_VERSION=v1.12.0
ARG SUBFINDER_VERSION=v2.17.0
ARG KATANA_VERSION=v1.8.0
ARG FFUF_VERSION=v2.3.0
ARG DALFOX_VERSION=v3.2.4
ARG SQLMAP_VERSION=1.9.9
ARG SQLMAP_COMMIT=cceda39208b5aad478de2c3f731df4239d9881f6

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git unzip xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      amd64) PD_ARCH=amd64; FFUF_ARCH=amd64; DALFOX_ARCH=x86_64 ;; \
      arm64) PD_ARCH=arm64; FFUF_ARCH=arm64; DALFOX_ARCH=aarch64 ;; \
      *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /out; \
    verify() { \
      _want="$1"; _file="$2"; \
      _got="$(sha256sum "$_file" | cut -d' ' -f1)"; \
      if [ "$_want" != "$_got" ]; then \
        echo "checksum mismatch for $_file: want $_want got $_got" >&2; exit 1; \
      fi; \
    }; \
    fetch_pd() { \
      _repo="$1"; _name="$2"; _ver="$3"; _sums="$4"; _nov="${_ver#v}"; \
      _base="https://github.com/projectdiscovery/${_repo}/releases/download/${_ver}"; \
      _asset="${_name}_${_nov}_linux_${PD_ARCH}.zip"; \
      curl -fsSL "${_base}/${_asset}" -o "/tmp/${_name}.zip"; \
      curl -fsSL "${_base}/${_sums}" -o "/tmp/${_name}-sums.txt"; \
      _want="$(grep -F " ${_asset}" "/tmp/${_name}-sums.txt" | awk '{print $1}')"; \
      if [ -z "$_want" ]; then echo "no checksum for ${_asset}" >&2; exit 1; fi; \
      verify "$_want" "/tmp/${_name}.zip"; \
      rm -rf "/tmp/${_name}"; mkdir -p "/tmp/${_name}"; \
      unzip -q "/tmp/${_name}.zip" -d "/tmp/${_name}"; \
      find "/tmp/${_name}" -type f -name "${_name}" -exec install -m 0755 {} /out/ \;; \
      rm -rf "/tmp/${_name}" "/tmp/${_name}.zip" "/tmp/${_name}-sums.txt"; \
    }; \
    fetch_pd nuclei    nuclei    "${NUCLEI_VERSION}"    "nuclei_${NUCLEI_VERSION#v}_checksums.txt"; \
    fetch_pd httpx     httpx     "${HTTPX_VERSION}"     "httpx_${HTTPX_VERSION#v}_checksums.txt"; \
    fetch_pd subfinder subfinder "${SUBFINDER_VERSION}" "subfinder_${SUBFINDER_VERSION#v}_checksums.txt"; \
    fetch_pd katana    katana    "${KATANA_VERSION}"    "katana-${KATANA_VERSION#v}-checksums.txt"; \
    _ffuf_asset="ffuf_${FFUF_VERSION#v}_linux_${FFUF_ARCH}.tar.gz"; \
    curl -fsSL "https://github.com/ffuf/ffuf/releases/download/${FFUF_VERSION}/${_ffuf_asset}" -o /tmp/ffuf.tar.gz; \
    curl -fsSL "https://github.com/ffuf/ffuf/releases/download/${FFUF_VERSION}/ffuf_${FFUF_VERSION#v}_checksums.txt" -o /tmp/ffuf-sums.txt; \
    _want="$(grep -F " ${_ffuf_asset}" /tmp/ffuf-sums.txt | awk '{print $1}')"; \
    if [ -z "$_want" ]; then echo "no checksum for ${_ffuf_asset}" >&2; exit 1; fi; \
    verify "$_want" /tmp/ffuf.tar.gz; \
    mkdir -p /tmp/ffuf; tar -xzf /tmp/ffuf.tar.gz -C /tmp/ffuf; \
    find /tmp/ffuf -type f -name ffuf -exec install -m 0755 {} /out/ \;; \
    rm -rf /tmp/ffuf /tmp/ffuf.tar.gz /tmp/ffuf-sums.txt; \
    _dalfox_asset="dalfox-${DALFOX_VERSION}-linux-${DALFOX_ARCH}-musl.tar.gz"; \
    curl -fsSL "https://github.com/hahwul/dalfox/releases/download/${DALFOX_VERSION}/${_dalfox_asset}" -o /tmp/dalfox.tar.gz; \
    curl -fsSL "https://github.com/hahwul/dalfox/releases/download/${DALFOX_VERSION}/${_dalfox_asset}.sha256" -o /tmp/dalfox.sha256; \
    _want="$(awk '{print $1}' /tmp/dalfox.sha256)"; \
    if [ -z "$_want" ]; then echo "no checksum for ${_dalfox_asset}" >&2; exit 1; fi; \
    verify "$_want" /tmp/dalfox.tar.gz; \
    mkdir -p /tmp/dalfox; tar -xzf /tmp/dalfox.tar.gz -C /tmp/dalfox; \
    find /tmp/dalfox -type f -name dalfox -exec install -m 0755 {} /out/ \;; \
    rm -rf /tmp/dalfox /tmp/dalfox.tar.gz /tmp/dalfox.sha256; \
    git clone --quiet --depth 1 --branch "${SQLMAP_VERSION}" https://github.com/sqlmapproject/sqlmap /opt/sqlmap; \
    _commit="$(git -C /opt/sqlmap rev-parse HEAD)"; \
    if [ "$_commit" != "${SQLMAP_COMMIT}" ]; then \
      echo "sqlmap commit mismatch: want ${SQLMAP_COMMIT} got ${_commit}" >&2; exit 1; \
    fi; \
    rm -rf /opt/sqlmap/.git

# ---------------------------------------------------------------- runtime ---
FROM python:3.12-slim-bookworm

ARG TARGETARCH
ARG NODE_VERSION=22.23.4
ARG BBOT_VERSION=3.0.2
ARG PLAYWRIGHT_MCP_VERSION=0.0.83
ARG PLAYWRIGHT_CLI_VERSION=0.1.22
ARG PLAYWRIGHT_VERSION=1.64.1

# Common tools: ripgrep / curl / vim, plus a set of recon staples.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip xz-utils \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && rm -rf /var/lib/apt/lists/*

# Node from the official distribution, verified against SHASUMS256.txt.
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      amd64) NODE_ARCH=x64 ;; \
      arm64) NODE_ARCH=arm64 ;; \
      *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    base="https://nodejs.org/dist/v${NODE_VERSION}"; \
    asset="node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz"; \
    curl -fsSL "${base}/${asset}" -o /tmp/node.tar.xz; \
    curl -fsSL "${base}/SHASUMS256.txt" -o /tmp/SHASUMS256.txt; \
    want="$(grep -F " ${asset}" /tmp/SHASUMS256.txt | awk '{print $1}')"; \
    got="$(sha256sum /tmp/node.tar.xz | cut -d' ' -f1)"; \
    if [ -z "$want" ] || [ "$want" != "$got" ]; then \
      echo "node checksum mismatch for ${asset}" >&2; exit 1; \
    fi; \
    tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1; \
    rm -f /tmp/node.tar.xz /tmp/SHASUMS256.txt; \
    node --version; npm --version

# bbot recursive recon orchestrator, pinned. Module dependencies are preinstalled
# so scans never need pip at runtime.
RUN set -eux; \
    python -m venv /opt/bbot; \
    /opt/bbot/bin/pip install --no-cache-dir "bbot==${BBOT_VERSION}"; \
    ln -s /opt/bbot/bin/bbot /usr/local/bin/bbot; \
    bbot -y --install-all-deps; \
    bbot --version >/dev/null; \
    bbot --dry-run -t example.com -m wafw00f,paramminer_getparams,lightfuzz,sslcert,gowitness --no-color >/tmp/bbot-dry.log 2>&1 || true; \
    ! grep -qE "Installing|ERRR|CRITICAL" /tmp/bbot-dry.log || { cat /tmp/bbot-dry.log; exit 1; }

# Playwright MCP/CLI and the browser, pinned so no unpinned download happens at runtime.
RUN set -eux; \
    npm install -g \
      "@playwright/mcp@${PLAYWRIGHT_MCP_VERSION}" \
      "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}" \
      "playwright@${PLAYWRIGHT_VERSION}"; \
    playwright-cli --help >/dev/null; \
    playwright install --with-deps chromium; \
    rm -rf /var/lib/apt/lists/*

# Pinned, checksum-verified recon binaries and the sqlmap checkout.
COPY --from=tools /out/ /usr/local/bin/
COPY --from=tools /opt/sqlmap /opt/sqlmap
RUN set -eux; \
    ln -sf /opt/sqlmap/sqlmap.py /usr/local/bin/sqlmap; \
    nuclei -version >/dev/null; \
    httpx -version >/dev/null; \
    subfinder -version >/dev/null; \
    katana -version >/dev/null; \
    dalfox --version >/dev/null; \
    ffuf -V >/dev/null; \
    sqlmap --version >/dev/null

WORKDIR /app
COPY --from=build /out/artex /app/artex
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# persistence point for data/ (SQLite + jwt.key)
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
