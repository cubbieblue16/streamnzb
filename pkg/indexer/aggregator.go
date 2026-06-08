package indexer

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"sort"
	"streamnzb/pkg/core/config"
	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/release"
	"strings"
	"sync"
)

type Aggregator struct {
	Indexers []Indexer
	// QuotaAware, when true, drops indexers whose daily API-hit (search) or
	// download quota is exhausted, preferring those with remaining headroom.
	// If every indexer is exhausted, all are tried anyway (graceful fallback).
	QuotaAware bool
}

// hasAPIHeadroom reports whether an indexer can still serve searches today.
// A non-positive limit means unknown/unlimited, which counts as headroom.
func hasAPIHeadroom(idx Indexer) bool {
	u := idx.GetUsage()
	if u.APIHitsLimit <= 0 {
		return true
	}
	return u.APIHitsRemaining > 0
}

// hasDownloadHeadroom reports whether an indexer can still serve grabs today.
func hasDownloadHeadroom(idx Indexer) bool {
	u := idx.GetUsage()
	if u.DownloadsLimit <= 0 {
		return true
	}
	return u.DownloadsRemaining > 0
}

// eligibleIndexers filters out indexers whose relevant quota (search vs.
// download) is exhausted, preserving the configured priority order among those
// with headroom. When quota awareness is off, only one indexer is configured,
// or every indexer is exhausted, the full list is returned unchanged so we
// never end up with zero indexers to try.
func eligibleIndexers(indexers []Indexer, quotaAware, forDownload bool) []Indexer {
	if !quotaAware || len(indexers) <= 1 {
		return indexers
	}
	hasHeadroom := hasAPIHeadroom
	if forDownload {
		hasHeadroom = hasDownloadHeadroom
	}
	eligible := make([]Indexer, 0, len(indexers))
	for _, idx := range indexers {
		if hasHeadroom(idx) {
			eligible = append(eligible, idx)
		}
	}
	if len(eligible) == 0 {
		return indexers
	}
	if len(eligible) < len(indexers) {
		skipped := make([]string, 0, len(indexers)-len(eligible))
		for _, idx := range indexers {
			if !hasHeadroom(idx) {
				skipped = append(skipped, idx.Name())
			}
		}
		logger.Debug("Quota-aware rotation skipped exhausted indexers",
			"skipped", strings.Join(skipped, ","),
			"for_download", forDownload,
			"eligible", len(eligible),
		)
	}
	return eligible
}

func (a *Aggregator) Name() string {
	return "Aggregator"
}

func (a *Aggregator) GetIndexers() []Indexer {
	return a.Indexers
}

func (a *Aggregator) GetUsage() Usage {
	var usage Usage
	var weightedResponseTotal float64
	for _, idx := range a.Indexers {
		u := idx.GetUsage()
		usage.APIHitsLimit += u.APIHitsLimit
		usage.APIHitsUsed += u.APIHitsUsed
		usage.APIHitsRemaining += u.APIHitsRemaining
		usage.DownloadsLimit += u.DownloadsLimit
		usage.DownloadsUsed += u.DownloadsUsed
		usage.DownloadsRemaining += u.DownloadsRemaining
		usage.AllTimeAPIHitsUsed += u.AllTimeAPIHitsUsed
		usage.AllTimeDownloadsUsed += u.AllTimeDownloadsUsed
		usage.SearchesCount += u.SearchesCount
		weightedResponseTotal += float64(u.SearchesCount) * u.AvgResponseMS
	}
	if usage.SearchesCount > 0 {
		usage.AvgResponseMS = weightedResponseTotal / float64(usage.SearchesCount)
	}
	return usage
}

func NewAggregator(indexers ...Indexer) *Aggregator {
	return &Aggregator{
		Indexers: indexers,
	}
}

func (a *Aggregator) Ping() error {
	var lastErr error
	successCount := 0

	for _, idx := range a.Indexers {
		if err := idx.Ping(); err != nil {
			lastErr = err
		} else {
			successCount++
		}
	}

	if successCount == 0 && len(a.Indexers) > 0 {
		return fmt.Errorf("all indexers failed ping, last error: %w", lastErr)
	}
	return nil
}

func (a *Aggregator) DownloadNZB(ctx context.Context, nzbURL string) ([]byte, error) {
	if len(a.Indexers) == 0 {
		return nil, fmt.Errorf("no indexers configured")
	}
	indexers := eligibleIndexers(a.Indexers, a.QuotaAware, true)
	var lastErr error
	for _, idx := range indexers {
		data, err := idx.DownloadNZB(ctx, nzbURL)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// isIDOnlySearch returns true when the request is an ID-based search
// (has IMDb/TVDB IDs and no text query).
func isIDOnlySearch(req SearchRequest) bool {
	return strings.EqualFold(strings.TrimSpace(req.SearchMode), "id")
}

// isTextSearch returns true when the request carries a text query.
func isTextSearch(req SearchRequest) bool {
	return strings.EqualFold(strings.TrimSpace(req.SearchMode), "text") && req.Query != ""
}

// shouldSkipIndexer checks the per-indexer DisableIdSearch / DisableStringSearch
// flags and returns true if this indexer should be skipped for the given request.
func shouldSkipIndexer(req SearchRequest, overrides *config.IndexerSearchConfig) bool {
	if overrides == nil {
		return false
	}
	if isIDOnlySearch(req) && overrides.DisableIdSearch != nil && *overrides.DisableIdSearch {
		return true
	}
	if isTextSearch(req) && overrides.DisableStringSearch != nil && *overrides.DisableStringSearch {
		return true
	}
	return false
}

func ShouldSkipIndexerForRequest(req SearchRequest, overrides *config.IndexerSearchConfig) bool {
	return shouldSkipIndexer(req, overrides)
}

func (a *Aggregator) Search(req SearchRequest) (*SearchResponse, error) {
	if strings.EqualFold(strings.TrimSpace(req.IndexerMode), "failover") {
		return a.searchWithFailover(req)
	}
	return a.searchCombined(req)
}

func (a *Aggregator) searchCombined(req SearchRequest) (*SearchResponse, error) {
	indexers := eligibleIndexers(a.Indexers, a.QuotaAware, false)
	resultsChan := make(chan []Item, len(indexers))
	var wg sync.WaitGroup

	for _, idx := range indexers {
		wg.Add(1)
		go func(indexer Indexer, r SearchRequest) {
			defer wg.Done()
			items, err := searchItemsForIndexer(indexer, r)
			if err != nil {
				logger.Warn("Indexer search failed", "indexer", indexer.Name(), "err", err)
				resultsChan <- []Item{}
				return
			}
			resultsChan <- items
		}(idx, req)
	}

	wg.Wait()
	close(resultsChan)

	var allItems []Item
	for items := range resultsChan {
		allItems = append(allItems, items...)
	}

	seenGUID := make(map[string]bool)
	seenLink := make(map[string]bool)
	seenTitleSize := make(map[string]bool)
	uniqueItems := []Item{}

	for _, item := range allItems {

		normalizedTitle := release.NormalizeTitleForDedup(item.Title)

		if item.GUID != "" {
			if seenGUID[item.GUID] {
				continue
			}
			seenGUID[item.GUID] = true
			uniqueItems = append(uniqueItems, item)
			continue
		}

		if item.Link != "" {

			normalizedLink := normalizeURL(item.Link)
			if seenLink[normalizedLink] {
				continue
			}
			seenLink[normalizedLink] = true
			uniqueItems = append(uniqueItems, item)
			continue
		}

		titleSizeKey := fmt.Sprintf("%s:%d", normalizedTitle, item.Size)
		if item.Size > 0 && seenTitleSize[titleSizeKey] {
			continue
		}
		if item.Size > 0 {
			seenTitleSize[titleSizeKey] = true
		}
		uniqueItems = append(uniqueItems, item)
	}

	sort.Slice(uniqueItems, func(i, j int) bool {
		return uniqueItems[i].Size > uniqueItems[j].Size
	})

	resp := &SearchResponse{
		XMLName: xml.Name{Local: "rss"},
		Channel: Channel{
			Items: uniqueItems,
		},
	}
	NormalizeSearchResponse(resp)
	return resp, nil
}

func (a *Aggregator) searchWithFailover(req SearchRequest) (*SearchResponse, error) {
	indexers := eligibleIndexers(a.Indexers, a.QuotaAware, false)
	type failoverResult struct {
		index int
		items []Item
		err   error
	}

	results := make(chan failoverResult, len(indexers))
	for i, idx := range indexers {
		go func(index int, idx Indexer) {
			items, err := searchItemsForIndexer(idx, req)
			results <- failoverResult{
				index: index,
				items: items,
				err:   err,
			}
		}(i, idx)
	}

	pending := len(indexers)
	done := make([]bool, len(indexers))
	itemsByIndexer := make([][]Item, len(indexers))

	for pending > 0 {
		result := <-results
		pending--
		done[result.index] = true
		if result.err != nil {
			logger.Warn("Indexer search failed", "indexer", indexers[result.index].Name(), "err", result.err)
		} else {
			itemsByIndexer[result.index] = result.items
		}

		for i := 0; i < len(indexers); i++ {
			if !done[i] {
				break
			}
			if len(itemsByIndexer[i]) == 0 {
				continue
			}
			resp := &SearchResponse{
				XMLName: xml.Name{Local: "rss"},
				Channel: Channel{
					Items: itemsByIndexer[i],
				},
			}
			NormalizeSearchResponse(resp)
			return resp, nil
		}
	}
	resp := &SearchResponse{
		XMLName: xml.Name{Local: "rss"},
		Channel: Channel{Items: []Item{}},
	}
	NormalizeSearchResponse(resp)
	return resp, nil
}

func searchItemsForIndexer(idx Indexer, req SearchRequest) ([]Item, error) {
	var indexerOverrides *config.IndexerSearchConfig
	if req.EffectiveByIndexer != nil {
		indexerOverrides = req.EffectiveByIndexer[idx.Name()]
	}

	reqCopy := req
	reqCopy.EffectiveByIndexer = nil
	reqCopy.OptionalOverrides = indexerOverrides
	if shouldSkipIndexer(reqCopy, indexerOverrides) {
		skipReason := "request mode disabled"
		if isIDOnlySearch(reqCopy) {
			skipReason = "id search disabled for this request"
		} else if isTextSearch(reqCopy) {
			skipReason = "text search disabled for this request"
		}
		logger.Debug("Indexer skipped for request",
			"stream", reqCopy.StreamLabel,
			"request", reqCopy.RequestLabel,
			"indexer", idx.Name(),
			"reason", skipReason,
			"is_id", isIDOnlySearch(reqCopy),
			"is_text", isTextSearch(reqCopy),
		)
		return []Item{}, nil
	}
	resp, err := idx.Search(reqCopy)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return []Item{}, nil
	}
	return resp.Channel.Items, nil
}

func normalizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(rawURL))
	}

	normalized := fmt.Sprintf("%s://%s%s", parsed.Scheme, parsed.Host, parsed.Path)
	return strings.ToLower(strings.TrimSpace(normalized))
}
