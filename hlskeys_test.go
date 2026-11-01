package wittytuner

import (
	"strings"
	"testing"
)

// A key is a key however its line is written. Looking for the words METHOD=NONE anywhere in the line
// let a keyed stream through as clear, and a long line stopped the reader before it got to one.
func TestHLSKeysThatLookLikeNone(t *testing.T) {
	long := "#" + strings.Repeat("x", 1<<20+10) + "\n"
	for _, l := range []string{
		"#EXT-X-KEY:METHOD=AES-128,URI=\"k\",IV=\"METHOD=NONE\"\n",
		"#EXT-X-KEY:URI=\"METHOD=NONE\",METHOD=AES-128\n",
		"#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"key?method=none\"\n",
		"#EXT-X-KEY:METHOD=NONE,METHOD=AES-128,URI=\"k\"\n",
		"#EXT-X-KEY:URI=\"k\"\n",
		"#EXT-X-KEY:\n",
		"#EXT-X-SESSION-KEY:METHOD=SAMPLE-AES,URI=\"k\"\n",
		"  #EXT-X-KEY:METHOD=AES-128,URI=\"k\"\r\n",
		long + "#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n",
	} {
		if why := hlsProtection([]byte("#EXTM3U\n" + l + "#EXTINF:6,\nseg.ts\n")); why == "" {
			t.Errorf("%.60q passed as clear", l)
		}
	}
	for _, l := range []string{"#EXT-X-KEY:METHOD=NONE\n", "#EXT-X-KEY:METHOD=\"NONE\"\n", "#EXTINF:6,METHOD=AES-128\n", "#EXT-X-MAP:URI=\"init.mp4\"\n"} {
		if why := hlsProtection([]byte("#EXTM3U\n" + l)); why != "" {
			t.Errorf("%q: %s", l, why)
		}
	}
	if why := hlsProtection([]byte("#EXT-X-KEY:METHOD=SAMPLE-AES,KEYFORMAT=\"com.example\",URI=\"k\"\n")); !strings.Contains(why, "DRM") {
		t.Errorf("%q", why)
	}
}

// A long line can't hide a variant from being looked at either.
func TestHLSVariantsPastALongLine(t *testing.T) {
	got := hlsVariants([]byte("#EXTM3U\n#" + strings.Repeat("x", 1<<20+10) + "\n#EXT-X-STREAM-INF:BANDWIDTH=1\nlocked.m3u8\n"))
	if len(got) != 1 || got[0] != "locked.m3u8" {
		t.Errorf("%v", got)
	}
}

func FuzzHLSReading(f *testing.F) {
	f.Add("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,IV=\"METHOD=NONE\"\n")
	f.Add("#EXT-X-MEDIA:URI=\"a\",NAME=\"b\n#EXT-X-STREAM-INF:\nv.m3u8\n")
	f.Fuzz(func(t *testing.T, s string) {
		why := hlsProtection([]byte(s))
		hlsVariants([]byte(s))
		// However it is written, an AES key the plain reader of a line would act on isn't clear.
		if why == "" && strings.Contains(s, "\n#EXT-X-KEY:METHOD=AES-128\n") {
			t.Fatalf("%q", s)
		}
	})
}
