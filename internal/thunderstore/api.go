package thunderstore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	baseURL         = "https://thunderstore.io"
	experimentalAPI = baseURL + "/api/experimental/package/"
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
// Accepts "Owner-Name", "Owner-Name-Version", or a Thunderstore URL.
func FindPackageByQuery(query string) (*Package, error) {
	// Try parsing as a Thunderstore URL (e.g., https://thunderstore.io/c/valheim/p/Owner/Name/)
	if strings.HasPrefix(query, "https://thunderstore.io/") || strings.HasPrefix(query, "http://thunderstore.io/") {
		parts := strings.Split(strings.Trim(query, "/"), "/")
		// URL format: .../c/{community}/p/{owner}/{name}
		for i, p := range parts {
			if p == "p" && i+2 < len(parts) {
				pkg, err := GetPackage(parts[i+1], parts[i+2])
				if err == nil {
					return pkg, nil
				}
				return nil, fmt.Errorf("could not fetch package from URL: %w", err)
			}
		}
		return nil, fmt.Errorf("could not parse Thunderstore URL: %s", query)
	}

	// Try parsing as Owner-Name-Version first (e.g., "warpalicious-Praetoris-1.1.16")
	ref := ParseDep(query)
	if ref.Owner != "" && ref.Name != "" {
		pkg, err := GetPackage(ref.Owner, ref.Name)
		if err == nil {
			return pkg, nil
		}
	}

	// Try as Owner-Name (e.g., "warpalicious-Praetoris")
	parts := strings.SplitN(query, "-", 2)
	if len(parts) == 2 {
		pkg, err := GetPackage(parts[0], parts[1])
		if err == nil {
			return pkg, nil
		}
	}

	return nil, fmt.Errorf("no package found matching '%s' — use Owner-Name format or a Thunderstore URL", query)
}
