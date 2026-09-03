package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sourceProxyScrape = "proxyscrape_free"

	proxyScrapeJSON    = "https://cdn.jsdelivr.net/gh/ProxyScrape/free-proxy-list@main/proxies/protocols/socks5/data.json"
	proxyScrapeRawJSON = "https://raw.githubusercontent.com/ProxyScrape/free-proxy-list/main/proxies/protocols/socks5/data.json"

	proxyScrapeCountryLimit = 30
	proxyScrapeMinUptimePct = 60.0
	proxyScrapeMaxLatencyMS = 5000.0
	proxyScrapeMaxCheckAge  = 6 * time.Hour
)

type proxyScrapeEntry struct {
	Protocol      string  `json:"protocol"`
	IP            string  `json:"ip"`
	Port          int     `json:"port"`
	Country       string  `json:"country"`
	CountryCode   string  `json:"country_code"`
	Anonymity     string  `json:"anonymity"`
	UptimePercent float64 `json:"uptime_percent"`
	ASN           string  `json:"asn"`
	ISP           string  `json:"isp"`
	LatencyMS     float64 `json:"latency_ms"`
	LastChecked   float64 `json:"last_checked"`
}

func fetchProxyScrapeNodes() ([]Node, error) {
	entries, endpoint, err := fetchProxyScrapeJSON()
	if err != nil {
		return nil, err
	}
	nodes := parseProxyScrapeEntries(entries, time.Now())
	if len(nodes) == 0 {
		return nil, fmt.Errorf("ProxyScrape 节点经过轻筛选后为空（%s）", endpoint)
	}
	return nodes, nil
}

func fetchProxyScrapeJSON() ([]proxyScrapeEntry, string, error) {
	endpoints := []string{proxyScrapeJSON, proxyScrapeRawJSON}
	var errs []string
	for _, endpoint := range endpoints {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		req.Header.Set("User-Agent", "Xs5/"+appVersion)
		req.Header.Set("Accept", "application/json")
		cli := &http.Client{Timeout: 35 * time.Second}
		resp, err := cli.Do(req)
		if err != nil {
			errs = append(errs, endpoint+": "+err.Error())
			continue
		}
		if resp.StatusCode != http.StatusOK {
			status := resp.StatusCode
			resp.Body.Close()
			errs = append(errs, fmt.Sprintf("%s: HTTP %d", endpoint, status))
			continue
		}
		var entries []proxyScrapeEntry
		decErr := json.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&entries)
		resp.Body.Close()
		if decErr != nil {
			errs = append(errs, endpoint+": 解析 JSON 失败: "+decErr.Error())
			continue
		}
		if len(entries) == 0 {
			errs = append(errs, endpoint+": 返回空列表")
			continue
		}
		return entries, endpoint, nil
	}
	if len(errs) == 0 {
		return nil, "", errors.New("ProxyScrape 没有可用数据入口")
	}
	return nil, "", errors.New(strings.Join(errs, "；"))
}

func parseProxyScrapeEntries(entries []proxyScrapeEntry, now time.Time) []Node {
	byCountry := map[string][]Node{}
	seen := map[string]bool{}
	for _, e := range entries {
		if !strings.EqualFold(strings.TrimSpace(e.Protocol), "socks5") {
			continue
		}
		ip := strings.TrimSpace(e.IP)
		if net.ParseIP(ip) == nil || e.Port < 1 || e.Port > 65535 {
			continue
		}
		cc := strings.ToUpper(strings.TrimSpace(e.CountryCode))
		if len(cc) != 2 {
			cc = countryCodeFromName(e.Country)
		}
		if len(cc) != 2 {
			continue
		}
		if e.UptimePercent < proxyScrapeMinUptimePct {
			continue
		}
		if e.LatencyMS <= 0 || e.LatencyMS > proxyScrapeMaxLatencyMS {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(e.Anonymity), "transparent") {
			continue
		}
		if e.LastChecked > 0 {
			checked := time.Unix(int64(e.LastChecked), 0)
			if checked.Before(now) && now.Sub(checked) > proxyScrapeMaxCheckAge {
				continue
			}
		}
		endpointKey := net.JoinHostPort(ip, strconv.Itoa(e.Port))
		if seen[endpointKey] {
			continue
		}
		seen[endpointKey] = true
		node := Node{
			Host: endpointKey, IP: ip, Port: e.Port, Source: sourceProxyScrape, Protocol: "socks5",
			Country: displayCountryName(cc, strings.TrimSpace(e.Country)), CountryCode: cc,
			Score: int(e.UptimePercent + 0.5), Ping: int(e.LatencyMS + 0.5), Uptime: e.UptimePercent / 100,
			ISP: strings.TrimSpace(e.ISP), SourceHits: 1,
		}
		byCountry[cc] = append(byCountry[cc], node)
	}

	countries := make([]string, 0, len(byCountry))
	for cc := range byCountry {
		countries = append(countries, cc)
	}
	sortStrings(countries)

	out := make([]Node, 0)
	for _, cc := range countries {
		nodes := byCountry[cc]
		sortCandidateQuality(nodes, now)
		if len(nodes) > proxyScrapeCountryLimit {
			nodes = nodes[:proxyScrapeCountryLimit]
		}
		out = append(out, nodes...)
	}
	return out
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
