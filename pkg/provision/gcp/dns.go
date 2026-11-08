package gcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
)

type managedZone struct {
	Name       string `json:"name"`
	DNSName    string `json:"dnsName"`
	Visibility string `json:"visibility"`
}

type rrset struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl"`
	Rrdatas []string `json:"rrdatas"`
}

// EnsureDNSRecords automatically discovers the matching Cloud DNS managed zone
// in dnsProject (or uses dnsZone when explicitly specified) and idempotently
// creates or updates the DNS records required by a Cloud Run domain mapping.
func (c *Client) EnsureDNSRecords(ctx context.Context, dnsProject, dnsZone, domain string, records []Record, say func(string, ...any)) error {
	if dnsProject == "" || len(records) == 0 {
		return nil
	}
	zoneName := dnsZone
	zoneDNSName := ""
	if zoneName == "" {
		u := c.url(dnsHost, "/dns/v1/projects/%s/managedZones", url.PathEscape(dnsProject))
		var out struct {
			ManagedZones []managedZone `json:"managedZones"`
		}
		if err := c.do(ctx, http.MethodGet, u, nil, &out); err != nil {
			return Classify(err, dnsProject, "list Cloud DNS managed zones", "roles/dns.admin")
		}
		fqdnDomain := strings.TrimSuffix(domain, ".") + "."
		for _, z := range out.ManagedZones {
			if z.Visibility != "" && z.Visibility != "public" {
				continue
			}
			zDNS := strings.TrimSuffix(z.DNSName, ".") + "."
			if fqdnDomain == zDNS || strings.HasSuffix(fqdnDomain, "."+zDNS) {
				if len(zDNS) > len(zoneDNSName) {
					zoneName = z.Name
					zoneDNSName = zDNS
				}
			}
		}
		if zoneName == "" {
			return fmt.Errorf("no public Cloud DNS managed zone matching %s found in project %s — create one or pass --dns-zone", domain, dnsProject)
		}
	} else {
		u := c.url(dnsHost, "/dns/v1/projects/%s/managedZones/%s", url.PathEscape(dnsProject), url.PathEscape(zoneName))
		var z managedZone
		if err := c.do(ctx, http.MethodGet, u, nil, &z); err != nil {
			return Classify(err, dnsProject, "get Cloud DNS managed zone "+zoneName, "roles/dns.admin")
		}
		zoneDNSName = strings.TrimSuffix(z.DNSName, ".") + "."
	}

	type groupKey struct {
		fqdn  string
		rtype string
	}
	grouped := map[groupKey][]string{}
	var order []groupKey
	for _, rec := range records {
		fqdn := rec.Name
		switch {
		case fqdn == "" || fqdn == "@":
			fqdn = zoneDNSName
		case strings.HasSuffix(fqdn, "."):
			// already FQDN
		case strings.HasSuffix(fqdn+".", "."+zoneDNSName) || fqdn+"." == zoneDNSName:
			fqdn = fqdn + "."
		default:
			fqdn = fqdn + "." + zoneDNSName
		}
		data := rec.Data
		if (rec.Type == "CNAME" || rec.Type == "NS" || rec.Type == "PTR") && !strings.HasSuffix(data, ".") {
			data += "."
		}
		k := groupKey{fqdn: fqdn, rtype: rec.Type}
		if _, exists := grouped[k]; !exists {
			order = append(order, k)
		}
		if !slices.Contains(grouped[k], data) {
			grouped[k] = append(grouped[k], data)
		}
	}

	for _, k := range order {
		wantData := grouped[k]
		sort.Strings(wantData)
		listURL := c.url(dnsHost, "/dns/v1/projects/%s/managedZones/%s/rrsets?name=%s&type=%s",
			url.PathEscape(dnsProject), url.PathEscape(zoneName), url.QueryEscape(k.fqdn), url.QueryEscape(k.rtype))
		var existing struct {
			Rrsets []rrset `json:"rrsets"`
		}
		if err := c.do(ctx, http.MethodGet, listURL, nil, &existing); err != nil {
			return Classify(err, dnsProject, "read Cloud DNS record "+k.fqdn, "roles/dns.admin")
		}
		if len(existing.Rrsets) > 0 {
			got := slices.Clone(existing.Rrsets[0].Rrdatas)
			sort.Strings(got)
			if slices.Equal(got, wantData) {
				if say != nil {
					say("✓ dns          %s %s → %s (%s/%s)", k.fqdn, k.rtype, strings.Join(wantData, ", "), dnsProject, zoneName)
				}
				continue
			}
			patchURL := c.url(dnsHost, "/dns/v1/projects/%s/managedZones/%s/rrsets/%s/%s",
				url.PathEscape(dnsProject), url.PathEscape(zoneName), url.PathEscape(k.fqdn), url.PathEscape(k.rtype))
			body := rrset{Name: k.fqdn, Type: k.rtype, TTL: 300, Rrdatas: wantData}
			if err := c.do(ctx, http.MethodPatch, patchURL, body, nil); err != nil {
				return Classify(err, dnsProject, "update Cloud DNS record "+k.fqdn, "roles/dns.admin")
			}
			if say != nil {
				say("✓ dns          updated %s %s → %s (%s/%s)", k.fqdn, k.rtype, strings.Join(wantData, ", "), dnsProject, zoneName)
			}
			continue
		}
		createURL := c.url(dnsHost, "/dns/v1/projects/%s/managedZones/%s/rrsets",
			url.PathEscape(dnsProject), url.PathEscape(zoneName))
		body := rrset{Name: k.fqdn, Type: k.rtype, TTL: 300, Rrdatas: wantData}
		if err := c.do(ctx, http.MethodPost, createURL, body, nil); err != nil {
			return Classify(err, dnsProject, "create Cloud DNS record "+k.fqdn, "roles/dns.admin")
		}
		if say != nil {
			say("✓ dns          created %s %s → %s (%s/%s)", k.fqdn, k.rtype, strings.Join(wantData, ", "), dnsProject, zoneName)
		}
	}
	return nil
}
