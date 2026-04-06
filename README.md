# yt-new-video-notify

Minimal Go CLI for Windows that watches one or more YouTube channels for new uploads without a Data API key.

## How it works

- Resolves the channel URL to a `UC...` channel ID from the public page HTML.
- Polls YouTube's public uploads feed: `https://www.youtube.com/feeds/videos.xml?channel_id=...`
- Stores the latest seen publish timestamp plus recent video IDs per channel in `state.json`.
- On a new upload, shows a Windows notification and opens the matching channel page in the default browser.

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
  "channels": [
    {
      "channel_url": "https://www.youtube.com/@username",
      "poll_interval_seconds": 30,
      "skip_shorts": true,
      "skip_livestreams": true
    },
    {
      "channel_url": "https://www.youtube.com/@another-channel",
      "poll_interval_seconds": 60,
      "skip_shorts": false,
      "skip_livestreams": false
    }
  ],
  "heartbeat_interval_seconds": 300
}
```

| Field                        | Description                                         |
| ---------------------------- | --------------------------------------------------- |
| `channels`                   | Array of channel configs to watch                   |
| `channel_url`                | YouTube channel URL for that channel entry          |
| `poll_interval_seconds`      | How often to check that channel for new videos      |
| `skip_shorts`                | Ignore YouTube Shorts (`/shorts/` URLs) for channel |
| `skip_livestreams`           | Ignore livestream/watch-page entries for channel    |
| `heartbeat_interval_seconds` | How often to print status lines for all channels    |

The first run sets a baseline to the current latest upload for each configured channel and does not notify for existing videos.
Channel IDs are resolved from each `channel_url` automatically and cached in `state.json`.
YouTube's public feed can include livestream-related entries as well as regular uploads, so `skip_livestreams` resolves new items against the watch page before notifying.

## License

Created entirely by OpenAI GPT-5.4.
