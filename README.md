# yt-new-video-notify

Minimal Go CLI for Windows that watches a YouTube channel for new uploads without a Data API key.

## How it works

- Resolves the channel URL to a `UC...` channel ID from the public page HTML.
- Polls YouTube's public uploads feed: `https://www.youtube.com/feeds/videos.xml?channel_id=...`
- Stores the latest seen video in `state.json`.
- On a new upload, shows a Windows notification and opens the channel page in the default browser.

## Build

```powershell
go build -o yt-new-video-notify.exe .
```

## Usage

```powershell
# Watch mode — polls continuously
yt-new-video-notify.exe

# Single check, then exit
yt-new-video-notify.exe -once
```

Alternatively, run without building:

```powershell
go run .
go run . -once
```

## Configuration

Edit [`config.json`](config.json):

```json
{
  "channel_url": "https://www.youtube.com/@username",
  "poll_interval_seconds": 30,
  "heartbeat_interval_seconds": 300,
  "skip_shorts": true
}
```

| Field                        | Description                             |
| ---------------------------- | --------------------------------------- |
| `channel_url`                | YouTube channel URL to watch            |
| `poll_interval_seconds`      | How often to check for new videos       |
| `heartbeat_interval_seconds` | How often to print a status line        |
| `skip_shorts`                | Ignore YouTube Shorts (`/shorts/` URLs) |

The first run sets a baseline to the current latest upload and does not notify for existing videos.
The channel ID is resolved from `channel_url` automatically and cached in `state.json`.

## License

Created entirely by OpenAI GPT-5.4.
