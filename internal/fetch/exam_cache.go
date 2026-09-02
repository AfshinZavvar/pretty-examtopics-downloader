package fetch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	providerIndexCacheTTL     = 24 * time.Hour
	providerIndexCacheVersion = 2
)

type providerIndexCacheFile struct {
	Version   int                      `json:"version"`
	Providers map[string]ProviderIndex `json:"providers"`
}

type legacyDiscussionExamCacheEntry struct {
	ExamSlugs []string `json:"exam_slugs"`
	UpdatedAt int64    `json:"updated_at_unix"`
}

type legacyDiscussionExamCacheFile struct {
	Providers map[string]legacyDiscussionExamCacheEntry `json:"providers"`
}

var (
	providerIndexCacheEnabled      = true
	providerIndexCachePathOverride string
	legacyIndexCachePathOverride   string
	providerIndexCacheMu           sync.Mutex
	providerIndexCacheLoaded       bool
	providerIndexCache             = providerIndexCacheFile{
		Version:   providerIndexCacheVersion,
		Providers: map[string]ProviderIndex{},
	}
)

func getCachedProviderIndex(provider string) (ProviderIndex, bool) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" || !providerIndexCacheEnabled {
		return ProviderIndex{}, false
	}

	providerIndexCacheMu.Lock()
	defer providerIndexCacheMu.Unlock()
	ensureProviderIndexCacheLoadedLocked()

	entry, ok := providerIndexCache.Providers[provider]
	if !ok {
		return ProviderIndex{}, false
	}
	if entry.UpdatedAtUnix <= 0 || time.Since(time.Unix(entry.UpdatedAtUnix, 0)) > providerIndexCacheTTL {
		delete(providerIndexCache.Providers, provider)
		saveProviderIndexCacheLocked()
		return ProviderIndex{}, false
	}

	entry = cloneProviderIndex(entry)
	entry.Provider = provider
	entry.FromCache = true
	return entry, true
}

func setCachedProviderIndex(provider string, index ProviderIndex) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" || !providerIndexCacheEnabled {
		return
	}
	index.Provider = provider
	index.FromCache = false
	index.UpdatedAtUnix = time.Now().Unix()
	normalizeProviderIndex(&index)

	providerIndexCacheMu.Lock()
	defer providerIndexCacheMu.Unlock()
	ensureProviderIndexCacheLoadedLocked()
	providerIndexCache.Providers[provider] = cloneProviderIndex(index)
	saveProviderIndexCacheLocked()
}

// GetCachedProviderExamSlugs returns menu-ready cached data without starting
// network discovery. Empty completed scans are valid cache hits.
func GetCachedProviderExamSlugs(provider string) ([]string, bool) {
	index, ok := getCachedProviderIndex(provider)
	if !ok {
		return nil, false
	}
	return append([]string(nil), index.ExamSlugs...), true
}

// InvalidateProviderDiscussionIndex forces the next scan/extraction to rebuild
// the provider listing index while leaving cached question pages untouched.
func InvalidateProviderDiscussionIndex(provider string) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" {
		return
	}
	providerIndexCacheMu.Lock()
	defer providerIndexCacheMu.Unlock()
	ensureProviderIndexCacheLoadedLocked()
	delete(providerIndexCache.Providers, provider)
	saveProviderIndexCacheLocked()
}

func ensureProviderIndexCacheLoadedLocked() {
	if providerIndexCacheLoaded {
		return
	}
	providerIndexCacheLoaded = true

	payload, err := os.ReadFile(providerIndexCachePath())
	if err == nil {
		var loaded providerIndexCacheFile
		if json.Unmarshal(payload, &loaded) == nil {
			if loaded.Providers == nil {
				loaded.Providers = map[string]ProviderIndex{}
			}
			loaded.Version = providerIndexCacheVersion
			providerIndexCache = loaded
			return
		}
		debugf("failed to parse provider index cache %q", providerIndexCachePath())
	}

	// One-time, non-destructive import from the old slug-only cache. Migrated
	// entries are intentionally partial because they contain no reusable links.
	legacyPayload, legacyErr := os.ReadFile(legacyDiscussionExamCachePath())
	if legacyErr != nil {
		return
	}
	var legacy legacyDiscussionExamCacheFile
	if err := json.Unmarshal(legacyPayload, &legacy); err != nil {
		debugf("failed to parse legacy exam cache %q: %v", legacyDiscussionExamCachePath(), err)
		return
	}
	now := time.Now()
	for provider, entry := range legacy.Providers {
		if entry.UpdatedAt <= 0 || now.Sub(time.Unix(entry.UpdatedAt, 0)) > providerIndexCacheTTL {
			continue
		}
		index := ProviderIndex{
			Provider:      strings.ToLower(strings.TrimSpace(provider)),
			ExamSlugs:     append([]string(nil), entry.ExamSlugs...),
			Complete:      false,
			UpdatedAtUnix: entry.UpdatedAt,
		}
		normalizeProviderIndex(&index)
		providerIndexCache.Providers[index.Provider] = index
	}
	if len(providerIndexCache.Providers) > 0 {
		saveProviderIndexCacheLocked()
	}
}

func saveProviderIndexCacheLocked() {
	if !providerIndexCacheEnabled {
		return
	}
	path := providerIndexCachePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		debugf("failed to create provider index cache dir %q: %v", dir, err)
		return
	}
	payload, err := json.MarshalIndent(providerIndexCache, "", "  ")
	if err != nil {
		debugf("failed to marshal provider index cache: %v", err)
		return
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		debugf("failed to write provider index cache %q: %v", path, err)
	}
}

func cloneProviderIndex(index ProviderIndex) ProviderIndex {
	index.Links = append([]string(nil), index.Links...)
	index.ExamSlugs = append([]string(nil), index.ExamSlugs...)
	index.CompletedPages = append([]int(nil), index.CompletedPages...)
	return index
}

func normalizeProviderIndex(index *ProviderIndex) {
	index.Provider = strings.ToLower(strings.TrimSpace(index.Provider))
	index.Links = dedupeSortedStrings(index.Links)
	index.ExamSlugs = dedupeSortedStrings(index.ExamSlugs)
	sort.Ints(index.CompletedPages)
	if len(index.CompletedPages) > 1 {
		out := index.CompletedPages[:1]
		for _, page := range index.CompletedPages[1:] {
			if page != out[len(out)-1] {
				out = append(out, page)
			}
		}
		index.CompletedPages = out
	}
}

func dedupeSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func providerIndexCachePath() string {
	if providerIndexCachePathOverride != "" {
		return providerIndexCachePathOverride
	}
	base, err := os.UserCacheDir()
	if err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "examtopics-downloader", "provider_discussion_index_v2.json")
	}
	return filepath.Join(".", ".examtopics_provider_index_v2.json")
}

func legacyDiscussionExamCachePath() string {
	if legacyIndexCachePathOverride != "" {
		return legacyIndexCachePathOverride
	}
	base, err := os.UserCacheDir()
	if err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "examtopics-downloader", "discussion_exam_slugs.json")
	}
	return filepath.Join(".", ".examtopics_discussion_exam_cache.json")
}

// Compatibility helpers retained for package callers and older tests.
func getCachedDiscussionExamSlugs(provider string) ([]string, bool) {
	return GetCachedProviderExamSlugs(provider)
}

func setCachedDiscussionExamSlugs(provider string, slugs []string) {
	setCachedProviderIndex(provider, ProviderIndex{Provider: provider, ExamSlugs: slugs})
}
