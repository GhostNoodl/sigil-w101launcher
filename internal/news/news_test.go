package news

// Live integration test against KingsIsle's public GraphQL API.
// Only runs when W101_LIVE_TEST=1 is set.

import (
	"context"
	"os"
	"testing"
)

func TestLiveFetch(t *testing.T) {
	if os.Getenv("W101_LIVE_TEST") != "1" {
		t.Skip("set W101_LIVE_TEST=1 to run against the live news API")
	}

	items, err := Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no news items returned")
	}
	for _, item := range items {
		t.Logf("%s | %s", item.Date, item.Headline)
		if item.Headline == "" || item.URL == "" {
			t.Errorf("incomplete item: %+v", item)
		}
	}
}
