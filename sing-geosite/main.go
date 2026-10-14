package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/common/geosite"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/google/go-github/v45/github"
	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"
)

var githubClient *github.Client

func init() {
	accessToken, loaded := os.LookupEnv("GITHUB_TOKEN")
	if !loaded {
		accessToken, loaded = os.LookupEnv("ACCESS_TOKEN")
	}
	if !loaded {
		githubClient = github.NewClient(nil)
		return
	}
	transport := &github.BasicAuthTransport{
		Username: accessToken,
	}
	githubClient = github.NewClient(transport.Client())
}

func fetch(from string) (*github.RepositoryRelease, error) {
	names := strings.SplitN(from, "/", 2)
	if len(names) < 2 {
		return nil, fmt.Errorf("invalid repository format: %s", from)
	}
	latestRelease, _, err := githubClient.Repositories.GetLatestRelease(context.Background(), names[0], names[1])
	if err != nil {
		return nil, err
	}
	return latestRelease, nil
}

func get(downloadURL *string) ([]byte, error) {
	log.Info("download ", *downloadURL)
	response, err := http.Get(*downloadURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return io.ReadAll(response.Body)
}

func download(release *github.RepositoryRelease) ([]byte, error) {
	geositeAsset := common.Find(release.Assets, func(it *github.ReleaseAsset) bool {
		return *it.Name == "geosite.dat"
	})
	geositeChecksumAsset := common.Find(release.Assets, func(it *github.ReleaseAsset) bool {
		return *it.Name == "geosite.dat.sha256sum"
	})
	if geositeAsset == nil {
		return nil, E.New("geosite asset not found in upstream release ", release.Name)
	}
	if geositeChecksumAsset == nil {
		return nil, E.New("geosite checksum asset not found in upstream release ", release.Name)
	}
	data, err := get(geositeAsset.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	remoteChecksum, err := get(geositeChecksumAsset.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	checksum := sha256.Sum256(data)
	if hex.EncodeToString(checksum[:]) != string(remoteChecksum[:64]) {
		return nil, E.New("checksum mismatch")
	}
	return data, nil
}

func parse(vGeositeData []byte) (map[string][]geosite.Item, error) {
	vGeositeList := routercommon.GeoSiteList{}
	err := proto.Unmarshal(vGeositeData, &vGeositeList)
	if err != nil {
		return nil, err
	}
	domainMap := make(map[string][]geosite.Item)
	for _, vGeositeEntry := range vGeositeList.Entry {
		code := strings.ToLower(vGeositeEntry.CountryCode)
		domains := make([]geosite.Item, 0, len(vGeositeEntry.Domain)*2)
		attributes := make(map[string][]*routercommon.Domain)
		for _, domain := range vGeositeEntry.Domain {
			if len(domain.Attribute) > 0 {
				for _, attribute := range domain.Attribute {
					attributes[attribute.Key] = append(attributes[attribute.Key], domain)
				}
			}
			switch domain.Type {
			case routercommon.Domain_Plain:
				domains = append(domains, geosite.Item{
					Type:  geosite.RuleTypeDomainKeyword,
					Value: domain.Value,
				})
			case routercommon.Domain_Regex:
				domains = append(domains, geosite.Item{
					Type:  geosite.RuleTypeDomainRegex,
					Value: domain.Value,
				})
			case routercommon.Domain_RootDomain:
				if strings.Contains(domain.Value, ".") {
					domains = append(domains, geosite.Item{
						Type:  geosite.RuleTypeDomain,
						Value: domain.Value,
					})
				}
				domains = append(domains, geosite.Item{
					Type:  geosite.RuleTypeDomainSuffix,
					Value: "." + domain.Value,
				})
			case routercommon.Domain_Full:
				domains = append(domains, geosite.Item{
					Type:  geosite.RuleTypeDomain,
					Value: domain.Value,
				})
			}
		}
		domainMap[code] = common.Uniq(domains)
		for attribute, attributeEntries := range attributes {
			attributeDomains := make([]geosite.Item, 0, len(attributeEntries)*2)
			for _, domain := range attributeEntries {
				switch domain.Type {
				case routercommon.Domain_Plain:
					attributeDomains = append(attributeDomains, geosite.Item{
						Type:  geosite.RuleTypeDomainKeyword,
						Value: domain.Value,
					})
				case routercommon.Domain_Regex:
					attributeDomains = append(attributeDomains, geosite.Item{
						Type:  geosite.RuleTypeDomainRegex,
						Value: domain.Value,
					})
				case routercommon.Domain_RootDomain:
					if strings.Contains(domain.Value, ".") {
						attributeDomains = append(attributeDomains, geosite.Item{
							Type:  geosite.RuleTypeDomain,
							Value: domain.Value,
						})
					}
					attributeDomains = append(attributeDomains, geosite.Item{
						Type:  geosite.RuleTypeDomainSuffix,
						Value: "." + domain.Value,
					})
				case routercommon.Domain_Full:
					attributeDomains = append(attributeDomains, geosite.Item{
						Type:  geosite.RuleTypeDomain,
						Value: domain.Value,
					})
				}
			}
			domainMap[code+"@"+attribute] = common.Uniq(attributeDomains)
		}
	}
	return domainMap, nil
}

// optimizeGeositeRule optimizes domain matching rules for sing-box rule-set (.srs / JSON).
// Features:
// 1. Cleans and normalizes domain_suffix (removes leading dot, sing-box natively matches root + subdomains).
// 2. Prunes redundant sub-domain suffixes if parent suffix already exists (e.g. drops *.music.apple.com if *.apple.com exists).
// 3. Prunes exact domains that are already covered by domain_suffix (e.g. drops news.apple.com if apple.com exists).
// 4. Normalizes to lowercase and sorts alphabetically for deterministic output.
func optimizeGeositeRule(items []geosite.Item) option.DefaultHeadlessRule {
	var (
		rawFull    []string
		rawSuffix  []string
		rawKeyword []string
		rawRegex   []string
	)

	for _, it := range items {
		val := strings.ToLower(strings.TrimSpace(it.Value))
		if val == "" {
			continue
		}
		switch it.Type {
		case geosite.RuleTypeDomain:
			rawFull = append(rawFull, val)
		case geosite.RuleTypeDomainSuffix:
			val = strings.TrimPrefix(val, ".")
			if val != "" {
				rawSuffix = append(rawSuffix, val)
			}
		case geosite.RuleTypeDomainKeyword:
			rawKeyword = append(rawKeyword, val)
		case geosite.RuleTypeDomainRegex:
			rawRegex = append(rawRegex, val)
		}
	}

	// 1. Optimize Suffixes:
	suffixSet := make(map[string]bool)
	for _, s := range rawSuffix {
		suffixSet[s] = true
	}
	sortedSuffixes := make([]string, 0, len(suffixSet))
	for s := range suffixSet {
		sortedSuffixes = append(sortedSuffixes, s)
	}
	sort.Slice(sortedSuffixes, func(i, j int) bool {
		if len(sortedSuffixes[i]) != len(sortedSuffixes[j]) {
			return len(sortedSuffixes[i]) < len(sortedSuffixes[j])
		}
		return sortedSuffixes[i] < sortedSuffixes[j]
	})

	var finalSuffixes []string
	finalSuffixSet := make(map[string]bool)
	for _, s := range sortedSuffixes {
		isSub := false
		for _, parent := range finalSuffixes {
			if strings.HasSuffix(s, "."+parent) {
				isSub = true
				break
			}
		}
		if !isSub {
			finalSuffixes = append(finalSuffixes, s)
			finalSuffixSet[s] = true
		}
	}
	sort.Strings(finalSuffixes)

	// 2. Optimize Full Domains:
	domainSet := make(map[string]bool)
	for _, d := range rawFull {
		if finalSuffixSet[d] {
			continue
		}
		isSub := false
		for _, parent := range finalSuffixes {
			if strings.HasSuffix(d, "."+parent) {
				isSub = true
				break
			}
		}
		if !isSub {
			domainSet[d] = true
		}
	}
	finalDomains := make([]string, 0, len(domainSet))
	for d := range domainSet {
		finalDomains = append(finalDomains, d)
	}
	sort.Strings(finalDomains)

	// 3. Keywords:
	kwSet := make(map[string]bool)
	for _, k := range rawKeyword {
		kwSet[k] = true
	}
	finalKeywords := make([]string, 0, len(kwSet))
	for k := range kwSet {
		finalKeywords = append(finalKeywords, k)
	}
	sort.Strings(finalKeywords)

	// 4. Regexes:
	rgSet := make(map[string]bool)
	for _, r := range rawRegex {
		rgSet[r] = true
	}
	finalRegex := make([]string, 0, len(rgSet))
	for r := range rgSet {
		finalRegex = append(finalRegex, r)
	}
	sort.Strings(finalRegex)

	return option.DefaultHeadlessRule{
		Domain:        finalDomains,
		DomainSuffix:  finalSuffixes,
		DomainKeyword: finalKeywords,
		DomainRegex:   finalRegex,
	}
}

func generate(release *github.RepositoryRelease, output string, cnOutput string, ruleSetOutput string) error {
	vData, err := download(release)
	if err != nil {
		return err
	}
	domainMap, err := parse(vData)
	if err != nil {
		return err
	}

	// Legacy geosite.db generation
	outputPath, _ := filepath.Abs(output)
	os.Stderr.WriteString("write " + outputPath + "\n")
	outputFile, err := os.Create(output)
	if err != nil {
		return err
	}
	defer outputFile.Close()
	err = geosite.Write(bufio.NewWriter(outputFile), domainMap)
	if err != nil {
		return err
	}

	cnCodes := []string{
		"cn",
		"geolocation-!cn",
		"category-companies@cn",
	}
	cnDomainMap := make(map[string][]geosite.Item)
	for _, cnCode := range cnCodes {
		cnDomainMap[cnCode] = domainMap[cnCode]
	}
	cnOutputFile, err := os.Create(cnOutput)
	if err != nil {
		return err
	}
	defer cnOutputFile.Close()
	err = geosite.Write(bufio.NewWriter(cnOutputFile), cnDomainMap)
	if err != nil {
		return err
	}

	// Optimized sing-box rule-set (.srs and .json) generation with concurrent worker pool
	os.RemoveAll(ruleSetOutput)
	err = os.MkdirAll(ruleSetOutput, 0o755)
	if err != nil {
		return err
	}

	type geositeTask struct {
		code  string
		items []geosite.Item
	}

	tasks := make(chan geositeTask, len(domainMap))
	for code, items := range domainMap {
		tasks <- geositeTask{code: code, items: items}
	}
	close(tasks)

	numWorkers := runtime.NumCPU() * 2
	if numWorkers < 4 {
		numWorkers = 4
	}

	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				if firstErr != nil {
					return
				}

				headlessRule := optimizeGeositeRule(t.items)
				if len(headlessRule.Domain) == 0 &&
					len(headlessRule.DomainSuffix) == 0 &&
					len(headlessRule.DomainKeyword) == 0 &&
					len(headlessRule.DomainRegex) == 0 {
					continue
				}

				plainRuleSet := option.PlainRuleSet{
					Rules: []option.HeadlessRule{
						{
							Type:           C.RuleTypeDefault,
							DefaultOptions: headlessRule,
						},
					},
				}

				// Binary SRS
				srsPath := filepath.Join(ruleSetOutput, "geosite-"+t.code+".srs")
				outputRuleSet, err := os.Create(srsPath)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
				err = srs.Write(outputRuleSet, plainRuleSet, 3)
				outputRuleSet.Close()
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}

				// Formatted JSON
				jsonPath := filepath.Join(ruleSetOutput, "geosite-"+t.code+".json")
				outputJSON, err := os.Create(jsonPath)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
				encoder := json.NewEncoder(outputJSON)
				versionPlainRuleSet := option.PlainRuleSetCompat{
					Options: plainRuleSet,
					Version: 3,
				}
				encoder.SetIndent("", "  ")
				encoder.SetEscapeHTML(false)
				err = encoder.Encode(versionPlainRuleSet)
				outputJSON.Close()
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	return nil
}

func setActionOutput(name string, content string) {
	outputFile := os.Getenv("GITHUB_OUTPUT")
	if outputFile != "" {
		f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			defer f.Close()
			f.WriteString(fmt.Sprintf("%s=%s\n", name, content))
		}
	}
	os.Stdout.WriteString(fmt.Sprintf("::set-output name=%s::%s\n", name, content))
	log.Info(fmt.Sprintf("action output %s=%s", name, content))
}

func release(source string, destination string, output string, cnOutput string, ruleSetOutput string) error {
	sourceRelease, err := fetch(source)
	if err != nil {
		return err
	}

	sourceTag := ""
	if sourceRelease.TagName != nil {
		sourceTag = *sourceRelease.TagName
	}
	if sourceTag == "" && sourceRelease.Name != nil {
		sourceTag = *sourceRelease.Name
	}

	if destination != "" {
		destinationRelease, err := fetch(destination)
		if err != nil {
			log.Warn("missing destination latest release: ", err)
		} else if destinationRelease != nil {
			destTag := ""
			if destinationRelease.TagName != nil {
				destTag = *destinationRelease.TagName
			}
			destName := ""
			if destinationRelease.Name != nil {
				destName = *destinationRelease.Name
			}
			if os.Getenv("NO_SKIP") != "true" && sourceTag != "" && (strings.Contains(destTag, sourceTag) || strings.Contains(destName, sourceTag)) {
				log.Info("already latest: ", sourceTag)
				setActionOutput("skip", "true")
				setActionOutput("tag", sourceTag)
				return nil
			}
		}
	}

	err = generate(sourceRelease, output, cnOutput, ruleSetOutput)
	if err != nil {
		return err
	}
	setActionOutput("tag", sourceTag)
	setActionOutput("skip", "false")
	return nil
}

func main() {
	dest := os.Getenv("GITHUB_REPOSITORY")
	if dest == "" {
		dest = "wx2020/sing-box-rules"
	}
	err := release(
		"Loyalsoldier/v2ray-rules-dat",
		dest,
		"geosite.db",
		"geosite-cn.db",
		"rule-set",
	)
	if err != nil {
		log.Fatal(err)
	}
}
