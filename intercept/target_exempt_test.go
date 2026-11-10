package intercept

import "testing"

// An operator may exempt a private unicast IP literal, never a loopback,
// link-local (cloud metadata), unspecified or multicast one.
func TestValidateExemptionHost(t *testing.T) {
	for _, host := range []string{
		"10.0.0.5", "172.16.0.1", "192.168.1.1", "fd00::1", // private unicast
		"203.0.113.7", "2001:db8::1", // public
		"pypi.org", "internal.corp", "localhost", // names: the void rule's business
	} {
		if err := ValidateExemptionHost(host); err != nil {
			t.Errorf("%s refused: %v", host, err)
		}
	}
	for _, host := range []string{
		"169.254.169.254", "169.254.170.2", "169.254.0.1", "fe80::1", "fe80::1%eth0", // link-local, metadata
		"::ffff:169.254.169.254",           // the same address, IPv4-mapped
		"fd00:ec2::254", "100.100.100.200", // metadata outside link-local
		"127.0.0.1", "127.8.8.8", "::1", "::ffff:127.0.0.1", // loopback
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", "239.255.255.250", "ff02::1", "ff05::2", // multicast
	} {
		if err := ValidateExemptionHost(host); err == nil {
			t.Errorf("%s accepted, want it refused", host)
		}
	}
}
