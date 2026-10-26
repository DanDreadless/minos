package threats

import "testing"

func TestSplit(t *testing.T) {
	for name, want := range map[string][2]string{
		"www.example.co.uk":                      {"example.co.uk", "www"},
		"d3abc123xyz.cloudfront.net":             {"cloudfront.net", "d3abc123xyz"},
		"a.b.example.com.":                       {"example.com", "a.b"},
		"user.github.io":                         {"github.io", "user"},
		"abcdefgh12345678.blob.core.windows.net": {"windows.net", "abcdefgh12345678.blob.core"},
	} {
		reg, sub, ok := split(name)
		if !ok || reg != want[0] || sub != want[1] {
			t.Errorf("split(%q) = %q, %q, %v; want %q, %q", name, reg, sub, ok, want[0], want[1])
		}
	}
	for _, name := range []string{"localhost", "com", "nas"} {
		if _, _, ok := split(name); ok {
			t.Errorf("split(%q) found a registrable domain", name)
		}
	}
}

// Real, legitimate domains — including CDN, tracker and cloud
// infrastructure that looks odd — must never read as generated.
var benign = []string{
	"google.com", "googleapis.com", "googleusercontent.com", "gstatic.com", "1e100.net",
	"doubleclick.net", "googlesyndication.com", "cloudflare.com", "cloudflare-dns.com",
	"microsoft.com", "microsoftonline.com", "windowsupdate.com", "msftconnecttest.com",
	"footprintdns.com", "trafficmanager.net", "azureedge.net", "azurewebsites.net",
	"netflix.com", "nflxvideo.net", "nflxso.net", "nflximg.net", "nflxext.com",
	"whatsapp.net", "facebook.com", "fbcdn.net", "cdninstagram.com", "akamaiedge.net",
	"akamaihd.net", "akadns.net", "edgekey.net", "edgesuite.net", "amazonaws.com",
	"amazon-adsystem.com", "spotify.com", "spotifycdn.com", "scdn.co", "scorecardresearch.com",
	"crashlytics.com", "appsflyersdk.com", "onetrust.com", "cookielaw.org", "xboxlive.com",
	"playstation.net", "tplinkcloud.com", "ring.com", "samsungcloudsolution.com",
	"samsungqbe.com", "samsungosp.com", "pndsn.com", "bilibili.com", "hdslb.com",
	"mmstat.com", "tbcdn.cn", "qpic.cn", "gvt1.com", "gvt2.com", "ggpht.com", "ytimg.com",
	"googlevideo.com", "youtubei.googleapis.com", "smartthings.com", "tuyaeu.com",
	"tuyaus.com", "iot.eu-west-1.amazonaws.com", "sentry.io", "bugsnag.com", "nr-data.net",
	"newrelic.com", "datadoghq.com", "branch.io", "adjust.com", "kochava.com", "mxpnl.com",
	"zqtk.net", "rbxcdn.com", "roblox.com", "discordapp.net", "steamcontent.com",
	"steamserver.net", "wbx2.com", "webex.com", "zoom.us", "slack-edge.com",
}

// Published DGA samples (Conficker, CryptoLocker, Necurs, Ramnit, Murofet,
// Qakbot) and a digits-mixed shape.
var generated = []string{
	"qnrbmqbvjfx.ws", "kqlxtqptsmys.com", "pxhbfogkrmplf.com", "kvxhvklvrlwrbi.com",
	"tpgulxbofqfx.net", "ndqoxlfxugzk.net", "a1b2c3d4e5f6g7.com", "xjwqmtrvbnkl.org",
}

func TestRandomLabel(t *testing.T) {
	for _, d := range benign {
		reg, _, ok := split(d)
		if ok && randomLabel(label(reg)) {
			t.Errorf("benign %s (%s) reads as generated", d, label(reg))
		}
	}
	for _, d := range generated {
		reg, _, ok := split(d)
		if !ok || !randomLabel(label(reg)) {
			t.Errorf("generated %s (%s) not detected", d, label(reg))
		}
	}
}

func TestTunnelLike(t *testing.T) {
	for _, sub := range []string{
		"www", "api", "e1234.dscb", "r3---sn-aigl6nek", "abcdefgh12345678.blob.core",
		"k3j9x2m8q4w7e1r5t6y0u2i8o4p6a2s.ab.cdefghijklmn.wxyz.abcdef", // A hashed CDN host (29 + short)
		"x8f2k9q3m7w1e5r4t6y0u2i8.vfjxqk4mcz1.ab",                     // A VPN client's hashed name
	} {
		if tunnelLike(sub) {
			t.Errorf("%q reads as tunnelling", sub)
		}
	}
	for _, sub := range []string{
		"4a6f686e20446f6520736563726574.2070617373776f726420313233343536.c2", // Hex-encoded text, dnscat2-style
		"nbswy3dpeb3w64tmmqqgc3tdmfzxi5a.ojswy5dbojsxg5atmmqqgc3t",
	} {
		if !tunnelLike(sub) {
			t.Errorf("%q not detected as tunnelling", sub)
		}
	}
}
