// Package services is the curated catalog behind one-click "blocked
// services": each service maps to the domains that must be denied to block
// it. The catalog is static data compiled into the binary — it has no
// dependencies and may be imported by config for validation.
//
// Domains err on the side of the service's own infrastructure (sites, apps,
// CDNs); they never include shared third-party hosts that would break
// unrelated things.
package services

import "sort"

// Service is one blockable service.
type Service struct {
	Name    string   `json:"name"`  // stable key used in config
	Label   string   `json:"label"` // display name for the UI
	Domains []string `json:"domains"`
}

var catalog = []Service{
	{"9gag", "9GAG", []string{"9gag.com", "9cache.com"}},
	{"amazon", "Amazon (shopping)", []string{"amazon.com", "amazon.co.uk", "amazon.de", "amazon.fr", "amazon.it", "amazon.es", "amazon.ca", "amazon.com.au", "amazon.in", "amazon.co.jp", "media-amazon.com", "ssl-images-amazon.com", "amazonpay.com"}},
	{"battlenet", "Battle.net (Blizzard)", []string{"battle.net", "blizzard.com", "blz-contentstack.com"}},
	{"bereal", "BeReal", []string{"bereal.com", "bereal.team"}},
	{"bluesky", "Bluesky", []string{"bsky.app", "bsky.social", "bsky.network"}},
	{"chatgpt", "ChatGPT (OpenAI)", []string{"chatgpt.com", "openai.com", "oaistatic.com", "oaiusercontent.com"}},
	{"claude", "Claude (Anthropic)", []string{"claude.ai", "anthropic.com"}},
	{"crunchyroll", "Crunchyroll", []string{"crunchyroll.com", "vrv.co", "crunchyrollsvc.com"}},
	{"dailymotion", "Dailymotion", []string{"dailymotion.com", "dmcdn.net", "dm-event.net"}},
	{"deezer", "Deezer", []string{"deezer.com", "dzcdn.net"}},
	{"discord", "Discord", []string{"discord.com", "discordapp.com", "discord.gg", "discordapp.net", "discord.media", "discordcdn.com"}},
	{"disneyplus", "Disney+", []string{"disneyplus.com", "disney-plus.net", "dssott.com", "bamgrid.com", "disneystreaming.com"}},
	{"ea", "EA / Origin", []string{"ea.com", "origin.com", "easports.com"}},
	{"ebay", "eBay", []string{"ebay.com", "ebay.co.uk", "ebay.de", "ebaystatic.com", "ebayimg.com", "ebaydesc.com"}},
	// Bypass resistance: hardcoded public DoH/DoT endpoints apps and browsers
	// use to sidestep the network resolver. Provider-owned hostnames only,
	// never shared infrastructure. Blocking this does not affect Minos's own
	// upstreams (they bypass the filter; the presets are IP-literal anyway) —
	// config validation warns about the one self-sabotage case, a hand-typed
	// hostname upstream.
	{"encrypted-dns", "Encrypted DNS bypass (public DoH/DoT providers)", []string{"cloudflare-dns.com", "one.one.one.one", "dns.google", "dns.quad9.net", "doh.opendns.com", "dns.adguard-dns.com", "doh.cleanbrowsing.org", "dns.nextdns.io", "freedns.controld.com", "dns.mullvad.net", "doh.libredns.gr", "dns.sb"}},
	{"epicgames", "Epic Games / Fortnite", []string{"epicgames.com", "epicgames.dev", "unrealengine.com", "fortnite.com"}},
	{"facebook", "Facebook & Messenger", []string{"facebook.com", "fb.com", "fb.watch", "fbcdn.net", "facebook.net", "fbsbx.com", "messenger.com"}},
	// github.io is user content hosted by GitHub itself, not shared
	// third-party infrastructure — it belongs in the bundle.
	{"github", "GitHub", []string{"github.com", "githubusercontent.com", "githubassets.com", "github.io"}},
	{"hbomax", "HBO Max", []string{"max.com", "hbomax.com", "hbo.com", "hbomaxcdn.com"}},
	{"hulu", "Hulu", []string{"hulu.com", "hulustream.com", "huluim.com"}},
	{"instagram", "Instagram", []string{"instagram.com", "cdninstagram.com", "ig.me"}},
	{"kick", "Kick", []string{"kick.com"}},
	{"linkedin", "LinkedIn", []string{"linkedin.com", "licdn.com"}},
	// Mastodon is decentralised; this covers the flagship instances only.
	{"mastodon", "Mastodon (major instances)", []string{"mastodon.social", "mastodon.online", "mstdn.social", "joinmastodon.org"}},
	{"minecraft", "Minecraft", []string{"minecraft.net", "mojang.com", "minecraftservices.com", "minecraft-services.net"}},
	{"netflix", "Netflix", []string{"netflix.com", "nflxvideo.net", "nflximg.net", "nflxso.net", "nflxext.com"}},
	{"nintendo", "Nintendo Online", []string{"nintendo.com", "nintendo.net", "nintendo-europe.com"}},
	{"onlyfans", "OnlyFans", []string{"onlyfans.com", "onlyfansassets.com"}},
	{"paramountplus", "Paramount+", []string{"paramountplus.com", "pplusstatic.com", "cbsaavideo.com"}},
	{"pinterest", "Pinterest", []string{"pinterest.com", "pinimg.com"}},
	{"playstation", "PlayStation Network", []string{"playstation.com", "playstation.net", "sonyentertainmentnetwork.com"}},
	{"primevideo", "Prime Video", []string{"primevideo.com", "aiv-cdn.net", "aiv-delivery.net"}},
	{"reddit", "Reddit", []string{"reddit.com", "redd.it", "redditmedia.com", "redditstatic.com"}},
	{"riot", "Riot Games (LoL, Valorant)", []string{"riotgames.com", "leagueoflegends.com", "valorant.com", "riotcdn.net"}},
	{"roblox", "Roblox", []string{"roblox.com", "rbxcdn.com", "rbx.com", "robloxlabs.com", "rbxtrk.com"}},
	{"signal", "Signal", []string{"signal.org", "signal.art", "whispersystems.org"}},
	{"snapchat", "Snapchat", []string{"snapchat.com", "snap.com", "sc-cdn.net", "snapkit.com", "snap-dev.net"}},
	{"soundcloud", "SoundCloud", []string{"soundcloud.com", "sndcdn.com"}},
	{"spotify", "Spotify", []string{"spotify.com", "scdn.co", "spotifycdn.com", "pscdn.co"}},
	{"steam", "Steam", []string{"steampowered.com", "steamcommunity.com", "steamstatic.com", "steamcontent.com", "steamusercontent.com"}},
	{"telegram", "Telegram", []string{"telegram.org", "telegram.me", "t.me", "tdesktop.com", "telesco.pe"}},
	{"temu", "Temu", []string{"temu.com"}},
	{"threads", "Threads", []string{"threads.net", "threads.com"}},
	{"tiktok", "TikTok", []string{"tiktok.com", "tiktokv.com", "tiktokcdn.com", "tiktokcdn-us.com", "ttlivecdn.com", "musical.ly", "byteoversea.com", "ibytedtos.com", "muscdn.com"}},
	{"tinder", "Tinder", []string{"tinder.com", "gotinder.com"}},
	{"tumblr", "Tumblr", []string{"tumblr.com", "tmblr.co"}},
	{"twitch", "Twitch", []string{"twitch.tv", "ttvnw.net", "jtvnw.net", "twitchcdn.net", "twitchsvc.net"}},
	{"twitter", "X (Twitter)", []string{"x.com", "twitter.com", "twimg.com", "t.co"}},
	{"ubisoft", "Ubisoft", []string{"ubisoft.com", "ubi.com", "ubisoftconnect.com"}},
	{"vimeo", "Vimeo", []string{"vimeo.com", "vimeocdn.com"}},
	{"vk", "VK", []string{"vk.com", "vk.me", "userapi.com", "vkuservideo.net"}},
	// weixin.qq.com is a subdomain-scoped deny: qq.com itself stays clear.
	{"wechat", "WeChat", []string{"wechat.com", "weixin.qq.com"}},
	{"whatsapp", "WhatsApp", []string{"whatsapp.com", "whatsapp.net", "wa.me"}},
	{"xbox", "Xbox Live", []string{"xbox.com", "xboxlive.com", "xboxservices.com"}},
	{"youtube", "YouTube", []string{"youtube.com", "youtu.be", "ytimg.com", "googlevideo.com", "youtube-nocookie.com", "youtubei.googleapis.com", "youtube.googleapis.com", "youtubekids.com"}},
	{"zoom", "Zoom", []string{"zoom.us", "zoomgov.com", "zoom.com"}},
}

var byName = func() map[string]*Service {
	m := make(map[string]*Service, len(catalog))
	for i := range catalog {
		m[catalog[i].Name] = &catalog[i]
	}
	return m
}()

// All returns the catalog sorted by label, for the UI.
func All() []Service {
	out := make([]Service, len(catalog))
	copy(out, catalog)
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Exists reports whether name is a known service (config validation).
func Exists(name string) bool {
	_, ok := byName[name]
	return ok
}

// Domains returns the deny domains for a known service, nil otherwise.
func Domains(name string) []string {
	if s, ok := byName[name]; ok {
		return s.Domains
	}
	return nil
}

// allowExtra lists additional domains a service needs to *work* when it is
// pardoned rather than blocked: playback, auth, and license hosts that live
// on shared CDNs and therefore must never appear in the deny bundles above.
// Entries are precise hostnames — never a shared-CDN apex like
// cloudfront.net or akamaihd.net, which would pardon unrelated things.
// Sourced from the Pi-hole community's commonly-whitelisted lists; expect
// them to drift as the services move distributions.
var allowExtra = map[string][]string{
	"disneyplus": {
		"cdn.registerdisney.go.com", // sign-in
	},
	"primevideo": {
		"amazonvideo.com",               // app control plane
		"atv-ps.amazon.com",             // playback services
		"atv-ext.amazon.com",            // playback API
		"atv-ext-eu.amazon.com",         //   … EU region
		"atv-ext-fe.amazon.com",         //   … FE region
		"pv-cdn.net",                    // Prime Video's own CDN
		"avodmp4s3ww-a.akamaihd.net",    // video segments (Akamai distribution)
		"d25xi40x97liuc.cloudfront.net", // artwork (CloudFront distribution)
		"dmqdd6hw24ucf.cloudfront.net",  // playback manifests (CloudFront distribution)
		"d22qjgkvxw22r6.cloudfront.net", // playback (CloudFront distribution)
	},
}

// AllowDomains returns the domains pardoned when a service is allowed: its
// deny bundle plus any extras playback needs. Defined for every catalog
// service (default: identical to Domains); allow entries cover subdomains,
// so the deny bundle already unblocks anything a blocklist names under it.
func AllowDomains(name string) []string {
	base := Domains(name)
	extra := allowExtra[name]
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}
