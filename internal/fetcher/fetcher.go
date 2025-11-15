package fetcher

// Fetcher will implement HTTP Range requests with bounded readahead,
// LRU seek cache, and retry logic in M2.
// This is a placeholder stub for M1.

type Fetcher struct {
	url string
}

func New(url string) *Fetcher {
	return &Fetcher{url: url}
}
