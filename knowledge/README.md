# Knowledge pack

Small, versioned security taxonomy pack embedded in the ARTEX binary. It gives
the planner and the workers stable identifiers to plan against and to cite in
findings and facts:

- **attack**: MITRE ATT&CK Enterprise techniques (706 entries).
- **wstg**: OWASP Web Security Testing Guide tests (133 entries).
- **cwe**: MITRE Common Weakness Enumeration weaknesses (956 entries).

Each entry carries an id, a name, a category (tactics, WSTG category or CWE
abstraction), a short description capped by the build script, a `truncated` flag
when the description was cut, and the canonical URL.

## Sources and licenses

The data files are generated from the official sources and keep their own
licenses. They are not covered by the repository's AGPL-3.0 license.

| source | license | source |
|---|---|---|
| attack | MITRE ATT&CK Terms of Use | https://github.com/mitre-attack/attack-stix-data |
| wstg | CC-BY-SA-4.0 | https://github.com/OWASP/wstg |
| cwe | MITRE CWE Terms of Use | https://cwe.mitre.org/data/csv/1000.csv |

See [NOTICE](NOTICE) and [licenses/](licenses/) for the attribution and the
license texts. The WSTG descriptions are shortened; IDs, names and references
are unchanged.

## Files

- `data/attack.json`, `data/wstg.json`, `data/cwe.json`: the embedded entries.
- `data/sources.json`: source URL, license, retrieval date, source SHA-256 and
  entry count for each file.
- `licenses/`: the license texts.
- `../scripts/knowledge/build.py`: the generator.

## Rebuilding

```sh
python3 scripts/knowledge/build.py
```

The script downloads the official sources, rewrites the three JSON files and
`sources.json`, and refreshes the license files. The generated data is committed
so the binary needs no network at runtime. Review the diff before committing:
the result is a new pack version.

## How it is used

The read-only `knowledge_lookup` tool is available to the planner and the
workers. It accepts a `source`, a `query` (an id such as `T1190`,
`WSTG-ATHN-01`, `CWE-79`, or keywords) and an optional `limit` (default 5, max
20), and returns the matching entries. It reads the embedded pack, never the
network.

## What it is not

- Not a training corpus, not a fine-tuning dataset, not a RAG dump.
- No exploit code, no scraped lab writeups, no vendor-specific material.
- Not the place for dynamic feeds (CISA KEV, NVD, OSV). Those change daily and
  belong to a fetch-at-runtime layer, not to a committed pack.
- Descriptions are capped for tool output; open the entry URL for the full text.
