package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const osvAPI = "https://api.osv.dev/v1"

var exactOSVVersionPattern = regexp.MustCompile(`^[vV]?[0-9][A-Za-z0-9.+_-]{0,79}$`)

func isExactOSVVersion(version string) bool {
	if !exactOSVVersionPattern.MatchString(version) {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(version), ".") {
		if part == "x" {
			return false
		}
	}
	return true
}

type osvEdge struct{ ecosystem, name, version string }
type osvQuery struct {
	Package struct {
		Ecosystem string `json:"ecosystem"`
		Name      string `json:"name"`
	} `json:"package"`
	Version string `json:"version"`
}
type osvVulnerability struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary"`
	Details  string   `json:"details"`
	Aliases  []string `json:"aliases"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	Affected []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		Ranges []struct {
			Events []map[string]string `json:"events"`
		} `json:"ranges"`
		Versions []string `json:"versions"`
	} `json:"affected"`
}
type osvPersisted struct{ ID, Summary, Package, Ecosystem, Version, Patched, URL, Severity string }

func queryOSV(ctx context.Context, client *http.Client, base string, edges []osvEdge) ([]osvPersisted, error) {
	queries := make([]osvQuery, 0, len(edges))
	for _, edge := range edges {
		ecosystem := map[string]string{"go": "Go", "npm": "npm", "pypi": "PyPI"}[edge.ecosystem]
		if ecosystem == "" || edge.name == "" || !isExactOSVVersion(edge.version) {
			continue
		}
		var query osvQuery
		query.Package.Ecosystem, query.Package.Name, query.Version = ecosystem, edge.name, edge.version
		queries = append(queries, query)
		if len(queries) == 100 {
			break
		}
	}
	if len(queries) == 0 {
		return nil, nil
	}
	requestBody, err := json.Marshal(struct {
		Queries []osvQuery `json:"queries"`
	}{queries})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/querybatch", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OSV returned HTTP %d", response.StatusCode)
	}
	var batch struct {
		Results []struct {
			Vulns []struct {
				ID string `json:"id"`
			} `json:"vulns"`
		} `json:"results"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&batch); err != nil {
		return nil, err
	}
	type osvRef struct {
		id   string
		edge osvEdge
	}
	ids := map[string]osvRef{}
	for i, result := range batch.Results {
		if i >= len(queries) {
			break
		}
		for _, vuln := range result.Vulns {
			if validOSVID(vuln.ID) && len(ids) < 20 {
				edge := osvEdge{ecosystem: queries[i].Package.Ecosystem, name: queries[i].Package.Name, version: queries[i].Version}
				key := vuln.ID + "\x00" + edge.ecosystem + "\x00" + edge.name + "\x00" + edge.version
				ids[key] = osvRef{id: vuln.ID, edge: edge}
			}
		}
	}
	items := make([]osvPersisted, 0, len(ids))
	for _, ref := range ids {
		id, edge := ref.id, ref.edge
		detailReq, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/vulns/"+id, nil)
		if reqErr != nil {
			continue
		}
		detail, getErr := client.Do(detailReq)
		if getErr != nil {
			continue
		}
		var vuln osvVulnerability
		decodeErr := json.NewDecoder(io.LimitReader(detail.Body, 1<<20)).Decode(&vuln)
		_ = detail.Body.Close()
		if decodeErr != nil || detail.StatusCode != http.StatusOK || vuln.ID != id {
			continue
		}
		pkg, ecosystem, patched := edge.name, edge.ecosystem, ""
		for _, affected := range vuln.Affected {
			if !sameOSVPackage(affected.Package.Name, edge.name) || !sameOSVEcosystem(affected.Package.Ecosystem, edge.ecosystem) {
				continue
			}
			for _, version := range affected.Versions {
				if version == edge.version {
					for _, r := range affected.Ranges {
						for _, event := range r.Events {
							if fixed := event["fixed"]; fixed != "" {
								patched = fixed
								break
							}
						}
					}
				}
			}
			for _, r := range affected.Ranges {
				for _, event := range r.Events {
					if fixed := event["fixed"]; patched == "" && fixed != "" {
						patched = fixed
					}
				}
			}
			break
		}
		summary := strings.TrimSpace(vuln.Summary)
		if summary == "" {
			summary = "OSV advisory affects " + pkg
		}
		if len(summary) > 300 {
			summary = summary[:300]
		}
		items = append(items, osvPersisted{ID: id, Summary: summary, Package: pkg, Ecosystem: strings.ToLower(ecosystem), Version: edge.version, Patched: patched, URL: "https://osv.dev/vulnerability/" + id, Severity: osvSeverity(vuln.Severity)})
	}
	return items, nil
}

func validOSVID(id string) bool {
	if len(id) < 3 || len(id) > 120 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func sameOSVPackage(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func sameOSVEcosystem(upstream, local string) bool {
	want := map[string]string{"go": "Go", "npm": "npm", "pypi": "PyPI"}[local]
	return want != "" && strings.EqualFold(strings.TrimSpace(upstream), want)
}

func osvSeverity(severity []struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}) string {
	for _, item := range severity {
		var score float64
		if _, err := fmt.Sscanf(item.Score, "%f", &score); err == nil {
			switch {
			case score >= 9:
				return "critical"
			case score >= 7:
				return "high"
			case score >= 4:
				return "medium"
			case score > 0:
				return "low"
			}
		}
	}
	return "medium"
}

func osvCode(repositoryID, advisoryID, packageName, version string) string {
	sum := sha256.Sum256([]byte(repositoryID + ":" + advisoryID + ":" + packageName + ":" + version))
	return "OSV-" + strings.ToUpper(hex.EncodeToString(sum[:16]))
}
