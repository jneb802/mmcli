package thunderstore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	baseURL         = "https://thunderstore.io"
	experimentalAPI = baseURL + "/api/experimental/package/"
	v1API           = baseURL + "/c/valheim/api/v1/package/"
	cacheTTL        = 15 * time.Minute
)

// GetPackage fetches a single package from the experimental API.
func GetPackage(owner, name string) (*Package, error) {
	url := fmt.Sprintf("%s%s/%s/", experimentalAPI, owner, name)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("package %s-%s not found (HTTP %d)", owner, name, resp.StatusCode)
	}

	var expPkg ExperimentalPackage
	if err := json.NewDecoder(resp.Body).Decode(&expPkg); err != nil {
		return nil, fmt.Errorf("failed to decode package response: %w", err)
	}

	// Convert to Package type
	pkg := &Package{
		Owner:    expPkg.Namespace,
		Name:     expPkg.Name,
		FullName: expPkg.FullName,
		Versions: []Version{
			{
				Name:          expPkg.Name,
				FullName:      fmt.Sprintf("%s-%s-%s", expPkg.Namespace, expPkg.Name, expPkg.LatestVersion.VersionNumber),
				VersionNumber: expPkg.LatestVersion.VersionNumber,
				DownloadURL:   expPkg.LatestVersion.DownloadURL,
				Dependencies:  expPkg.LatestVersion.Dependencies,
				Description:   expPkg.LatestVersion.Description,
				FileSize:      expPkg.LatestVersion.FileSize,
			},
		},
	}
	return pkg, nil
}

// Search fetches the full v1 package list (cached), filters by query, and returns top 20 results.
func Search(query, cacheDir string) ([]Package, error) {
	packages, err := fetchPackageList(cacheDir)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(query)
	var results []Package
	for _, pkg := range packages {
		if pkg.IsDeprecated {
			continue
		}
		name := strings.ToLower(pkg.Name)
		fullName := strings.ToLower(pkg.FullName)
		desc := ""
		if len(pkg.Versions) > 0 {
			desc = strings.ToLower(pkg.Versions[0].Description)
		}
		if strings.Contains(name, query) || strings.Contains(fullName, query) || strings.Contains(desc, query) {
			results = append(results, pkg)
		}
	}

	// Sort by total downloads (sum of all versions' downloads, approximated by first version)
	sort.Slice(results, func(i, j int) bool {
		di, dj := 0, 0
		if len(results[i].Versions) > 0 {
			di = results[i].Versions[0].Downloads
		}
		if len(results[j].Versions) > 0 {
			dj = results[j].Versions[0].Downloads
		}
		return di > dj
	})

	if len(results) > 20 {
		results = results[:20]
	}
	return results, nil
}

func fetchPackageList(cacheDir string) ([]Package, error) {
	cacheFile := cacheDir + "/packages.json"

	// Check cache freshness
	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) < cacheTTL {
			data, err := os.ReadFile(cacheFile)
			if err == nil {
				var packages []Package
				if err := json.Unmarshal(data, &packages); err == nil {
					return packages, nil
				}
			}
		}
	}

	fmt.Println("Fetching package index from Thunderstore...")
	resp, err := http.Get(v1API)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Thunderstore API returned HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read package list: %w", err)
	}

	var packages []Package
	if err := json.Unmarshal(data, &packages); err != nil {
		return nil, fmt.Errorf("failed to parse package list: %w", err)
	}

	// Save to cache
	os.MkdirAll(cacheDir, 0755)
	os.WriteFile(cacheFile, data, 0644)

	return packages, nil
}

// ResolveDependencies resolves all dependencies for a package recursively.
// Returns packages in topological order (dependencies first).
// Skips BepInExPack_Valheim and already-installed mods.
func ResolveDependencies(pkg *Package, installed map[string]bool) ([]DepRef, error) {
	if len(pkg.Versions) == 0 {
		return nil, fmt.Errorf("package %s has no versions", pkg.FullName)
	}

	var result []DepRef
	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var dfs func(deps []string) error
	dfs = func(deps []string) error {
		for _, dep := range deps {
			ref := ParseDep(dep)
			fullName := fmt.Sprintf("%s-%s", ref.Owner, ref.Name)

			// Skip BepInExPack
			if ref.Name == "BepInExPack_Valheim" || ref.Name == "BepInEx_pack" {
				continue
			}

			// Skip already installed
			if installed[fullName] {
				continue
			}

			// Skip already visited
			if visited[fullName] {
				continue
			}

			// Cycle detection
			if inStack[fullName] {
				continue // Skip cycles silently
			}

			inStack[fullName] = true

			// Fetch the dependency package to get its deps
			depPkg, err := GetPackage(ref.Owner, ref.Name)
			if err != nil {
				// Non-fatal: some deps may not resolve
				fmt.Printf("  Warning: could not resolve dependency %s: %v\n", fullName, err)
				inStack[fullName] = false
				continue
			}

			if len(depPkg.Versions) > 0 {
				if err := dfs(depPkg.Versions[0].Dependencies); err != nil {
					return err
				}
			}

			inStack[fullName] = false
			visited[fullName] = true
			result = append(result, DepRef{
				Owner:   ref.Owner,
				Name:    ref.Name,
				Version: ref.Version,
			})
		}
		return nil
	}

	if err := dfs(pkg.Versions[0].Dependencies); err != nil {
		return nil, err
	}

	return result, nil
}

// FindPackageByQuery searches for a package by query string.
// Accepts "Name" or "Owner-Name" format.
func FindPackageByQuery(query, cacheDir string) (*Package, error) {
	// Try direct lookup first if it looks like Owner-Name
	parts := strings.SplitN(query, "-", 2)
	if len(parts) == 2 {
		pkg, err := GetPackage(parts[0], parts[1])
		if err == nil {
			return pkg, nil
		}
	}

	// Search and return best match
	results, err := Search(query, cacheDir)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no package found matching '%s'", query)
	}

	// Exact name match takes priority
	queryLower := strings.ToLower(query)
	for _, pkg := range results {
		if strings.ToLower(pkg.Name) == queryLower {
			// Fetch full info from experimental API
			return GetPackage(pkg.Owner, pkg.Name)
		}
	}

	// Return first result (highest downloads)
	best := results[0]
	return GetPackage(best.Owner, best.Name)
}
