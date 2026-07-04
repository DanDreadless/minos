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
	{"discord", "Discord", []string{"discord.com", "discordapp.com", "discord.gg", "discordapp.net", "discord.media", "discordcdn.com"}},
	{"disneyplus", "Disney+", []string{"disneyplus.com", "disney-plus.net", "dssott.com", "bamgrid.com", "disneystreaming.com"}},
	{"epicgames", "Epic Games / Fortnite", []string{"epicgames.com", "epicgames.dev", "unrealengine.com", "fortnite.com"}},
	{"facebook", "Facebook & Messenger", []string{"facebook.com", "fb.com", "fb.watch", "fbcdn.net", "facebook.net", "fbsbx.com", "messenger.com"}},
	{"hulu", "Hulu", []string{"hulu.com", "hulustream.com", "huluim.com"}},
	{"instagram", "Instagram", []string{"instagram.com", "cdninstagram.com", "ig.me"}},
	{"minecraft", "Minecraft", []string{"minecraft.net", "mojang.com", "minecraftservices.com", "minecraft-services.net"}},
	{"netflix", "Netflix", []string{"netflix.com", "nflxvideo.net", "nflximg.net", "nflxso.net", "nflxext.com"}},
	{"onlyfans", "OnlyFans", []string{"onlyfans.com", "onlyfansassets.com"}},
	{"pinterest", "Pinterest", []string{"pinterest.com", "pinimg.com"}},
	{"primevideo", "Prime Video", []string{"primevideo.com", "aiv-cdn.net", "aiv-delivery.net"}},
	{"reddit", "Reddit", []string{"reddit.com", "redd.it", "redditmedia.com", "redditstatic.com"}},
	{"roblox", "Roblox", []string{"roblox.com", "rbxcdn.com", "rbx.com", "robloxlabs.com", "rbxtrk.com"}},
	{"snapchat", "Snapchat", []string{"snapchat.com", "snap.com", "sc-cdn.net", "snapkit.com", "snap-dev.net"}},
	{"spotify", "Spotify", []string{"spotify.com", "scdn.co", "spotifycdn.com", "pscdn.co"}},
	{"steam", "Steam", []string{"steampowered.com", "steamcommunity.com", "steamstatic.com", "steamcontent.com", "steamusercontent.com"}},
	{"telegram", "Telegram", []string{"telegram.org", "telegram.me", "t.me", "tdesktop.com", "telesco.pe"}},
	{"tiktok", "TikTok", []string{"tiktok.com", "tiktokv.com", "tiktokcdn.com", "tiktokcdn-us.com", "ttlivecdn.com", "musical.ly", "byteoversea.com", "ibytedtos.com", "muscdn.com"}},
	{"tumblr", "Tumblr", []string{"tumblr.com", "tmblr.co"}},
	{"twitch", "Twitch", []string{"twitch.tv", "ttvnw.net", "jtvnw.net", "twitchcdn.net", "twitchsvc.net"}},
	{"twitter", "X (Twitter)", []string{"x.com", "twitter.com", "twimg.com", "t.co"}},
	{"vk", "VK", []string{"vk.com", "vk.me", "userapi.com", "vkuservideo.net"}},
	{"whatsapp", "WhatsApp", []string{"whatsapp.com", "whatsapp.net", "wa.me"}},
	{"youtube", "YouTube", []string{"youtube.com", "youtu.be", "ytimg.com", "googlevideo.com", "youtube-nocookie.com", "youtubei.googleapis.com", "youtube.googleapis.com", "youtubekids.com"}},
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
