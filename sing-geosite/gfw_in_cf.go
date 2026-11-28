package main

// geosite-gfw-in-cf：把 geosite-gfw 中「上游解析结果落在 Cloudflare 官方段」的域名
// 过滤成独立 rule-set（.srs），供 sing-box 在 cf-pool 直连规则之前按域名把这些
// 域名导向 proxy（避免被 GFW 按 SNI 阻断的 CF 站误入直连）。
//
// 运行环境：GitHub Actions（境外）⇒ net.LookupIP 直连系统 resolver 即真实答案，
// 无需 DoH / 代理。若在本地（境内）跑，解析结果可能被污染，仅供代码验证。
//
// 输出：rule-set/geosite-gfw-in-cf.srs + .json，随主流程一起发布到
// rule-set-geosite 分支与 Release assets（release.yaml 的 POPULAR_GEOSITE 列表）。

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/common/geosite"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// cloudflareCIDRs 是 Cloudflare 官方公布的 anycast 网段（IPv4 15 段 + IPv6 7 段）。
// https://www.cloudflare.com/ips/
var cloudflareCIDRs = func() []netip.Prefix {
	lines := strings.Fields(strings.NewReplacer("\n", " ", "\r", " ").Replace(`
173.245.48.0/20
103.21.244.0/22
103.22.200.0/22
103.31.4.0/22
141.101.64.0/18
108.162.192.0/18
190.93.240.0/20
188.114.96.0/20
197.234.240.0/22
198.41.128.0/17
162.158.0.0/15
104.16.0.0/13
104.24.0.0/14
172.64.0.0/13
131.0.72.0/22
2400:cb00::/32
2606:4700::/32
2803:f800::/32
2405:b500::/32
2405:8100::/32
2a06:98c0::/29
2c0f:f248::/32`))
	out := make([]netip.Prefix, 0, len(lines))
	for _, l := range lines {
		p, err := netip.ParsePrefix(l)
		if err == nil {
			out = append(out, p)
		}
	}
	return out
}()

func ipInCloudflare(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, p := range cloudflareCIDRs {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// domainResolvesToCF 用 DoH（8.8.8.8 / 1.1.1.1）解析域名，解析结果任一 IP
// 落在 CF 官方段即判命中。CI 在境外直连 DoH——不用系统 resolver（net.LookupIP，
// 它对 x.com 返回 Twitter 真实 IP 157.240.x 而非 CF 边缘，导致 x.com 漏判），
// 更不能用国内 UDP（223.5.5.5 返回污染/调度 IP）。DoH 与设备上 smartdns 的
// default 组（8.8.8.8/1.1.1.1 via proxy）同视角，能拿到 CF anycast IP。
func domainResolvesToCF(domain string) bool {
	urls := []string{
		"https://dns.google/resolve?name=" + domain + "&type=A",
		"https://cloudflare-dns.com/dns-query?name=" + domain + "&type=A",
	}
	for _, u := range urls {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		if strings.Contains(u, "cloudflare-dns") {
			req.Header.Set("accept", "application/dns-json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		var doc struct {
			Answer []struct {
				Type int    `json:"type"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		err = json.NewDecoder(resp.Body).Decode(&doc)
		resp.Body.Close()
		if err != nil {
			continue
		}
		for _, a := range doc.Answer {
			if a.Type != 1 {
				continue
			}
			if ip := net.ParseIP(a.Data); ip != nil && ipInCloudflare(ip) {
				return true
			}
		}
	}
	return false
}

// buildGFWInCF 从 domainMap["gfw"] 过滤 CF 域名，产出 geosite-gfw-in-cf.{srs,json}。
// 在主流程生成 rule-set 后调用；失败不致命（仅打印警告，不影响其它 rule-set 发布）。
func buildGFWInCF(domainMap map[string][]geosite.Item, ruleSetOutput string) error {
	items, ok := domainMap["gfw"]
	if !ok || len(items) == 0 {
		return fmt.Errorf("geosite-gfw not found in domainMap")
	}

	// 归一化域名（大小写、去前导点、去通配），去重
	set := map[string]struct{}{}
	for _, it := range items {
		v := strings.ToLower(strings.TrimSpace(it.Value))
		v = strings.TrimPrefix(v, ".")
		if v == "" || strings.ContainsAny(v, "*?") || !strings.Contains(v, ".") {
			continue
		}
		set[v] = struct{}{}
	}
	domains := make([]string, 0, len(set))
	for d := range set {
		domains = append(domains, d)
	}
	sort.Strings(domains)

	// 并发解析（境外 runner 上系统 resolver 即真实答案）
	workers := 64
	if n := runtime.NumCPU() * 4; n > workers {
		workers = n
	}
	type job struct {
		idx    int
		domain string
	}
	type result struct {
		domain string
		hit    bool
	}
	jobs := make(chan job, len(domains))
	for i, d := range domains {
		jobs <- job{idx: i, domain: d}
	}
	close(jobs)

	results := make([]result, len(domains))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results[j.idx] = result{domain: j.domain, hit: domainResolvesToCF(j.domain)}
			}
		}()
	}
	wg.Wait()

	hits := make([]string, 0)
	for _, r := range results {
		if r.hit && r.domain != "" {
			hits = append(hits, r.domain)
		}
	}
	sort.Strings(hits)
	fmt.Printf("geosite-gfw-in-cf: %d/%d domains resolve into Cloudflare ranges\n", len(hits), len(domains))

	// 复用主流程的 optimizeGeositeRule，产出 rule items
	ruleItems := make([]geosite.Item, 0, len(hits))
	for _, d := range hits {
		ruleItems = append(ruleItems,
			geosite.Item{Type: geosite.RuleTypeDomain, Value: d},
			geosite.Item{Type: geosite.RuleTypeDomainSuffix, Value: "." + d},
		)
	}
	headless := optimizeGeositeRule(ruleItems)
	plain := option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type:           C.RuleTypeDefault,
		DefaultOptions: headless,
	}}}

	srsPath := filepath.Join(ruleSetOutput, "geosite-gfw-in-cf.srs")
	f, err := os.Create(srsPath)
	if err != nil {
		return err
	}
	if err := srs.Write(f, plain, 3); err != nil {
		f.Close()
		return err
	}
	f.Close()

	jsonPath := filepath.Join(ruleSetOutput, "geosite-gfw-in-cf.json")
	jf, err := os.Create(jsonPath)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(jf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	err = enc.Encode(option.PlainRuleSetCompat{Options: plain, Version: 3})
	jf.Close()
	return err
}
