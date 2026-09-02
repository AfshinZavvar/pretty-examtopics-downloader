package fetch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"examtopics-downloader/internal/constants"
)

type Progress struct {
	Phase     string
	Completed int
	Total     int
	Found     int
	Retries   int
	Failures  int
	CacheHits int
	FromCache bool
	Complete  bool
	Rate      float64
	Elapsed   time.Duration
}

type ProgressFunc func(Progress)

type IndexOptions struct {
	ForceRefresh bool
	Timeout      time.Duration
	Workers      int
}

type ProviderIndex struct {
	Provider       string   `json:"provider,omitempty"`
	Links          []string `json:"links"`
	ExamSlugs      []string `json:"exam_slugs"`
	TotalPages     int      `json:"total_pages"`
	CompletedPages []int    `json:"completed_pages"`
	Complete       bool     `json:"complete"`
	UpdatedAtUnix  int64    `json:"updated_at_unix"`
	FromCache      bool     `json:"-"`
}

type indexPageResult struct {
	page    int
	links   []string
	retries int
	err     error
}

func reportProgress(report ProgressFunc, progress Progress) {
	if report != nil {
		report(progress)
	}
}

// GetProviderDiscussionIndex returns a reusable provider-wide discussion-link
// index. Complete cache entries return immediately; partial entries resume only
// pages that were not checkpointed successfully.
func GetProviderDiscussionIndex(ctx context.Context, provider string, options IndexOptions, report ProgressFunc) (ProviderIndex, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ProviderIndex{}, fmt.Errorf("provider is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Timeout <= 0 {
		options.Timeout = constants.DiscoveryTimeout
	}
	if options.Workers <= 0 {
		options.Workers = constants.DiscoveryWorkers
	}

	started := time.Now()
	index := ProviderIndex{Provider: provider}
	if !options.ForceRefresh {
		if cached, ok := getCachedProviderIndex(provider); ok {
			index = cached
			if cached.Complete {
				reportProgress(report, Progress{
					Phase: "discussion-index", Completed: cached.TotalPages, Total: cached.TotalPages,
					Found: len(cached.ExamSlugs), CacheHits: len(cached.Links), FromCache: true,
					Complete: true, Rate: requestLimiter.RPS(), Elapsed: time.Since(started),
				})
				return cached, nil
			}
		}
	}
	index.Provider = provider
	index.FromCache = false

	scanCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	if index.TotalPages <= 0 || options.ForceRefresh {
		pages, _, err := getMaxNumPagesContext(scanCtx, fmt.Sprintf("%s/discussions/%s/", examTopicsBaseURL, provider))
		if err != nil {
			return index, err
		}
		index.TotalPages = pages
		if options.ForceRefresh {
			index.Links = nil
			index.ExamSlugs = nil
			index.CompletedPages = nil
			index.Complete = false
		}
	}

	completedSet := make(map[int]struct{}, len(index.CompletedPages))
	for _, page := range index.CompletedPages {
		if page >= 1 && page <= index.TotalPages {
			completedSet[page] = struct{}{}
		}
	}
	linkSet := make(map[string]struct{}, len(index.Links))
	for _, link := range index.Links {
		linkSet[link] = struct{}{}
	}
	slugSet := make(map[string]struct{}, len(index.ExamSlugs))
	for _, slug := range index.ExamSlugs {
		slugSet[slug] = struct{}{}
	}
	// Build the immutable work list before workers start. The previous
	// dispatcher read completedSet while the result collector wrote to it,
	// which could terminate the executable with an unrecoverable
	// "concurrent map read and map write" runtime fatal error.
	missingPages := make([]int, 0, index.TotalPages-len(completedSet))
	for page := 1; page <= index.TotalPages; page++ {
		if _, done := completedSet[page]; !done {
			missingPages = append(missingPages, page)
		}
	}

	jobs := make(chan int)
	results := make(chan indexPageResult, options.Workers)
	var workers sync.WaitGroup
	for worker := 0; worker < options.Workers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for page := range jobs {
				links, metrics, err := getDiscussionLinksFromPageContext(scanCtx, listingPageURL(provider, page))
				select {
				case results <- indexPageResult{page: page, links: links, retries: metrics.Retries, err: err}:
				case <-scanCtx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, page := range missingPages {
			select {
			case jobs <- page:
			case <-scanCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	retries := 0
	failures := 0
	checkpointed := len(completedSet)
	for result := range results {
		retries += result.retries
		if result.err != nil {
			failures++
		} else {
			completedSet[result.page] = struct{}{}
			for _, link := range result.links {
				linkSet[link] = struct{}{}
				if slug := extractExamSlugFromDiscussionURL(link); slug != "" {
					slugSet[slug] = struct{}{}
				}
			}
		}

		index = buildProviderIndexSnapshot(provider, index.TotalPages, completedSet, linkSet, slugSet)
		if len(completedSet)-checkpointed >= constants.DiscoveryCheckpointPages {
			setCachedProviderIndex(provider, index)
			checkpointed = len(completedSet)
		}
		reportProgress(report, Progress{
			Phase: "discussion-index", Completed: len(completedSet), Total: index.TotalPages,
			Found: len(slugSet), Retries: retries, Failures: failures,
			Rate: requestLimiter.RPS(), Elapsed: time.Since(started),
		})
	}

	index = buildProviderIndexSnapshot(provider, index.TotalPages, completedSet, linkSet, slugSet)
	index.Complete = len(completedSet) == index.TotalPages
	setCachedProviderIndex(provider, index)
	reportProgress(report, Progress{
		Phase: "discussion-index", Completed: len(completedSet), Total: index.TotalPages,
		Found: len(index.ExamSlugs), Retries: retries, Failures: failures,
		Complete: index.Complete, Rate: requestLimiter.RPS(), Elapsed: time.Since(started),
	})

	if err := scanCtx.Err(); err != nil {
		return index, err
	}
	if !index.Complete {
		return index, fmt.Errorf("provider index incomplete: %d of %d pages", len(completedSet), index.TotalPages)
	}
	return index, nil
}

func buildProviderIndexSnapshot(provider string, total int, completed map[int]struct{}, links, slugs map[string]struct{}) ProviderIndex {
	index := ProviderIndex{Provider: provider, TotalPages: total}
	for page := range completed {
		index.CompletedPages = append(index.CompletedPages, page)
	}
	for link := range links {
		index.Links = append(index.Links, link)
	}
	for slug := range slugs {
		index.ExamSlugs = append(index.ExamSlugs, slug)
	}
	sort.Ints(index.CompletedPages)
	sort.Strings(index.Links)
	sort.Strings(index.ExamSlugs)
	index.Complete = len(index.CompletedPages) == total && total > 0
	return index
}

func getDiscussionLinksFromPageContext(ctx context.Context, pageURL string) ([]string, FetchMetrics, error) {
	doc, metrics, err := ParseHTMLContext(ctx, pageURL, *client, MetadataRequestPolicy)
	if err != nil {
		return nil, metrics, err
	}
	return extractDiscussionLinksFromDoc(doc), metrics, nil
}
