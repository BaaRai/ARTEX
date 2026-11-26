# egress-guard: DNS-driven egress allowlist for the ARTEX network.
# Runs in the host network namespace (network_mode: host) with NET_ADMIN to manage
# an ipset + iptables rules in the DOCKER-USER chain. See README.md.
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache dnsmasq iptables ipset bash tini
COPY entrypoint.sh /usr/local/bin/egress-guard
RUN chmod +x /usr/local/bin/egress-guard
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/egress-guard"]
