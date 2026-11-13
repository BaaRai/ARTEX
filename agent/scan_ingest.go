package agent

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// Deterministic ingestion of scanner output files (nmap -oX). The rules are the
// bbot parser's: only typed fields are read and validated, and every free-text
// field the target shapes (banner, product, version, hostname, OS name) is kept
// only inside one Go-quoted fact detail. It never reaches a summary, a notable
// item or an instruction.

// scanNotableMax caps the notable items returned per type.
const scanNotableMax = 20

// scanIngest is what one scanner file yields: counters, the insert_assets
// items, the facts, and a capped list of notable items per type.
type scanIngest struct {
	Events, Ports, Skipped int
	Truncated              bool
	hosts, urls            map[string]bool
	Assets                 []assetInputItem
	Facts                  []factItem
	Notable                map[string][]string
}

func newScanIngest() *scanIngest {
	return &scanIngest{hosts: map[string]bool{}, urls: map[string]bool{}, Notable: map[string][]string{}}
}

func (s *scanIngest) note(kind, item string) {
	if len(s.Notable[kind]) < scanNotableMax {
		s.Notable[kind] = append(s.Notable[kind], item)
	}
}

// fact records a tool_output fact: the summary is built by the caller from
// typed fields only, the detail is the compact JSON of detail as one Go-quoted
// inert string.
func (s *scanIngest) fact(summary string, detail any) {
	b, _ := json.Marshal(detail)
	s.Facts = append(s.Facts, factItem{Summary: summary, Detail: strconv.Quote(string(b)), Source: factSourceToolOutput})
}

// scanHost returns h as a lower-case IP or hostname, "" when it is neither.
func scanHost(h string) string {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if ip := net.ParseIP(h); ip != nil {
		return ip.String()
	}
	if bbotHostRe.MatchString(h) {
		return h
	}
	return ""
}

// ---- nmap -oX ----

type nmapHost struct {
	Status struct {
		State string `xml:"state,attr"`
	} `xml:"status"`
	Addrs []struct {
		Addr string `xml:"addr,attr"`
		Type string `xml:"addrtype,attr"`
	} `xml:"address"`
	Names []struct {
		Name string `xml:"name,attr"`
		Type string `xml:"type,attr"`
	} `xml:"hostnames>hostname"`
	Ports []struct {
		Proto string `xml:"protocol,attr"`
		ID    string `xml:"portid,attr"`
		State struct {
			State string `xml:"state,attr"`
		} `xml:"state"`
		Svc struct {
			Name    string   `xml:"name,attr"`
			Product string   `xml:"product,attr"`
			Version string   `xml:"version,attr"`
			Tunnel  string   `xml:"tunnel,attr"`
			CPE     []string `xml:"cpe"`
		} `xml:"service"`
	} `xml:"ports>port"`
	OS []struct {
		Name     string `xml:"name,attr"`
		Accuracy string `xml:"accuracy,attr"`
	} `xml:"os>osmatch"`
}

// readNmapXML streams the file and decodes one <host> element at a time. A file
// cut short keeps the hosts read before the cut and is marked truncated; a file
// with no <nmaprun> root, or with no complete host that fails to parse, is an
// error.
func readNmapXML(r io.Reader, s *scanIngest) error {
	dec := xml.NewDecoder(r)
	root := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !root {
				return errors.New("no nmaprun element")
			}
			return nil
		}
		if err == nil {
			se, ok := tok.(xml.StartElement)
			root = root || (ok && se.Name.Local == "nmaprun")
			if !ok || se.Name.Local != "host" {
				continue
			}
			var h nmapHost
			if err = dec.DecodeElement(&h, &se); err == nil {
				s.Events++
				s.addNmapHost(h)
				continue
			}
		}
		if s.Events == 0 {
			return err
		}
		s.Truncated = true
		return nil
	}
}

// nmapWebScheme returns the URL scheme of an nmap web service, "" otherwise.
func nmapWebScheme(name, tunnel string) string {
	switch name {
	case "http", "http-alt", "http-proxy":
		if tunnel == "ssl" {
			return "https"
		}
		return "http"
	case "https", "https-alt":
		return "https"
	}
	return ""
}

func (s *scanIngest) addNmapHost(h nmapHost) {
	if h.Status.State != "up" {
		return
	}
	ip := ""
	for _, a := range h.Addrs {
		if ip == "" && (a.Type == "ipv4" || a.Type == "ipv6") && net.ParseIP(a.Addr) != nil {
			ip = scanHost(a.Addr)
		}
	}
	if ip == "" {
		s.Skipped++
		return
	}
	s.hosts[ip] = true
	detail := map[string]any{"ip": ip, "hostnames": h.Names, "os": h.OS}
	var bound []string
	for _, n := range h.Names {
		// Only the name the operator scanned is bound to the IP; a PTR name is
		// third-party data and stays in the fact detail.
		if hn := scanHost(n.Name); n.Type == "user" && hn != "" && net.ParseIP(hn) == nil {
			bound = append(bound, hn)
		}
	}
	var open []db.PortService
	var list []string
	var ports []map[string]any
	for _, p := range h.Ports {
		port, err := strconv.Atoi(p.ID)
		if err != nil || port <= 0 || port > 65535 || p.State.State != "open" ||
			(p.Proto != "tcp" && p.Proto != "udp" && p.Proto != "sctp") {
			continue
		}
		name := bbotToken(p.Svc.Name)
		if name == "" {
			name = "unknown"
		}
		s.Ports++
		pp := fmt.Sprintf("%d/%s", port, p.Proto)
		list = append(list, pp)
		open = append(open, db.PortService{Port: port, Service: name})
		ports = append(ports, map[string]any{"port": pp, "service": p.Svc.Name, "product": p.Svc.Product,
			"version": p.Svc.Version, "tunnel": p.Svc.Tunnel, "cpe": p.Svc.CPE})
		s.note("ports", ip+" "+pp+" "+name)
		if scheme := nmapWebScheme(name, p.Svc.Tunnel); scheme != "" && p.Proto == "tcp" {
			u := scheme + "://" + net.JoinHostPort(ip, strconv.Itoa(port))
			s.urls[u] = true
			s.note("urls", u)
			s.Assets = append(s.Assets, assetInputItem{Type: "service", URL: u, IP: ip})
		} else {
			s.Assets = append(s.Assets, assetInputItem{Type: "service", IP: ip, Port: port, ServiceName: name})
		}
	}
	s.Assets = append(s.Assets, assetInputItem{Type: "ip", IP: ip, BoundDomains: bound, OpenPorts: open})
	s.note("hosts", fmt.Sprintf("%s (%d open ports)", ip, len(list)))
	shown := list
	if len(shown) > 50 {
		shown = shown[:50]
	}
	summary := fmt.Sprintf("nmap: host %s has %d open ports: %s", ip, len(list), strings.Join(shown, ", "))
	if len(list) > len(shown) {
		summary += fmt.Sprintf(" and %d more", len(list)-len(shown))
	}
	detail["ports"] = ports
	s.fact(summary, detail)
}
