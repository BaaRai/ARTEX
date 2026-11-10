package intercept

import (
	"fmt"
	"net/netip"
)

// metadataAddrs are instance-metadata addresses that lie outside the link-local
// ranges and would otherwise pass as private or public unicast.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("fd00:ec2::254"),   // AWS IMDS over IPv6
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud
}

// ValidateExemptionHost checks the host of an entry an operator adds to the
// target-gate exemption list. host is expected in normalised form.
//
// An exemption on a name is resolved at check time and is void when the name
// resolves to a private, loopback, link-local or unspecified address. An IP
// literal is never resolved, so that rule cannot protect it and the literal is
// checked here instead. A private unicast address is allowed: an internal
// service the operator names on purpose (10.0.0.5:8080). Loopback, link-local
// (169.254.0.0/16 holds the cloud metadata address 169.254.169.254; fe80::/10),
// unspecified and multicast addresses are never accepted, nor are the metadata
// addresses outside the link-local ranges. A host that is not an IP literal
// passes: names are the void rule's business.
func ValidateExemptionHost(host string) error {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	addr = addr.Unmap().WithZone("")
	kind := ""
	switch {
	case addr.IsLoopback():
		kind = "a loopback"
	case addr.IsUnspecified():
		kind = "the unspecified"
	case addr.IsLinkLocalUnicast():
		kind = "a link-local (metadata range)"
	case addr.IsMulticast():
		kind = "a multicast"
	}
	for _, m := range metadataAddrs {
		if addr == m {
			kind = "an instance-metadata"
		}
	}
	if kind != "" {
		return fmt.Errorf("%s is %s address and can never be exempt from target approval", host, kind)
	}
	return nil
}
