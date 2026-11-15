package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// releaseRepository publishes AI Router releases. The module path is
// github.com/zbss/airoute because this checkout is a fork that keeps its own
// module path, so the two intentionally differ: point the update check at the
// fork that actually ships binaries with AIROUTE_RELEASE_REPO (or
// -X github.com/zbss/airoute/internal/admin.releaseRepository=owner/repo).
var releaseRepository = "soooooollee/ai-router"

func releaseRepo() string {
	if configured := strings.TrimSpace(os.Getenv("AIROUTE_RELEASE_REPO")); configured != "" {
		return strings.Trim(configured, "/")
	}
	return releaseRepository
}

func releaseAPIURL() string {
	return "https://api.github.com/repos/" + releaseRepo() + "/releases/latest"
}

func releasesPage() string {
	return "https://github.com/" + releaseRepo() + "/releases"
}

type UpdateInfo struct {
	Checked          bool      `json:"checked"`
	CurrentVersion   string    `json:"current_version"`
	LatestVersion    string    `json:"latest_version,omitempty"`
	UpdateAvailable  bool      `json:"update_available"`
	ReleaseURL       string    `json:"release_url"`
	CheckedAt        time.Time `json:"checked_at"`
	CheckUnavailable bool      `json:"check_unavailable,omitempty"`
}

func (s *Server) checkUpdate(parent context.Context) UpdateInfo {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	now := time.Now()
	if !s.updateCachedUntil.IsZero() && now.Before(s.updateCachedUntil) {
		return s.updateCached
	}
	current := normalizeVersion(s.Version)
	info := UpdateInfo{
		CurrentVersion:  current,
		ReleaseURL:      releasesPage(),
		CheckedAt:       now,
		UpdateAvailable: false,
	}
	if _, _, ok := comparableVersion(current); !ok {
		s.updateCached = info
		s.updateCachedUntil = now.Add(15 * time.Minute)
		return info
	}

	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	releaseURL := s.ReleaseURL
	if releaseURL == "" {
		releaseURL = releaseAPIURL()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err == nil {
		request.Header.Set("accept", "application/vnd.github+json")
		request.Header.Set("user-agent", "AI-Router/"+current)
		client := s.Client
		if client == nil {
			client = http.DefaultClient
		}
		var response *http.Response
		response, err = client.Do(request)
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = fmt.Errorf("release API returned HTTP %d", response.StatusCode)
			} else {
				var release struct {
					TagName string `json:"tag_name"`
				}
				err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release)
				if err == nil {
					latest := normalizeVersion(release.TagName)
					if _, _, valid := comparableVersion(latest); !valid {
						err = fmt.Errorf("release API returned an invalid version")
					} else {
						info.Checked = true
						info.LatestVersion = latest
						info.UpdateAvailable = newerVersion(latest, current)
						info.ReleaseURL = releasesPage() + "/tag/" + url.PathEscape(release.TagName)
					}
				}
			}
		}
	}
	if err != nil {
		info.CheckUnavailable = true
		s.updateCachedUntil = now.Add(15 * time.Minute)
	} else {
		s.updateCachedUntil = now.Add(6 * time.Hour)
	}
	s.updateCached = info
	return info
}

func normalizeVersion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}

func newerVersion(candidate, current string) bool {
	candidateParts, candidatePrerelease, candidateOK := comparableVersion(candidate)
	currentParts, currentPrerelease, currentOK := comparableVersion(current)
	if !candidateOK || !currentOK {
		return false
	}
	length := max(len(candidateParts), len(currentParts))
	for index := 0; index < length; index++ {
		candidatePart, currentPart := 0, 0
		if index < len(candidateParts) {
			candidatePart = candidateParts[index]
		}
		if index < len(currentParts) {
			currentPart = currentParts[index]
		}
		if candidatePart != currentPart {
			return candidatePart > currentPart
		}
	}
	if candidatePrerelease == currentPrerelease {
		return false
	}
	return candidatePrerelease == "" && currentPrerelease != ""
}

func comparableVersion(value string) ([]int, string, bool) {
	value = normalizeVersion(value)
	value = strings.SplitN(value, "+", 2)[0]
	core, prerelease, _ := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return nil, "", false
	}
	numbers := make([]int, len(parts))
	for index, part := range parts {
		if part == "" {
			return nil, "", false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, "", false
		}
		numbers[index] = number
	}
	return numbers, prerelease, true
}
