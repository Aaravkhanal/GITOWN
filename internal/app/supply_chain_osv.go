package app

import (
	"context"
	"net/http"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

// refreshExternalAdvisories is deliberately opt-in: package names and exact
// versions from a repository are sent to OSV only when the operator enables it.
func (a *App) refreshExternalAdvisories(ctx context.Context, repo *Repository) (int, string, error) {
	if !a.cfg.OSVEnabled {
		return 0, "disabled_private_metadata_opt_in_required", nil
	}
	var edges []osvEdge
	for _, item := range []struct{ path, ecosystem string }{{"go.mod", "go"}, {"package.json", "npm"}, {"requirements.txt", "pypi"}} {
		data, err := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, item.path)
		if err != nil {
			continue
		}
		var deps [][2]string
		switch item.ecosystem {
		case "go":
			deps = parseGoMod(string(data))
		case "npm":
			deps = parsePackageJSON(string(data))
		case "pypi":
			deps = parseRequirements(string(data))
		}
		for _, dep := range deps {
			edges = append(edges, osvEdge{ecosystem: item.ecosystem, name: dep[0], version: dep[1]})
		}
	}
	exactCount := 0
	for _, edge := range edges {
		if isExactOSVVersion(edge.version) {
			exactCount++
		}
	}
	if exactCount == 0 {
		return 0, "no_exact_versions", nil
	}
	items, err := queryOSV(ctx, &http.Client{Timeout: 4 * time.Second}, osvAPI, edges)
	if err != nil {
		return 0, "unavailable", nil
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return 0, "unavailable", err
	}
	defer tx.Rollback(ctx)
	for _, item := range items {
		if _, err = tx.Exec(ctx, `INSERT INTO security_advisories(id,repository_id,author_id,code,severity,summary,package_name,ecosystem,patched_version,state,external_source,external_id,external_url,external_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'published','osv',$10,$11,$12) ON CONFLICT(repository_id,external_source,external_id,package_name,external_version) WHERE external_source <> '' AND external_id <> '' DO UPDATE SET severity=excluded.severity,summary=excluded.summary,ecosystem=excluded.ecosystem,patched_version=excluded.patched_version,state='published',external_url=excluded.external_url`, auth.ID(), repo.ID, repo.OwnerID, osvCode(repo.ID, item.ID, item.Package, item.Version), item.Severity, item.Summary, item.Package, item.Ecosystem, item.Patched, item.ID, item.URL, item.Version); err != nil {
			return 0, "unavailable", err
		}
	}
	rows, err := tx.Query(ctx, `SELECT s.id::text,e.manifest,e.package_name,e.version FROM dependency_edges e JOIN security_advisories s ON s.repository_id=e.repository_id AND s.package_name=e.package_name AND s.ecosystem=e.ecosystem AND s.state='published' AND s.external_source='osv' AND s.external_version=e.version WHERE e.repository_id=$1`, repo.ID)
	if err != nil {
		return 0, "unavailable", err
	}
	type alert struct{ advisory, manifest, packageName, version string }
	var alerts []alert
	for rows.Next() {
		var item alert
		if err = rows.Scan(&item.advisory, &item.manifest, &item.packageName, &item.version); err != nil {
			rows.Close()
			return 0, "unavailable", err
		}
		alerts = append(alerts, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, "unavailable", err
	}
	rows.Close()
	for _, item := range alerts {
		if _, err = tx.Exec(ctx, `INSERT INTO vulnerability_alerts(id,repository_id,advisory_id,manifest,package_name,installed_version) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, auth.ID(), repo.ID, item.advisory, item.manifest, item.packageName, item.version); err != nil {
			return 0, "unavailable", err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, "unavailable", err
	}
	return len(items), "ok", nil
}
