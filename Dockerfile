# syntax=docker/dockerfile:1
#
# Runtime image (no compilation inside the image): install common tools only and ship
# a **pre-built Linux single binary**. The binary is cross-compiled by the CI binaries
# job (pure Go, no QEMU) and placed at dist/<TARGETARCH>/artex in the build context.
# This way multi-arch builds only need to emulate the apt layer for arm64 instead of
# emulating the Next/Go compilation, which is much faster.
#
# To build the image manually, prepare the binary yourself first:
#   cd web && npm run build:static && cd ..
#   cp -r web/out server/webui/dist
#   CGO_ENABLED=0 GOARCH=amd64 go build -tags embedui -o dist/amd64/artex ./cmd/artex
#   docker build -t artex:local .
FROM python:3.12-slim-bookworm
ARG TARGETARCH
# Common tools: ripgrep / curl / vim, plus a set of recon staples (add/remove as needed).
# Node 20.x comes from NodeSource: bookworm's apt nodejs is 18, but Playwright requires >=20.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# Web-recon toolbox (drost-community take 1, #100): pinned release binaries installed
# into /usr/local/bin so hunt workers can run them directly through their Bash tool.
# Versions pinned per tool; upgrade deliberately. sqlmap is a python checkout (no
# release binaries) symlinked into PATH.
ARG NUCLEI_VERSION=v3.11.1
ARG HTTPX_VERSION=v1.12.0
ARG SUBFINDER_VERSION=v2.17.0
ARG KATANA_VERSION=v1.8.0
ARG FFUF_VERSION=v2.3.0
ARG DALFOX_VERSION=v3.2.4
RUN set -eux; \
    base="https://github.com"; \
    pd_fetch() { repo="$1"; ver="$2"; name="$3"; nov="${ver#v}"; \
      curl -fsSL "$base/$repo/releases/download/$ver/${name}_${nov}_linux_${TARGETARCH}.zip" -o /tmp/t.zip \
      && unzip -o -q /tmp/t.zip -d /tmp/td \
      && find /tmp/td -type f -name "$name" -exec install -m 0755 {} /usr/local/bin/ \; \
      && rm -rf /tmp/t.zip /tmp/td; }; \
    case "$TARGETARCH" in amd64) FFUF_ARCH=amd64; DALFOX_ARCH=x86_64;; arm64) FFUF_ARCH=arm64; DALFOX_ARCH=aarch64;; esac; \
    pd_fetch projectdiscovery/nuclei "$NUCLEI_VERSION" nuclei; \
    pd_fetch projectdiscovery/httpx "$HTTPX_VERSION" httpx; \
    pd_fetch projectdiscovery/subfinder "$SUBFINDER_VERSION" subfinder; \
    pd_fetch projectdiscovery/katana "$KATANA_VERSION" katana; \
    curl -fsSL "$base/ffuf/ffuf/releases/download/$FFUF_VERSION/ffuf_${FFUF_VERSION#v}_linux_${FFUF_ARCH}.tar.gz" \
      | tar -xz -C /usr/local/bin ffuf; \
    curl -fsSL "$base/hahwul/dalfox/releases/download/$DALFOX_VERSION/dalfox-${DALFOX_VERSION}-linux-${DALFOX_ARCH}-musl.tar.gz" \
      | tar -xz -C /usr/local/bin dalfox; \
    chmod +x /usr/local/bin/ffuf /usr/local/bin/dalfox; \
    git clone --depth 1 https://github.com/sqlmapproject/sqlmap /opt/sqlmap; \
    ln -s /opt/sqlmap/sqlmap.py /usr/local/bin/sqlmap; \
    nuclei -version >/dev/null; httpx -version; subfinder -version; katana -version; \
    ffuf -V >/dev/null; dalfox version >/dev/null; sqlmap --version >/dev/null

# Preinstall Playwright MCP and CLI (globally) so no npx network download is needed at runtime.
# @playwright/mcp: browser MCP runs directly via `npx @playwright/mcp` (installed globally, no -y/@latest needed).
# @playwright/cli: provides playwright-cli, verified with --help right after install.
# Then install playwright (browser management) and preinstall chromium plus its system
# dependencies via --with-deps, so MCP/CLI work on first start inside the container
# without downloading browsers over the network.
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# pre-built binary for the matching architecture (dist/amd64/artex or dist/arm64/artex)
COPY dist/${TARGETARCH}/artex /app/artex
# Daemon startup script: decides whether to relaunch the process based on its exit code;
# the in-page one-click update relies on it to swap the binary. It also forwards SIGTERM
# to artex — docker stop only signals PID 1; without forwarding, artex never receives it,
# cannot shut down gracefully, and is hard-killed by SIGKILL after 10 seconds.
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# persistence point for data/ (SQLite + jwt.key)
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
