package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

const (
	defaultConfigPath       = "config.json"
	defaultStatePath        = "state.json"
	defaultPollSeconds      = 30
	defaultHeartbeatSeconds = 300
	schedulerTickInterval   = time.Second
)

var (
	channelIDPathPattern = regexp.MustCompile(`/channel/(UC[\w-]{22})`)
	anyChannelIDPattern  = regexp.MustCompile(`UC[\w-]{22}`)
	channelIDPatterns    = []*regexp.Regexp{
		regexp.MustCompile(`"browseId":"(UC[\w-]{22})"`),
		regexp.MustCompile(`"externalId":"(UC[\w-]{22})"`),
		regexp.MustCompile(`"channelId":"(UC[\w-]{22})"`),
		regexp.MustCompile(`browseId\\":\\"(UC[\w-]{22})`),
		regexp.MustCompile(`externalId\\":\\"(UC[\w-]{22})`),
		regexp.MustCompile(`https://www\.youtube\.com/channel/(UC[\w-]{22})`),
		regexp.MustCompile(`/channel/(UC[\w-]{22})`),
	}
)

type Config struct {
	Channels          []ChannelConfig `json:"channels"`
	HeartbeatSeconds  int             `json:"heartbeat_interval_seconds"`
	LegacyChannelURL  string          `json:"channel_url"`
	LegacyPollSeconds int             `json:"poll_interval_seconds"`
	LegacySkipShorts  bool            `json:"skip_shorts"`
}

type ChannelConfig struct {
	ChannelURL          string `json:"channel_url"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	SkipShorts          bool   `json:"skip_shorts"`
}

type State struct {
	Channels []ChannelState `json:"channels"`
}

type ChannelState struct {
	ChannelID     string `json:"channel_id"`
	ChannelURL    string `json:"channel_url"`
	LastVideoID   string `json:"last_video_id"`
	LastVideoURL  string `json:"last_video_url"`
	LastVideoName string `json:"last_video_name"`
	LastCheckedAt string `json:"last_checked_at"`
}

type channelTracker struct {
	Config    ChannelConfig
	Interval  time.Duration
	NextCheck time.Time
}

type Feed struct {
	Title   string      `xml:"title"`
	Entries []FeedEntry `xml:"entry"`
}

type FeedEntry struct {
	ID        string     `xml:"id"`
	VideoID   string     `xml:"http://www.youtube.com/xml/schemas/2015 videoId"`
	ChannelID string     `xml:"http://www.youtube.com/xml/schemas/2015 channelId"`
	Title     string     `xml:"title"`
	Links     []AtomLink `xml:"link"`
	Published string     `xml:"published"`
}

type AtomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

func main() {
	log.SetFlags(log.LstdFlags)

	configPath := flag.String("config", defaultConfigPath, "Path to the JSON config file")
	statePath := flag.String("state", defaultStatePath, "Path to the state file")
	runOnce := flag.Bool("once", false, "Check once and exit")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	state, err := loadState(*statePath)
	if err != nil {
		log.Fatalf("load state: %v", err)
	}

	trackers := make([]channelTracker, 0, len(cfg.Channels))
	log.Printf("starting yt-new-video-notify")
	log.Printf("yt-new-video-notify is running")
	log.Printf("tracking %d channel(s)", len(cfg.Channels))
	for _, channelCfg := range cfg.Channels {
		interval := time.Duration(channelCfg.PollIntervalSeconds) * time.Second
		log.Printf("watching %s every %s", channelCfg.ChannelURL, interval)
		trackers = append(trackers, channelTracker{
			Config:    channelCfg,
			Interval:  interval,
			NextCheck: time.Now(),
		})
	}

	heartbeatInterval := time.Duration(cfg.HeartbeatSeconds) * time.Second
	log.Printf("heartbeat every %s", heartbeatInterval)
	log.Printf("initial checks started")

	for i := range trackers {
		if err := runCheck(trackers[i].Config, state, *statePath); err != nil {
			log.Printf("check failed for %s: %v", trackers[i].Config.ChannelURL, err)
		}
		trackers[i].NextCheck = time.Now().Add(trackers[i].Interval)
	}

	if *runOnce {
		return
	}

	pollTicker := time.NewTicker(schedulerTickInterval)
	defer pollTicker.Stop()

	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case now := <-pollTicker.C:
			for i := range trackers {
				if trackers[i].NextCheck.After(now) {
					continue
				}

				if err := runCheck(trackers[i].Config, state, *statePath); err != nil {
					log.Printf("check failed for %s: %v", trackers[i].Config.ChannelURL, err)
				}
				trackers[i].NextCheck = time.Now().Add(trackers[i].Interval)
			}
		case <-heartbeatTicker.C:
			logHeartbeat(state, cfg)
		}
	}
}

func logHeartbeat(state *State, cfg Config) {
	log.Printf("heartbeat: tracking %d channel(s)", len(cfg.Channels))
	for _, channelCfg := range cfg.Channels {
		channelState := state.findChannelState(channelCfg.ChannelURL)

		lastSeen := "not set yet"
		if channelState != nil && channelState.LastVideoName != "" {
			lastSeen = channelState.LastVideoName
		}

		lastChecked := "not checked yet"
		if channelState != nil && channelState.LastCheckedAt != "" {
			lastChecked = channelState.LastCheckedAt
		}

		log.Printf(
			"heartbeat: watching %s | last seen: %s | last check: %s",
			channelCfg.ChannelURL,
			lastSeen,
			lastChecked,
		)
	}
}

func loadConfig(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	if len(cfg.Channels) == 0 && strings.TrimSpace(cfg.LegacyChannelURL) != "" {
		cfg.Channels = []ChannelConfig{
			{
				ChannelURL:          cfg.LegacyChannelURL,
				PollIntervalSeconds: cfg.LegacyPollSeconds,
				SkipShorts:          cfg.LegacySkipShorts,
			},
		}
	}

	if len(cfg.Channels) == 0 {
		return cfg, errors.New("channels must contain at least one channel")
	}

	seen := make(map[string]struct{}, len(cfg.Channels))
	for i := range cfg.Channels {
		cfg.Channels[i].ChannelURL = strings.TrimSpace(cfg.Channels[i].ChannelURL)
		if cfg.Channels[i].ChannelURL == "" {
			return cfg, fmt.Errorf("channels[%d].channel_url is required", i)
		}
		if cfg.Channels[i].PollIntervalSeconds <= 0 {
			cfg.Channels[i].PollIntervalSeconds = defaultPollSeconds
		}

		normalized := normalizeURL(cfg.Channels[i].ChannelURL)
		if _, exists := seen[normalized]; exists {
			return cfg, fmt.Errorf("duplicate channel_url in config: %s", cfg.Channels[i].ChannelURL)
		}
		seen[normalized] = struct{}{}
	}

	if cfg.HeartbeatSeconds <= 0 {
		cfg.HeartbeatSeconds = defaultHeartbeatSeconds
	}

	return cfg, nil
}

func loadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err == nil && len(state.Channels) > 0 {
		return &state, nil
	}

	var legacy ChannelState
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	if legacy.ChannelURL == "" && legacy.ChannelID == "" && legacy.LastVideoID == "" {
		return &State{}, nil
	}

	return &State{Channels: []ChannelState{legacy}}, nil
}

func saveState(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (s *State) ensureChannelState(channelURL string) *ChannelState {
	if s == nil {
		return nil
	}

	for i := range s.Channels {
		if sameChannelURL(s.Channels[i].ChannelURL, channelURL) {
			return &s.Channels[i]
		}
	}

	s.Channels = append(s.Channels, ChannelState{ChannelURL: channelURL})
	return &s.Channels[len(s.Channels)-1]
}

func (s *State) findChannelState(channelURL string) *ChannelState {
	if s == nil {
		return nil
	}

	for i := range s.Channels {
		if sameChannelURL(s.Channels[i].ChannelURL, channelURL) {
			return &s.Channels[i]
		}
	}

	return nil
}

func runCheck(cfg ChannelConfig, state *State, statePath string) error {
	channelState := state.ensureChannelState(cfg.ChannelURL)
	if channelState == nil {
		return errors.New("could not create channel state")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	channelID, err := effectiveChannelID(ctx, cfg, channelState)
	if err != nil {
		return err
	}

	feed, err := fetchFeed(ctx, channelID)
	if err != nil {
		return fmt.Errorf("fetch feed: %w", err)
	}
	if len(feed.Entries) == 0 {
		return errors.New("feed is empty")
	}

	if channelState.ChannelID != "" && channelState.ChannelID != channelID {
		log.Printf("channel changed from %s to %s for %s, resetting baseline", channelState.ChannelID, channelID, cfg.ChannelURL)
		*channelState = ChannelState{ChannelURL: cfg.ChannelURL}
	}

	relevantEntries := filterFeedEntries(feed.Entries, cfg.SkipShorts)
	if len(relevantEntries) == 0 {
		channelState.ChannelID = channelID
		channelState.ChannelURL = cfg.ChannelURL
		channelState.LastCheckedAt = time.Now().Format(time.RFC3339)
		if err := saveState(statePath, state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}

		if cfg.SkipShorts {
			log.Printf("no non-short uploads found in feed for %s; skipping notification check", cfg.ChannelURL)
		}
		return nil
	}

	latest := relevantEntries[0]
	if latest.VideoID == "" {
		return errors.New("latest feed entry is missing a video ID")
	}

	if channelState.LastVideoID == "" {
		updateStateFromEntry(channelState, cfg.ChannelURL, channelID, latest)
		channelState.LastCheckedAt = time.Now().Format(time.RFC3339)
		if err := saveState(statePath, state); err != nil {
			return fmt.Errorf("save initial state: %w", err)
		}

		log.Printf("baseline set for %s to current latest video: %s", cfg.ChannelURL, latest.Title)
		return nil
	}

	if cfg.SkipShorts && stateTracksShort(channelState, feed.Entries) {
		updateStateFromEntry(channelState, cfg.ChannelURL, channelID, latest)
		channelState.LastCheckedAt = time.Now().Format(time.RFC3339)
		if err := saveState(statePath, state); err != nil {
			return fmt.Errorf("save state after shorts baseline update: %w", err)
		}

		log.Printf("baseline moved for %s from stored short to latest non-short upload: %s", cfg.ChannelURL, latest.Title)
		return nil
	}

	if latest.VideoID == channelState.LastVideoID {
		channelState.ChannelID = channelID
		channelState.ChannelURL = cfg.ChannelURL
		channelState.LastCheckedAt = time.Now().Format(time.RFC3339)
		return saveState(statePath, state)
	}

	newEntries := collectNewEntries(relevantEntries, channelState.LastVideoID)
	if len(newEntries) == 0 {
		newEntries = []FeedEntry{latest}
	}

	title, message := buildNotification(feed.Title, newEntries)
	log.Printf("new upload detected for %s: %s", cfg.ChannelURL, message)

	if err := showWindowsNotification(title, message, cfg.ChannelURL); err != nil {
		log.Printf("notification failed for %s: %v", cfg.ChannelURL, err)
	}

	if err := openBrowser(cfg.ChannelURL); err != nil {
		log.Printf("browser launch failed for %s: %v", cfg.ChannelURL, err)
	}

	updateStateFromEntry(channelState, cfg.ChannelURL, channelID, latest)
	channelState.LastCheckedAt = time.Now().Format(time.RFC3339)
	if err := saveState(statePath, state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	return nil
}

func effectiveChannelID(ctx context.Context, cfg ChannelConfig, state *ChannelState) (string, error) {
	if sameChannelURL(state.ChannelURL, cfg.ChannelURL) && state.ChannelID != "" {
		return state.ChannelID, nil
	}

	channelID, err := resolveChannelID(ctx, cfg.ChannelURL)
	if err != nil {
		if state.ChannelID != "" && sameChannelURL(state.ChannelURL, cfg.ChannelURL) {
			log.Printf("channel ID resolve failed, using cached channel ID %s for %s", state.ChannelID, cfg.ChannelURL)
			return state.ChannelID, nil
		}
		return "", fmt.Errorf("resolve channel ID: %w", err)
	}

	return channelID, nil
}

func sameChannelURL(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}

	return normalizeURL(left) == normalizeURL(right)
}

func normalizeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if parsed.Path != "/" {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}

	return parsed.String()
}

func updateStateFromEntry(state *ChannelState, channelURL, channelID string, entry FeedEntry) {
	state.ChannelID = channelID
	state.ChannelURL = channelURL
	state.LastVideoID = entry.VideoID
	state.LastVideoURL = entry.URL()
	state.LastVideoName = entry.Title
}

func collectNewEntries(entries []FeedEntry, lastVideoID string) []FeedEntry {
	var pending []FeedEntry
	for _, entry := range entries {
		if entry.VideoID == "" {
			continue
		}
		if entry.VideoID == lastVideoID {
			break
		}
		pending = append(pending, entry)
	}

	return pending
}

func filterFeedEntries(entries []FeedEntry, skipShorts bool) []FeedEntry {
	if !skipShorts {
		return entries
	}

	filtered := make([]FeedEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsShort() {
			continue
		}
		filtered = append(filtered, entry)
	}

	return filtered
}

func stateTracksShort(state *ChannelState, entries []FeedEntry) bool {
	if state == nil || state.LastVideoID == "" {
		return false
	}

	if isShortURL(state.LastVideoURL) {
		return true
	}

	for _, entry := range entries {
		if entry.VideoID == state.LastVideoID {
			return entry.IsShort()
		}
	}

	return false
}

func buildNotification(channelName string, entries []FeedEntry) (string, string) {
	if channelName == "" {
		channelName = "YouTube channel"
	}

	if len(entries) == 1 {
		return fmt.Sprintf("New upload on %s", channelName), entries[0].Title
	}

	latest := entries[0].Title
	return fmt.Sprintf("%d new uploads on %s", len(entries), channelName), fmt.Sprintf("Latest: %s", latest)
}

func resolveChannelID(ctx context.Context, channelURL string) (string, error) {
	if matches := channelIDPathPattern.FindStringSubmatch(channelURL); len(matches) == 2 {
		return matches[1], nil
	}

	body, err := fetchText(ctx, channelURL)
	if err != nil {
		return "", err
	}

	for _, pattern := range channelIDPatterns {
		if matches := pattern.FindStringSubmatch(body); len(matches) == 2 {
			return matches[1], nil
		}
	}

	if channelID := findDominantChannelID(body); channelID != "" {
		return channelID, nil
	}

	return "", errors.New("could not find channel ID in page HTML")
}

func findDominantChannelID(body string) string {
	matches := anyChannelIDPattern.FindAllString(body, -1)
	if len(matches) == 0 {
		return ""
	}

	counts := make(map[string]int, len(matches))
	bestID := ""
	bestCount := 0
	for _, match := range matches {
		counts[match]++
		if counts[match] > bestCount {
			bestID = match
			bestCount = counts[match]
		}
	}

	return bestID
}

func fetchFeed(ctx context.Context, channelID string) (*Feed, error) {
	url := fmt.Sprintf("https://www.youtube.com/feeds/videos.xml?channel_id=%s", channelID)
	body, err := fetchBytes(ctx, url)
	if err != nil {
		return nil, err
	}

	var feed Feed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, err
	}

	return &feed, nil
}

func fetchText(ctx context.Context, targetURL string) (string, error) {
	body, err := fetchBytes(ctx, targetURL)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func fetchBytes(ctx context.Context, targetURL string) ([]byte, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("PowerShell-based fetch is only implemented on Windows")
	}

	return fetchBytesWithPowerShell(ctx, targetURL)
}

func fetchBytesWithPowerShell(ctx context.Context, targetURL string) ([]byte, error) {
	escapedURL := strings.ReplaceAll(targetURL, "'", "''")
	script := fmt.Sprintf(
		"$ProgressPreference='SilentlyContinue'; "+
			"[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; "+
			"[System.Net.WebRequest]::DefaultWebProxy=[System.Net.WebRequest]::GetSystemWebProxy(); "+
			"(Invoke-WebRequest -UseBasicParsing -TimeoutSec 20 -Uri '%s').Content",
		escapedURL,
	)

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}

	return output, nil
}

func (e FeedEntry) URL() string {
	for _, link := range e.Links {
		if link.Rel == "alternate" && link.Href != "" {
			return link.Href
		}
	}

	if e.VideoID == "" {
		return ""
	}

	return "https://www.youtube.com/watch?v=" + e.VideoID
}

func (e FeedEntry) IsShort() bool {
	return isShortURL(e.URL())
}

func isShortURL(raw string) bool {
	if raw == "" {
		return false
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.Contains(strings.ToLower(raw), "/shorts/")
	}

	return strings.HasPrefix(strings.ToLower(parsed.Path), "/shorts/")
}

func showWindowsNotification(title, message, launchURL string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows notifications are only supported on Windows")
	}

	script := buildNotificationScript(title, message, launchURL)
	encoded := encodePowerShellCommand(script)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func buildNotificationScript(title, message, launchURL string) string {
	escapedTitle := xmlEscape(title)
	escapedMessage := xmlEscape(message)
	escapedURL := xmlEscape(launchURL)

	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime | Out-Null
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType=WindowsRuntime] | Out-Null

$template = @"
<toast activationType="protocol" launch="%s">
  <visual>
    <binding template="ToastGeneric">
      <text>%s</text>
      <text>%s</text>
    </binding>
  </visual>
</toast>
"@

$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($template)
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
$notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("yt-new-video-notify")
$notifier.Show($toast)
`, escapedURL, escapedTitle, escapedMessage)
}

func encodePowerShellCommand(script string) string {
	utf16CodeUnits := utf16.Encode([]rune(script))
	bytes := make([]byte, len(utf16CodeUnits)*2)
	for i, codeUnit := range utf16CodeUnits {
		bytes[i*2] = byte(codeUnit)
		bytes[i*2+1] = byte(codeUnit >> 8)
	}

	return base64.StdEncoding.EncodeToString(bytes)
}

func openBrowser(url string) error {
	if runtime.GOOS != "windows" {
		return errors.New("default browser opening is only implemented for Windows")
	}

	cmd := exec.Command("cmd", "/c", "start", "", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
