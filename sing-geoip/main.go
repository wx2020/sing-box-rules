package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"go4.org/netipx"

	"github.com/google/go-github/v45/github"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/inserter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/oschwald/geoip2-golang"
	"github.com/oschwald/maxminddb-golang"
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
	geoipAsset := common.Find(release.Assets, func(it *github.ReleaseAsset) bool {
		return *it.Name == "Country.mmdb"
	})
	if geoipAsset == nil {
		return nil, E.New("Country.mmdb not found in upstream release ", release.Name)
	}
	return get(geoipAsset.BrowserDownloadURL)
}

func parse(binary []byte) (metadata maxminddb.Metadata, countryMap map[string][]*net.IPNet, err error) {
	database, err := maxminddb.FromBytes(binary)
	if err != nil {
		return
	}
	metadata = database.Metadata
	networks := database.Networks(maxminddb.SkipAliasedNetworks)
	countryMap = make(map[string][]*net.IPNet)
	var country geoip2.Enterprise
	var ipNet *net.IPNet
	for networks.Next() {
		ipNet, err = networks.Network(&country)
		if err != nil {
			return
		}
		var code string
		if country.Country.IsoCode != "" {
			code = strings.ToLower(country.Country.IsoCode)
		} else if country.RegisteredCountry.IsoCode != "" {
			code = strings.ToLower(country.RegisteredCountry.IsoCode)
		} else if country.RepresentedCountry.IsoCode != "" {
			code = strings.ToLower(country.RepresentedCountry.IsoCode)
		} else if country.Continent.Code != "" {
			code = strings.ToLower(country.Continent.Code)
		} else {
			continue
		}
		countryMap[code] = append(countryMap[code], ipNet)
	}
	err = networks.Err()
	return
}

func newWriter(metadata maxminddb.Metadata, codes []string) (*mmdbwriter.Tree, error) {
	return mmdbwriter.New(mmdbwriter.Options{
		DatabaseType:            "sing-geoip",
		Languages:               codes,
		IPVersion:               int(metadata.IPVersion),
		RecordSize:              int(metadata.RecordSize),
		Inserter:                inserter.ReplaceWith,
		DisableIPv4Aliasing:     true,
		IncludeReservedNetworks: true,
	})
}

func write(writer *mmdbwriter.Tree, dataMap map[string][]*net.IPNet, output string, codes []string) error {
	if len(codes) == 0 {
		codes = make([]string, 0, len(dataMap))
		for code := range dataMap {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	codeMap := make(map[string]bool)
	for _, code := range codes {
		codeMap[code] = true
	}
	for code, data := range dataMap {
		if !codeMap[code] {
			continue
		}
		for _, item := range data {
			err := writer.Insert(item, mmdbtype.String(code))
			if err != nil {
				return err
			}
		}
	}
	outputFile, err := os.Create(output)
	if err != nil {
		return err
	}
	defer outputFile.Close()
	_, err = writer.WriteTo(outputFile)
	return err
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

func release(source string, destination string, output string, ruleSetOutput string) error {
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

	binary, err := download(sourceRelease)
	if err != nil {
		return err
	}
	metadata, countryMap, err := parse(binary)
	if err != nil {
		return err
	}
	allCodes := make([]string, 0, len(countryMap))
	for code := range countryMap {
		allCodes = append(allCodes, code)
	}

	// Legacy geoip.db generation
	writer, err := newWriter(metadata, allCodes)
	if err != nil {
		return err
	}
	err = write(writer, countryMap, output, nil)
	if err != nil {
		return err
	}

	writer, err = newWriter(metadata, []string{"cn"})
	if err != nil {
		return err
	}
	err = write(writer, countryMap, "geoip-cn.db", []string{"cn"})
	if err != nil {
		return err
	}

	// Optimized rule-set (.srs and .json) generation with concurrent worker pool
	os.RemoveAll(ruleSetOutput)
	err = os.MkdirAll(ruleSetOutput, 0o755)
	if err != nil {
		return err
	}

	type geoipTask struct {
		countryCode string
		ipNets      []*net.IPNet
	}

	tasks := make(chan geoipTask, len(countryMap))
	for countryCode, ipNets := range countryMap {
		tasks <- geoipTask{countryCode: countryCode, ipNets: ipNets}
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

				// Aggregate and merge contiguous / overlapping CIDRs
				var builder netipx.IPSetBuilder
				for _, cidr := range t.ipNets {
					if prefix, err := netip.ParsePrefix(cidr.String()); err == nil {
						builder.AddPrefix(prefix)
					}
				}
				ipSet, err := builder.IPSet()
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
				prefixes := ipSet.Prefixes()

				var headlessRule option.DefaultHeadlessRule
				headlessRule.IPCIDR = make([]string, len(prefixes))
				for idx, p := range prefixes {
					headlessRule.IPCIDR[idx] = p.String()
				}

				var plainRuleSet option.PlainRuleSet
				plainRuleSet.Rules = []option.HeadlessRule{
					{
						Type:           C.RuleTypeDefault,
						DefaultOptions: headlessRule,
					},
				}

				// Binary SRS
				srsPath := filepath.Join(ruleSetOutput, "geoip-"+t.countryCode+".srs")
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
				jsonPath := filepath.Join(ruleSetOutput, "geoip-"+t.countryCode+".json")
				outputJSON, err := os.Create(jsonPath)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
				je := json.NewEncoder(outputJSON)
				versionPlainRuleSet := option.PlainRuleSetCompat{
					Options: plainRuleSet,
					Version: 3,
				}
				je.SetEscapeHTML(false)
				je.SetIndent("", "    ")
				err = je.Encode(versionPlainRuleSet)
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

	setActionOutput("tag", sourceTag)
	setActionOutput("skip", "false")
	return nil
}

func main() {
	dest := os.Getenv("GITHUB_REPOSITORY")
	if dest == "" {
		dest = "wx2020/sing-box-rules"
	}
	err := release("Loyalsoldier/geoip", dest, "geoip.db", "rule-set")
	if err != nil {
		log.Fatal(err)
	}
}
