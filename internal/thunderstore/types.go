package thunderstore

type Package struct {
	Owner             string    `json:"owner"`
	Name              string    `json:"name"`
	FullName          string    `json:"full_name"`
	PackageURL        string    `json:"package_url"`
	RatingScore       int       `json:"rating_score"`
	IsPinned          bool      `json:"is_pinned"`
	IsDeprecated      bool      `json:"is_deprecated"`
	Categories        []string  `json:"categories"`
	Versions          []Version `json:"versions"`
}

type Version struct {
	Name          string   `json:"name"`
	FullName      string   `json:"full_name"`
	Description   string   `json:"description"`
	VersionNumber string   `json:"version_number"`
	Dependencies  []string `json:"dependencies"`
	DownloadURL   string   `json:"download_url"`
	Downloads     int      `json:"downloads"`
	FileSize      int64    `json:"file_size"`
}

// ExperimentalPackage is the response from the experimental API endpoint.
type ExperimentalPackage struct {
	Namespace   string              `json:"namespace"`
	Name        string              `json:"name"`
	FullName    string              `json:"full_name"`
	LatestVersion ExperimentalVersion `json:"latest"`
}

type ExperimentalVersion struct {
	VersionNumber string   `json:"version_number"`
	DownloadURL   string   `json:"download_url"`
	Dependencies  []string `json:"dependencies"`
	Description   string   `json:"description"`
	FileSize      int64    `json:"file_size"`
}

type DepRef struct {
	Owner   string
	Name    string
	Version string
}

func ParseDep(dep string) DepRef {
	// Format: "Owner-Name-1.0.0"
	// Find last two dashes
	var ref DepRef
	parts := splitDep(dep)
	if len(parts) == 3 {
		ref.Owner = parts[0]
		ref.Name = parts[1]
		ref.Version = parts[2]
	}
	return ref
}

func splitDep(s string) []string {
	// Split "Owner-Name-1.0.0" into ["Owner", "Name", "1.0.0"]
	// Name can contain dashes, version is always last segment
	// Strategy: find the last dash that separates version (digits and dots)
	lastDash := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			lastDash = i
			break
		}
	}
	if lastDash <= 0 {
		return nil
	}
	version := s[lastDash+1:]
	rest := s[:lastDash]

	// Find the first dash in rest to split owner from name
	firstDash := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '-' {
			firstDash = i
			break
		}
	}
	if firstDash <= 0 {
		return nil
	}
	owner := rest[:firstDash]
	name := rest[firstDash+1:]
	return []string{owner, name, version}
}
