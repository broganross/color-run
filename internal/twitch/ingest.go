package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func IngestURL(ctx context.Context, client *http.Client, streamKey string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ingest.twitch.tv/ingests", nil)
	if err != nil {
		return "", fmt.Errorf("making http request: %w", err)
	}
	ingestResp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("getting ingests")
	} else if ingestResp.StatusCode < http.StatusOK || ingestResp.StatusCode > http.StatusIMUsed {
		defer ingestResp.Body.Close()
		b, err := io.ReadAll(ingestResp.Body)
		if err != nil {
			return "", fmt.Errorf("reading ingest response body: %w", err)
		}
		err = fmt.Errorf("getting ingest (%s): %s", http.StatusText(ingestResp.StatusCode), string(b))
		return "", err
	}
	defer ingestResp.Body.Close()
	r := ingestsResponse{}
	if err := json.NewDecoder(ingestResp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decoding ingest response: %w", err)
	}
	var ingestURL string
	for _, i := range r.Ingests {
		if i.Default {
			ingestURL = i.URLTemplate
		}
	}
	if ingestURL == "" {
		return "", fmt.Errorf("no default ingest server found")
	}
	ingestURL = strings.Replace(ingestURL, "{stream_key}", streamKey, -1)
	return ingestURL, nil
}
