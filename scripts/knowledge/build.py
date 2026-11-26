#!/usr/bin/env python3
"""Build the embedded knowledge pack from the official sources.

The output is committed so the agent binary does not need network access at
runtime. Re-run this script to refresh the pack; it records the source URL,
license, retrieval date and source digest in knowledge/data/sources.json.

Usage:
    python3 scripts/knowledge/build.py
"""

from __future__ import annotations

import csv
import hashlib
import io
import json
import sys
import urllib.request
import zipfile
from datetime import date
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DATA = ROOT / "knowledge" / "data"
LICENSES = ROOT / "knowledge" / "licenses"

ATTACK_URL = "https://raw.githubusercontent.com/mitre-attack/attack-stix-data/master/enterprise-attack/enterprise-attack.json"
ATTACK_LICENSE_URL = "https://raw.githubusercontent.com/mitre-attack/attack-stix-data/master/LICENSE.txt"
WSTG_URL = "https://raw.githubusercontent.com/OWASP/wstg/master/checklists/checklist.json"
WSTG_LICENSE_URL = "https://raw.githubusercontent.com/OWASP/wstg/master/LICENSE"
CWE_URL = "https://cwe.mitre.org/data/csv/1000.csv"
CWE_TERMS_URL = "https://cwe.mitre.org/about/termsofuse.html"

MAX_ATTACK_DESCRIPTION = 400
MAX_WSTG_DESCRIPTION = 400
MAX_CWE_DESCRIPTION = 300

CWE_TERMS_TEXT = """CWE™ is free to use by any organization or individual for any research,
development, and/or commercial purposes, per these CWE Terms of Use.
Accordingly, The MITRE Corporation hereby grants you a non-exclusive,
royalty-free license to use CWE for research, development, and commercial
purposes. Any copy you make for such purposes is authorized on the condition
that you reproduce MITRE's copyright designation and this license in any such
copy. CWE is a trademark of The MITRE Corporation.

Copyright © 2006-2026, The MITRE Corporation.

Full terms: https://cwe.mitre.org/about/termsofuse.html
"""


def fetch(url: str) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "artex-knowledge-build/1"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        return resp.read()


def digest(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def clip(text: str, limit: int) -> tuple[str, bool]:
    text = " ".join((text or "").split())
    if len(text) <= limit:
        return text, False
    return text[: limit - 1].rstrip() + "…", True


def attack_entries(raw: bytes) -> list[dict]:
    bundle = json.loads(raw)
    out: list[dict] = []
    for obj in bundle.get("objects", []):
        if obj.get("type") != "attack-pattern":
            continue
        if obj.get("revoked") or obj.get("x_mitre_deprecated"):
            continue
        ref = next(
            (r for r in obj.get("external_references", []) if r.get("source_name") == "mitre-attack"),
            None,
        )
        if not ref or not ref.get("external_id"):
            continue
        description, truncated = clip(obj.get("description", ""), MAX_ATTACK_DESCRIPTION)
        tactics = sorted(
            {
                p.get("phase_name", "")
                for p in obj.get("kill_chain_phases", [])
                if p.get("kill_chain_name") == "mitre-attack" and p.get("phase_name")
            }
        )
        out.append(
            {
                "id": ref["external_id"],
                "name": obj.get("name", ""),
                "category": tactics,
                "description": description,
                "truncated": truncated,
                "url": ref.get("url") or f"https://attack.mitre.org/techniques/{ref['external_id'].replace('.', '/')}/",
            }
        )
    return sorted(out, key=lambda e: e["id"])


def wstg_entries(raw: bytes) -> list[dict]:
    checklist = json.loads(raw)
    out: list[dict] = []
    for category, body in checklist.get("categories", {}).items():
        for test in body.get("tests", []):
            description, truncated = clip(" ".join(test.get("objectives", [])), MAX_WSTG_DESCRIPTION)
            out.append(
                {
                    "id": test.get("id", ""),
                    "name": test.get("name", ""),
                    "category": [category],
                    "description": description,
                    "truncated": truncated,
                    "url": test.get("reference", ""),
                }
            )
    return sorted(out, key=lambda e: e["id"])


def cwe_entries(raw_zip: bytes) -> list[dict]:
    out: list[dict] = []
    with zipfile.ZipFile(io.BytesIO(raw_zip)) as zf:
        name = next((n for n in zf.namelist() if n.endswith(".csv")), None)
        if not name:
            raise SystemExit("CWE archive has no CSV")
        text = io.TextIOWrapper(zf.open(name), encoding="utf-8", errors="replace", newline="")
        for row in csv.DictReader(text):
            cid = (row.get("CWE-ID") or "").strip()
            if not cid.isdigit():
                continue
            description, truncated = clip(row.get("Description", ""), MAX_CWE_DESCRIPTION)
            out.append(
                {
                    "id": f"CWE-{cid}",
                    "name": (row.get("Name") or "").strip(),
                    "category": [(row.get("Weakness Abstraction") or "").strip()],
                    "status": (row.get("Status") or "").strip(),
                    "description": description,
                    "truncated": truncated,
                    "url": f"https://cwe.mitre.org/data/definitions/{cid}.html",
                }
            )
    return sorted(out, key=lambda e: int(e["id"].split("-", 1)[1]))


def write_json(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def main() -> int:
    retrieved = date.today().isoformat()

    attack_raw = fetch(ATTACK_URL)
    wstg_raw = fetch(WSTG_URL)
    cwe_raw = fetch(CWE_URL)

    attack = attack_entries(attack_raw)
    wstg = wstg_entries(wstg_raw)
    cwe = cwe_entries(cwe_raw)
    if not attack or not wstg or not cwe:
        raise SystemExit("one of the sources produced no entries")

    packs = [
        (
            "attack",
            "MITRE ATT&CK Enterprise",
            "MITRE ATT&CK Terms of Use",
            ATTACK_URL,
            ATTACK_LICENSE_URL,
            digest(attack_raw),
            attack,
        ),
        (
            "wstg",
            "OWASP Web Security Testing Guide",
            "CC-BY-SA-4.0",
            WSTG_URL,
            WSTG_LICENSE_URL,
            digest(wstg_raw),
            wstg,
        ),
        (
            "cwe",
            "MITRE Common Weakness Enumeration",
            "MITRE CWE Terms of Use",
            CWE_URL,
            CWE_TERMS_URL,
            digest(cwe_raw),
            cwe,
        ),
    ]

    manifest = {"generated_at": retrieved, "sources": []}
    for source, title, license_name, url, license_url, sha256, entries in packs:
        write_json(
            DATA / f"{source}.json",
            {
                "source": source,
                "title": title,
                "license": license_name,
                "license_url": license_url,
                "source_url": url,
                "retrieved_at": retrieved,
                "sha256": sha256,
                "entries": entries,
            },
        )
        manifest["sources"].append(
            {
                "source": source,
                "title": title,
                "license": license_name,
                "license_url": license_url,
                "source_url": url,
                "retrieved_at": retrieved,
                "sha256": sha256,
                "entries": len(entries),
                "data_file": f"data/{source}.json",
            }
        )

    write_json(DATA / "sources.json", manifest)

    LICENSES.mkdir(parents=True, exist_ok=True)
    (LICENSES / "ATTACK-LICENSE.txt").write_bytes(fetch(ATTACK_LICENSE_URL).replace(b"\r\n", b"\n"))
    (LICENSES / "WSTG-LICENSE.txt").write_bytes(fetch(WSTG_LICENSE_URL).replace(b"\r\n", b"\n"))
    (LICENSES / "CWE-TERMS.html").write_bytes(fetch(CWE_TERMS_URL).replace(b"\r\n", b"\n"))
    (LICENSES / "CWE-TERMS.txt").write_text(CWE_TERMS_TEXT, encoding="utf-8", newline="\n")
    (LICENSES / "CWE-TERMS.url").write_text(CWE_TERMS_URL + "\n", encoding="utf-8", newline="\n")

    print(f"attack: {len(attack)} entries")
    print(f"wstg:   {len(wstg)} entries")
    print(f"cwe:    {len(cwe)} entries")
    print(f"wrote {DATA} and {LICENSES}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
