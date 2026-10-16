// Package news fetches recent Wizard101 news from KingsIsle's public
// GraphQL API (the same backend the official website's news page uses).
package news

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// IndexURL is the public news page; individual stories have no stable
// deep links, so items open here.
const IndexURL = "https://www.wizard101.com/game/news"

const graphqlURL = "https://strapi.kingsisle.com/graphql"

// imageHost prefixes the relative upload paths the API returns.
const imageHost = "https://akamai.strapi.kingsisle.com"

const query = `{"query":"{ newsStories(pagination:{pageSize:25}, sort:\"updatedAt:desc\") { NewsPreview { Date Headline PreviewText PreviewImage { url } } } }"}`

type Item struct {
	Date     string `json:"date"`
	Headline string `json:"headline"`
	Text     string `json:"text"`
	Image    string `json:"image"`
	URL      string `json:"url"`
}

type graphResponse struct {
	Data struct {
		NewsStories []struct {
			NewsPreview []struct {
				Date         string
				Headline     string
				PreviewText  string
				PreviewImage *struct {
					URL string `json:"url"`
				}
			}
		} `json:"NewsStories"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Fetch returns recent news items, newest first.
func Fetch(ctx context.Context) ([]Item, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, strings.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// The API expects a browser-ish origin; without it Akamai blocks the POST.
	req.Header.Set("Origin", "https://www.wizard101.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) w101client")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting news API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("news API returned %s", resp.Status)
	}

	var gr graphResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, fmt.Errorf("decoding news response: %w", err)
	}
	if len(gr.Errors) > 0 {
		return nil, fmt.Errorf("news API error: %s", gr.Errors[0].Message)
	}

	var items []Item
	// The CMS sometimes publishes the same story twice; show it once.
	seen := make(map[string]bool)
	for _, story := range gr.Data.NewsStories {
		for _, preview := range story.NewsPreview {
			if preview.Headline == "" {
				continue
			}
			key := preview.Date + "|" + strings.TrimSpace(preview.Headline)
			if seen[key] {
				continue
			}
			seen[key] = true
			item := Item{
				Date:     preview.Date,
				Headline: strings.TrimSpace(preview.Headline),
				Text:     strings.TrimSpace(preview.PreviewText),
				URL:      IndexURL,
			}
			if preview.PreviewImage != nil && preview.PreviewImage.URL != "" {
				item.Image = imageHost + preview.PreviewImage.URL
			}
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no news items in response")
	}

	// Stories are ordered by publish date, but the preview entries inside a
	// story carry their own dates and can be much older — sort the flat list
	// by each item's date.
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Date > items[j].Date
	})

	return items, nil
}
