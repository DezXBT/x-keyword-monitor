package main

import (
	"context"
	"sync"
	"time"
)

// Poller scans all enabled keyword queries on a fixed interval, detects tweets
// newer than each keyword's stored cursor, and routes matches to their bound
// Discord channel.
type Poller struct {
	cfg       *Config
	keywords  *KeywordStore
	state     *SeenState
	xClients  []*XClient
	bot       *DiscordBot
	clientIdx int
	mu        sync.Mutex

	// pushedLRU is a global ring of recently-pushed tweet IDs, preventing the
	// same tweet being posted twice when it matches two keywords whose channels
	// happen to be the same. Keyword cursors already dedup within a keyword;
	// this guards cross-keyword dupes into one channel.
	pushedLRU   map[string]struct{}
	pushedOrder []string
}

const pushedLRUCap = 1000

func NewPoller(cfg *Config, keywords *KeywordStore, state *SeenState, xClients []*XClient, bot *DiscordBot) *Poller {
	return &Poller{
		cfg:       cfg,
		keywords:  keywords,
		state:     state,
		xClients:  xClients,
		bot:       bot,
		pushedLRU: make(map[string]struct{}),
	}
}

// nextClient returns the next X client in round-robin order.
func (p *Poller) nextClient() *XClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.xClients[p.clientIdx]
	p.clientIdx = (p.clientIdx + 1) % len(p.xClients)
	return c
}

// markPushed records a tweet ID in the LRU; returns false if it was already there.
func (p *Poller) markPushed(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.pushedLRU[id]; ok {
		return false
	}
	p.pushedLRU[id] = struct{}{}
	p.pushedOrder = append(p.pushedOrder, id)
	if len(p.pushedOrder) > pushedLRUCap {
		old := p.pushedOrder[0]
		p.pushedOrder = p.pushedOrder[1:]
		delete(p.pushedLRU, old)
	}
	return true
}

// RunResyncCheck logs whether cookies changed while the bot was offline.
func (p *Poller) RunResyncCheck() {
	current := p.cfg.CookieHash()
	stored := p.state.GetCookieHash()
	if stored != "" && stored != current {
		logInfo("[resync] cookie changed while offline (was %s, now %s)", stored, current)
	}
	p.state.SetCookieHash(current)
	p.state.Save()
}

// Run is the main scan loop until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	interval := p.cfg.PollIntervalDuration()
	logInfo("[poll] keyword monitor started, interval %s", interval)

	// Prime cursors on first run so we don't blast the channel with backlog:
	// for any keyword with an empty cursor, seed it to the newest current match
	// without pushing.
	p.primeCursors()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logInfo("[poll] stopping")
			return
		case <-ticker.C:
			p.scanAll(ctx)
		}
	}
}

// primeCursors seeds last_seen_id for fresh keywords to the current newest match,
// so a newly-added keyword starts from "now" instead of flooding with history.
func (p *Poller) primeCursors() {
	for _, kw := range p.keywords.EnabledList() {
		if kw.LastSeenID != "" {
			continue
		}
		client := p.nextClient()
		tweets, err := client.SearchKeyword(kw.Query, p.cfg.Search.TweetsPerQuery)
		if err != nil {
			logWarn("[prime] @%s query %q: %v", kw.ID, kw.Query, err)
			continue
		}
		if len(tweets) > 0 {
			// tweets are sorted newest-first
			p.keywords.SetLastSeen(kw.ID, tweets[0].ID)
			logInfo("[prime] %s seeded at %s (%d current matches)", kw.ID, tweets[0].ID, len(tweets))
		}
		time.Sleep(p.cfg.PerKeywordDelayDuration())
	}
}

// scanAll runs one full cycle over every enabled keyword.
func (p *Poller) scanAll(ctx context.Context) {
	kws := p.keywords.EnabledList()
	if len(kws) == 0 {
		return
	}
	delay := p.cfg.PerKeywordDelayDuration()
	for _, kw := range kws {
		select {
		case <-ctx.Done():
			return
		default:
		}
		p.scanKeyword(kw)
		time.Sleep(delay)
	}
}

// scanKeyword scans a single keyword and pushes any new matches.
func (p *Poller) scanKeyword(kw Keyword) {
	client := p.nextClient()
	tweets, err := client.SearchKeyword(kw.Query, p.cfg.Search.TweetsPerQuery)
	if err != nil {
		logWarn("[scan] %s query %q: %v", kw.ID, kw.Query, err)
		return
	}
	if len(tweets) == 0 {
		return
	}

	// tweets are sorted newest-first. Collect those strictly newer than cursor,
	// then push oldest-first so the channel reads chronologically.
	var fresh []Tweet
	for _, t := range tweets {
		if t.ID <= kw.LastSeenID {
			break // rest are older (sorted desc)
		}
		fresh = append(fresh, t)
	}
	if len(fresh) == 0 {
		return
	}

	newestID := fresh[0].ID
	// reverse to oldest-first
	for i, j := 0, len(fresh)-1; i < j; i, j = i+1, j-1 {
		fresh[i], fresh[j] = fresh[j], fresh[i]
	}

	pushed := 0
	for _, t := range fresh {
		// optional engagement floor
		if kw.MinFaves > 0 && t.Metrics.Likes < kw.MinFaves {
			continue
		}
		if !p.markPushed(t.ID) {
			continue // already pushed to this channel via another keyword
		}
		if err := p.bot.SendKeywordHit(kw, t); err != nil {
			logError("[scan] %s push %s: %v", kw.ID, t.ID, err)
			continue
		}
		pushed++
	}

	p.keywords.SetLastSeen(kw.ID, newestID)
	if pushed > 0 {
		logInfo("[scan] %s: %d new match(es) pushed", kw.ID, pushed)
	}
}

// HealthCheck periodically verifies cookies are alive.
func (p *Poller) HealthCheck(ctx context.Context) {
	interval := p.cfg.HealthCheckDuration()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, xc := range p.xClients {
				if err := xc.HealthCheck(); err != nil {
					logWarn("[health] cookie %s unhealthy: %v", xc.label, err)
				}
			}
		}
	}
}
