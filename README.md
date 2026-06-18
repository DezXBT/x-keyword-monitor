# x-keyword-monitor

Monitors X/Twitter **search by keyword** in near-real-time and pushes new matching
tweets to per-keyword Discord channels. Sibling to `x-notify-dc` (follow-monitor)
and `X-Tracker-Bot` (follower-tracker) — this one watches *content*, not accounts.

## How it works

Each keyword is an X search query (full operator support) bound to a Discord
channel. A poller scans every enabled keyword once per cycle via the GraphQL
`SearchTimeline` endpoint (`product: Latest`, reverse-chronological), detects
tweets newer than each keyword's stored cursor, and posts them — oldest-first —
to the bound channel. Contract addresses in matched tweets are auto-detected and
resolved to DexScreener chart buttons.

## Slash commands

| Command | Description |
|---------|-------------|
| `/kw add query:<q> channel:<#ch> [id:<slug>] [min_faves:<n>]` | Add a keyword monitor |
| `/kw remove id:<slug>` | Remove a keyword |
| `/kw list` | List all monitors + status |
| `/kw pause id:<slug>` | Pause a keyword |
| `/kw resume id:<slug>` | Resume a keyword |

### Query examples
- `"fair launch" -filter:replies` — phrase match, no replies
- `$PEPE min_faves:5` — cashtag with engagement floor
- `freemint filter:links` — only posts with links
- `("CA soon" OR stealth) -giveaway -airdrop` — boolean + noise exclusion

## Setup

```bash
cp config.example.yaml config.yaml   # fill in bot token + cookies
go build -o x-keyword-monitor .
screen -dmS x-keyword ./x-keyword-monitor
```

## Notes

- **Poll interval**: default 20s full cycle. Each keyword = 1 API request; more
  keywords = longer effective cycle. Below ~15s risks rate limits.
- **Search lag**: X's search index trails live posts by ~5-30s, so detection is
  near-real-time, not instant.
- **Cursor priming**: a freshly-added keyword seeds its cursor to the current
  newest match (no historical backlog flood) and pushes only tweets posted after.
- **Dedup**: per-keyword cursor + a global LRU ring guard against double-posting.
- Cookies rotate round-robin across all configured pairs to spread rate-limit load.
