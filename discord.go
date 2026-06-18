package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// DiscordBot manages Discord interactions and keyword-hit embeds.
type DiscordBot struct {
	session  *discordgo.Session
	cfg      *Config
	keywords *KeywordStore
}

func NewDiscordBot(cfg *Config, keywords *KeywordStore) (*DiscordBot, error) {
	dg, err := discordgo.New("Bot " + cfg.Discord.BotToken)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	dg.Identify.Intents = discordgo.IntentGuilds
	return &DiscordBot{session: dg, cfg: cfg, keywords: keywords}, nil
}

func (db *DiscordBot) Open() error  { return db.session.Open() }
func (db *DiscordBot) Close()       { db.session.Close() }

// RegisterHandlers wires the interaction handler.
func (db *DiscordBot) RegisterHandlers() {
	db.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionApplicationCommand {
			return
		}
		switch i.ApplicationCommandData().Name {
		case "kw":
			db.handleKw(s, i)
		}
	})
}

// RegisterCommands registers the /kw slash command (guild-scoped if guildID set).
func (db *DiscordBot) RegisterCommands(guildID string) error {
	cmd := &discordgo.ApplicationCommand{
		Name:        "kw",
		Description: "Manage X keyword monitors",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Add a keyword/search query bound to a channel",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "query", Description: "X search query (operators allowed)", Required: true},
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Channel to push matches to", Required: true},
					{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Short unique id (default: derived from query)", Required: false},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "min_faves", Description: "Skip tweets below this like count", Required: false},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "remove",
				Description: "Remove a keyword by id",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Keyword id", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "list",
				Description: "List all keyword monitors",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "pause",
				Description: "Pause a keyword by id",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Keyword id", Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "resume",
				Description: "Resume a keyword by id",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "id", Description: "Keyword id", Required: true},
				},
			},
		},
	}
	_, err := db.session.ApplicationCommandCreate(db.session.State.User.ID, guildID, cmd)
	return err
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func optString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts {
		if o.Name == name {
			return o.StringValue()
		}
	}
	return ""
}

func optInt(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) int {
	for _, o := range opts {
		if o.Name == name {
			return int(o.IntValue())
		}
	}
	return 0
}

func optChannel(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts {
		if o.Name == name && o.Type == discordgo.ApplicationCommandOptionChannel {
			return o.ChannelValue(nil).ID
		}
	}
	return ""
}

// handleKw routes /kw subcommands.
func (db *DiscordBot) handleKw(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		respondEphemeral(s, i, "no subcommand")
		return
	}
	sub := data.Options[0]
	opts := sub.Options

	switch sub.Name {
	case "add":
		query := optString(opts, "query")
		channel := optChannel(opts, "channel")
		id := optString(opts, "id")
		minFaves := optInt(opts, "min_faves")
		if query == "" || channel == "" {
			respondEphemeral(s, i, "query and channel are required")
			return
		}
		kw, err := db.keywords.Add(id, query, channel, minFaves)
		if err != nil {
			respondEphemeral(s, i, "❌ "+err.Error())
			return
		}
		respondEphemeral(s, i, fmt.Sprintf("✅ Added `%s` → <#%s>\nQuery: `%s`%s", kw.ID, channel, query, faveSuffix(minFaves)))

	case "remove":
		id := optString(opts, "id")
		ok, err := db.keywords.Remove(id)
		if err != nil {
			respondEphemeral(s, i, "❌ "+err.Error())
			return
		}
		if !ok {
			respondEphemeral(s, i, fmt.Sprintf("❌ no keyword with id `%s`", id))
			return
		}
		respondEphemeral(s, i, fmt.Sprintf("🗑️ Removed `%s`", id))

	case "pause", "resume":
		id := optString(opts, "id")
		enabled := sub.Name == "resume"
		ok, err := db.keywords.SetEnabled(id, enabled)
		if err != nil {
			respondEphemeral(s, i, "❌ "+err.Error())
			return
		}
		if !ok {
			respondEphemeral(s, i, fmt.Sprintf("❌ no keyword with id `%s`", id))
			return
		}
		verb := "⏸️ Paused"
		if enabled {
			verb = "▶️ Resumed"
		}
		respondEphemeral(s, i, fmt.Sprintf("%s `%s`", verb, id))

	case "list":
		db.handleKwList(s, i)
	}
}

func faveSuffix(n int) string {
	if n > 0 {
		return fmt.Sprintf("\nmin_faves: %d", n)
	}
	return ""
}

func (db *DiscordBot) handleKwList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	kws := db.keywords.List()
	if len(kws) == 0 {
		respondEphemeral(s, i, "No keyword monitors configured. Add one with `/kw add`.")
		return
	}
	var b strings.Builder
	for n, kw := range kws {
		status := "🟢"
		if !kw.Enabled {
			status = "⏸️"
		}
		fmt.Fprintf(&b, "%d. %s **%s** → <#%s>\n", n+1, status, kw.ID, kw.ChannelID)
		fmt.Fprintf(&b, "   `%s`", kw.Query)
		if kw.MinFaves > 0 {
			fmt.Fprintf(&b, " · min_faves: %d", kw.MinFaves)
		}
		b.WriteString("\n")
	}
	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("🔍 Keyword Monitors (%d)", len(kws)),
		Description: b.String(),
		Color:       0x1DA1F2,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("x-keyword-monitor | %s WIB", time.Now().In(db.cfg.Timezone()).Format("02/01/2006, 15:04:05")),
		},
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

// SendKeywordHit posts a matched tweet to the keyword's bound channel, with CA
// detection + DexScreener chart buttons (reused from the notify bot).
func (db *DiscordBot) SendKeywordHit(kw Keyword, tweet Tweet) error {
	loc := db.cfg.Timezone()

	embed := &discordgo.MessageEmbed{
		URL:   tweet.TweetURL,
		Color: 0xFFD700,
		Author: &discordgo.MessageEmbedAuthor{
			Name:    fmt.Sprintf("%s (@%s)", tweet.Author.Name, tweet.Author.ScreenName),
			URL:     fmt.Sprintf("https://x.com/%s", tweet.Author.ScreenName),
			IconURL: tweet.Author.AvatarURL,
		},
		Description: tweet.Text,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("x-keyword-monitor | 🔍 %s | %s WIB", kw.ID, time.Now().In(loc).Format("02/01/2006, 15:04:05")),
		},
	}

	if len(tweet.MediaURLs) > 0 {
		embed.Image = &discordgo.MessageEmbedImage{URL: tweet.MediaURLs[0]}
	} else if tweet.Author.AvatarURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: tweet.Author.AvatarURL}
	}

	// CA detection
	caScanText := tweet.Text
	if len(tweet.URLs) > 0 {
		caScanText += " " + joinStrings(tweet.URLs, " ")
	}
	contracts := detectContracts(caScanText)
	for i := range contracts {
		contracts[i].ResolveDexScreener()
	}
	if len(contracts) > 0 {
		var caLines []string
		for _, c := range contracts {
			line := fmt.Sprintf("`%s`", c.Address)
			if c.Resolved() {
				tag := c.ResolvedChain
				if c.Symbol != "" {
					tag = "$" + c.Symbol + " · " + c.ResolvedChain
				}
				line += fmt.Sprintf("\n↳ [📈 %s](%s)", tag, c.ChartURL())
			} else {
				line += fmt.Sprintf(" · [🔍 search](%s)", c.ChartURL())
			}
			caLines = append(caLines, line)
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "💰 Contract Detected",
			Value:  joinStrings(caLines, "\n"),
			Inline: false,
		})
	}

	// Buttons
	var buttons []discordgo.MessageComponent
	if tweet.TweetURL != "" {
		buttons = append(buttons, discordgo.Button{
			Label: "View Tweet", Style: discordgo.LinkButton,
			Emoji: &discordgo.ComponentEmoji{Name: "🐦"}, URL: tweet.TweetURL,
		})
	}
	if tweet.Author.ScreenName != "" {
		buttons = append(buttons, discordgo.Button{
			Label: "Profile", Style: discordgo.LinkButton,
			Emoji: &discordgo.ComponentEmoji{Name: "👤"}, URL: fmt.Sprintf("https://x.com/%s", tweet.Author.ScreenName),
		})
	}
	for idx, c := range contracts {
		if idx >= 3 {
			break
		}
		label := "Chart"
		switch {
		case c.Resolved() && c.Symbol != "":
			label = fmt.Sprintf("📈 $%s (%s)", c.Symbol, c.ResolvedChain)
		case c.Resolved():
			label = fmt.Sprintf("📈 Chart (%s)", c.ResolvedChain)
		case len(contracts) > 1:
			label = fmt.Sprintf("Chart %s", c.Short())
		}
		buttons = append(buttons, discordgo.Button{
			Label: label, Style: discordgo.LinkButton,
			Emoji: &discordgo.ComponentEmoji{Name: "📈"}, URL: c.ChartURL(),
		})
	}

	msg := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}}
	if len(buttons) > 0 {
		msg.Components = []discordgo.MessageComponent{discordgo.ActionsRow{Components: buttons}}
	}
	_, err := db.session.ChannelMessageSendComplex(kw.ChannelID, msg)
	return err
}

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
