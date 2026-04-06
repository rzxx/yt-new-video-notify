package main

import "testing"

func TestCollectNewEntriesSinceIgnoresOlderReorderedHead(t *testing.T) {
	entries := []FeedEntry{
		{VideoID: "new", Title: "new", Published: "2026-04-05T01:26:13Z"},
		{VideoID: "old", Title: "old", Published: "2026-04-05T01:25:41Z"},
	}

	state := &ChannelState{
		LastVideoID:          "new",
		LastVideoPublishedAt: "2026-04-05T01:26:13Z",
		RecentVideoIDs:       []string{"new", "old"},
	}

	got := collectNewEntriesSince(entries, state)
	if len(got) != 0 {
		t.Fatalf("expected no new entries, got %+v", got)
	}
}

func TestCollectNewEntriesSinceDerivesWatermarkFromLegacyState(t *testing.T) {
	entries := []FeedEntry{
		{VideoID: "new", Title: "new", Published: "2026-04-05T01:26:13Z"},
		{VideoID: "old", Title: "old", Published: "2026-04-05T01:25:41Z"},
		{VideoID: "older", Title: "older", Published: "2026-04-05T01:20:00Z"},
	}

	state := &ChannelState{
		LastVideoID: "old",
	}

	got := collectNewEntriesSince(entries, state)
	if len(got) != 1 || got[0].VideoID != "new" {
		t.Fatalf("expected only the newer entry, got %+v", got)
	}
}

func TestSortFeedEntriesByPublishedDescending(t *testing.T) {
	entries := []FeedEntry{
		{VideoID: "old", Published: "2026-04-05T01:25:41Z"},
		{VideoID: "new", Published: "2026-04-05T01:26:13Z"},
	}

	got := sortFeedEntriesByPublished(entries)
	if len(got) != 2 || got[0].VideoID != "new" || got[1].VideoID != "old" {
		t.Fatalf("unexpected order: %+v", got)
	}
}
