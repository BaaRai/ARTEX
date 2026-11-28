# Reviewing a published vulnerability advisory

When a finding cites a CVE or GHSA identifier, look up that exact identifier in the
[NVD vulnerability search](https://nvd.nist.gov/vuln/search) or the
[GitHub Advisory Database](https://github.com/advisories). Check the issuer's advisory for the
affected product, version range and fixed versions. An identifier alone cannot confirm that an
installed version is affected.

Record the identifier, source URL, advisory's last-updated date when available, and the installed
version in your review notes. If the advisory and the observed version disagree, document the
difference and defer the applicability decision until you can confirm it. Search using public identifiers
and keep private hostnames, logs and credentials in your ARTEX instance.
