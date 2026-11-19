// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The demo tuner: a network tuner that runs inside WittyTuner on this machine, for trying live TV
// with no hardware and no internet, for demonstrations, and for testing. It answers the same
// discovery (UDP) and HTTP lineup protocol a network tuner box does, so everything after "add a
// tuner" is the real network tuner's code: the lineup, a scan, tuning over HTTP, the busy and
// error answers, the guard that refuses encrypted streams. Its channels are made as they're
// watched, by FFmpeg: test patterns with the channel's name and a clock, and clips from a folder
// the owner chooses, looped. It has a guide of its own, so the guide, WittyTV, recordings and
// catching up to live all work. It can drop its signal now and then, and tune slowly, to see how
// that's handled. Everything about it says it's a demo.

// DemoModel is what the demo tuner calls itself in discover.json.
const DemoModel = "WITTYDEMO"

// demoDeviceID is its device id in discovery.
const demoDeviceID = 0x1057D3E0

// DemoDefaultPort is where the demo tuner answers unless told otherwise.
const DemoDefaultPort = "47601"

func init() {
	register(Driver{
		ID: "demo", Name: "Demo tuner (built in)", Status: "tested",
		Description: "A pretend network tuner on this server, with test channels and a guide of its own. For trying live TV without a tuner or the internet. Nothing it shows is real TV.",
		Scans:       true,
		Fields: []Field{
			{Key: "tuners", Label: "Tuners", Kind: "number", Default: "2", Help: "How many channels it can show at once, as a real tuner box has a few."},
			{Key: "clips", Label: "Folder of clips (optional)", Kind: "path", Help: "Video files in this folder on the server become channels of their own, played in a loop."},
			{Key: "slowTune", Label: "Slow tuning (seconds)", Kind: "number", Default: "0", Help: "Wait this long before a channel starts, as a weak signal does. For testing."},
			{Key: "dropEvery", Label: "Lose the signal every (minutes)", Kind: "number", Default: "0", Help: "Cut the stream off now and then, to see how the server copes. 0 never does."},
			{Key: "port", Label: "Port", Kind: "number", Default: DemoDefaultPort, Help: "Where on this machine the demo tuner answers. Change it only if another program uses it."},
		},
		build: func(c Config, e Env) (Source, error) {
			d, err := demoDevice(c.Options)
			if err != nil {
				return nil, err
			}
			return &demoTuner{netTuner: &netTuner{cfg: c, env: e, base: d.base()}, dev: d}, nil
		},
	})
}

// demoTuner is the network tuner driver, pointed at the demo device.
type demoTuner struct {
	*netTuner
	dev    *demoDev
	closed sync.Once
}

func (t *demoTuner) Info() Info {
	i := t.netTuner.Info()
	i.Driver = "demo"
	i.MaxStreams = t.dev.settings().tuners
	i.Detail = fmt.Sprintf("Demo tuner at %s, %d tuners", strings.TrimPrefix(t.dev.base(), "http://"), i.MaxStreams)
	i.GuideURL = t.dev.base() + "/guide.xml"
	return i
}

func (t *demoTuner) Close() error {
	t.closed.Do(t.dev.release)
	return nil
}

// demoSettings is what the owner chose.
type demoSettings struct {
	tuners    int
	clips     string
	slowTune  time.Duration
	dropEvery time.Duration
}

func readDemoSettings(o map[string]string) (demoSettings, error) {
	s := demoSettings{tuners: 2}
	if v := strings.TrimSpace(o["tuners"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 8 {
			return s, errors.New("the demo tuner has from 1 to 8 tuners")
		}
		s.tuners = n
	}
	if v := strings.TrimSpace(o["slowTune"]); v != "" && v != "0" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 30 {
			return s, errors.New("slow tuning is from 0 to 30 seconds")
		}
		s.slowTune = time.Duration(n) * time.Second
	}
	if v := strings.TrimSpace(o["dropEvery"]); v != "" && v != "0" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 600 {
			return s, errors.New("losing the signal is every 0 to 600 minutes")
		}
		s.dropEvery = time.Duration(n) * time.Minute
	}
	if v := strings.TrimSpace(o["clips"]); v != "" {
		if st, err := os.Stat(v); err != nil || !st.IsDir() {
			return s, errors.New("that folder of clips was not found on the server")
		}
		s.clips = v
	}
	return s, nil
}

// demoDev is the demo device: its HTTP server, its discovery answer and its streams.
type demoDev struct {
	port string
	srv  *http.Server
	udp  net.PacketConn
	env  Env

	mu      sync.Mutex
	set     demoSettings
	users   int
	playing int
	scan    struct {
		running bool
		started time.Time
	}
}

var (
	demoMu   sync.Mutex
	demoDevs = map[string]*demoDev{}
)

// demoDevice starts the demo device on its port, or finds it running, with these settings.
func demoDevice(o map[string]string) (*demoDev, error) {
	set, err := readDemoSettings(o)
	if err != nil {
		return nil, err
	}
	port := strings.TrimSpace(o["port"])
	if port == "" {
		port = DemoDefaultPort
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1024 || n > 65535 {
		return nil, errors.New("the demo tuner's port is a number from 1024 to 65535")
	}
	demoMu.Lock()
	defer demoMu.Unlock()
	if d := demoDevs[port]; d != nil {
		d.mu.Lock()
		d.set = set
		d.users++
		d.mu.Unlock()
		return d, nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return nil, fmt.Errorf("the demo tuner can't use port %s, which another program is using: choose another port", port)
	}
	d := &demoDev{port: port, set: set, users: 1, env: demoEnv}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /discover.json", d.handleDiscover)
	mux.HandleFunc("GET /lineup.json", d.handleLineup)
	mux.HandleFunc("GET /lineup_status.json", d.handleLineupStatus)
	mux.HandleFunc("POST /lineup.post", d.handleLineupPost)
	mux.HandleFunc("GET /auto/{ch}", d.handleStream)
	mux.HandleFunc("GET /guide.xml", d.handleGuide)
	d.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = d.srv.Serve(ln) }()
	// Discovery, on this machine only. Another program may hold the port, which only means the
	// demo tuner isn't found by searching: it is still there to add.
	if pc, err := net.ListenPacket("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(hdhrPort))); err == nil {
		d.udp = pc
		go d.answerDiscovery(pc)
	}
	demoDevs[port] = d
	return d, nil
}

// demoEnv is what the demo device runs FFmpeg with: the module's FFmpeg, set by SetDemoFFmpeg.
var demoEnv Env

// SetDemoFFmpeg says which FFmpeg the demo tuner makes its channels with.
func SetDemoFFmpeg(ffmpeg string, command func(ctx context.Context, name string, args ...string) *exec.Cmd) {
	demoMu.Lock()
	defer demoMu.Unlock()
	demoEnv.FFmpeg, demoEnv.Command = ffmpeg, command
	for _, d := range demoDevs {
		d.mu.Lock()
		d.env = demoEnv
		d.mu.Unlock()
	}
}

func (d *demoDev) base() string { return "http://127.0.0.1:" + d.port }

func (d *demoDev) settings() demoSettings {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.set
}

// release lets go of the device; the last one stops it.
func (d *demoDev) release() {
	demoMu.Lock()
	defer demoMu.Unlock()
	d.mu.Lock()
	d.users--
	last := d.users <= 0
	d.mu.Unlock()
	if !last {
		return
	}
	delete(demoDevs, d.port)
	_ = d.srv.Close()
	if d.udp != nil {
		_ = d.udp.Close()
	}
}

// answerDiscovery answers the network tuner discovery protocol's question, as a box does.
func (d *demoDev) answerDiscovery(pc net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 8 || binary.BigEndian.Uint16(buf) != hdhrDiscoverReq {
			continue
		}
		base := d.base()
		tags := []byte{hdhrTagType, 4, 0, 0, 0, hdhrTypeTuner, hdhrTagID, 4}
		tags = binary.BigEndian.AppendUint32(tags, demoDeviceID)
		tags = append(tags, hdhrTagTuners, 1, byte(d.settings().tuners))
		tags = append(tags, hdhrTagBaseURL, byte(len(base)))
		tags = append(tags, base...)
		_, _ = pc.WriteTo(hdhrPacket(hdhrDiscoverRes, tags), from)
	}
}

func demoJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (d *demoDev) handleDiscover(w http.ResponseWriter, _ *http.Request) {
	demoJSON(w, discoverJSON{FriendlyName: "Demo tuner", ModelNumber: DemoModel, DeviceID: fmt.Sprintf("%08X", demoDeviceID), BaseURL: d.base(),
		LineupURL: d.base() + "/lineup.json", TunerCount: d.settings().tuners, Firmware: "demo"})
}

// demoChannel is one of the demo tuner's channels.
type demoChannel struct {
	number, name string
	source       string // a lavfi source, or a clip's path
	clip         bool
	protected    bool
	topics       []string // what its guide shows
}

var demoPatterns = []demoChannel{
	{number: "1", name: "Demo News", source: "smptehdbars=size=640x360:rate=25", topics: []string{"Morning Headlines", "The Daily Briefing", "World Report", "Business Update", "Evening News", "Late Edition"}},
	{number: "2", name: "Demo Pattern", source: "testsrc2=size=640x360:rate=25", topics: []string{"Test Card Classics", "Signal Hour", "The Pattern Show", "Calibration Corner"}},
	{number: "3", name: "Demo Life", source: "life=size=640x360:rate=25:mold=10:ratio=0.1:life_color=#3cb371:death_color=#1e2a3a", topics: []string{"Nature Watch", "Life in Motion", "Cells at Play", "The Long Evolution"}},
	{number: "4", name: "Demo Colours", source: "testsrc=size=640x360:rate=25", topics: []string{"Colour Theory", "Rainbow Hour", "Spectrum", "Counting Down"}},
	{number: "9", name: "Demo Protected", protected: true, topics: []string{"A Protected Programme"}},
}

// maxDemoClips is how many clips become channels, as each one playing costs a decoder.
const maxDemoClips = 6

var clipKinds = []string{".mp4", ".mkv", ".webm", ".ogv", ".mov", ".m4v", ".ts", ".avi"}

func (d *demoDev) channels() []demoChannel {
	out := slices.Clone(demoPatterns)
	set := d.settings()
	if set.clips == "" {
		return out
	}
	entries, err := os.ReadDir(set.clips)
	if err != nil {
		return out
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !slices.Contains(clipKinds, strings.ToLower(filepath.Ext(e.Name()))) || n >= maxDemoClips {
			continue
		}
		n++
		title := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		out = append(out, demoChannel{number: strconv.Itoa(10 + n), name: "Demo Clips " + strconv.Itoa(n), source: filepath.Join(set.clips, e.Name()), clip: true,
			topics: []string{title}})
	}
	return out
}

func (d *demoDev) find(number string) (demoChannel, bool) {
	for _, c := range d.channels() {
		if c.number == number {
			return c, true
		}
	}
	return demoChannel{}, false
}

func (d *demoDev) handleLineup(w http.ResponseWriter, _ *http.Request) {
	var out []lineupEntry
	for _, c := range d.channels() {
		e := lineupEntry{GuideNumber: c.number, GuideName: c.name, URL: d.base() + "/auto/v" + c.number, VideoCodec: "H264", AudioCodec: "AAC"}
		if c.protected {
			e.Tags = "drm"
		}
		out = append(out, e)
	}
	demoJSON(w, out)
}

// A scan takes a few seconds and finds what the demo tuner always has, as a real one would.
const demoScanTime = 5 * time.Second

func (d *demoDev) handleLineupPost(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("scan") == "start" {
		d.mu.Lock()
		d.scan.running, d.scan.started = true, time.Now()
		d.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func (d *demoDev) handleLineupStatus(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	running, started := d.scan.running, d.scan.started
	if running && time.Since(started) >= demoScanTime {
		d.scan.running, running = false, false
	}
	d.mu.Unlock()
	st := map[string]any{"ScanInProgress": 0, "ScanPossible": 1, "Source": "Antenna", "SourceList": []string{"Antenna", "Cable"}}
	if running {
		p := int(100 * time.Since(started) / demoScanTime)
		st["ScanInProgress"], st["Progress"], st["Found"] = 1, p, len(d.channels())*p/100
	}
	demoJSON(w, st)
}

// fontFile is a font drawtext can use here, or "" for none: then the channels have no words.
var fontFile = sync.OnceValue(func() string {
	var try []string
	switch runtime.GOOS {
	case "windows":
		dir := os.Getenv("WINDIR")
		if dir == "" {
			dir = `C:\Windows`
		}
		try = []string{filepath.Join(dir, "Fonts", "arial.ttf"), filepath.Join(dir, "Fonts", "segoeui.ttf")}
	case "darwin":
		try = []string{"/System/Library/Fonts/Supplemental/Arial.ttf", "/Library/Fonts/Arial.ttf", "/System/Library/Fonts/Helvetica.ttc"}
	default:
		try = []string{"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/usr/share/fonts/dejavu/DejaVuSans.ttf", "/usr/share/fonts/TTF/DejaVuSans.ttf",
			"/usr/local/share/fonts/dejavu/DejaVuSans.ttf", "/usr/share/fonts/liberation/LiberationSans-Regular.ttf"}
	}
	for _, f := range try {
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			return f
		}
	}
	return ""
})

// filterText escapes words for drawtext, inside single quotes in a filter graph.
func filterText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\'' || r == '\\' || r == ':' || r == '%' || r == ',' || r == ';' || r == '[' || r == ']':
			continue
		case r < ' ':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func filterPath(p string) string {
	p = filepath.ToSlash(p)
	return strings.ReplaceAll(p, ":", `\:`)
}

// demoArgs is FFmpeg's command for a channel: picture, stereo sound, MPEG-TS on stdout.
func demoArgs(c demoChannel) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if c.clip {
		args = append(args, "-re", "-stream_loop", "-1", "-i", c.source, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
	} else {
		args = append(args, "-re", "-f", "lavfi", "-i", c.source, "-f", "lavfi", "-i", "sine=frequency=440:beep_factor=4:sample_rate=48000")
	}
	v := "[0:v]scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=25"
	if f := fontFile(); f != "" {
		font := "fontfile='" + filterPath(f) + "':fontsize=26:fontcolor=white:box=1:boxcolor=black@0.6:boxborderw=8"
		v += ",drawtext=" + font + ":text='" + filterText(c.number+" "+c.name) + "':x=24:y=20"
		v += ",drawtext=" + font + ":text='DEMO':x=w-tw-24:y=20"
		v += ",drawtext=" + font + ":text='%{localtime\\:%X}':x=w-tw-24:y=h-th-20"
	}
	args = append(args, "-filter_complex", v+"[v]", "-map", "[v]")
	if c.clip {
		// The clip's own sound when it has some, else silence.
		args = append(args, "-map", "0:a:0?", "-map", "1:a", "-shortest")
	} else {
		args = append(args, "-map", "1:a")
	}
	args = append(args, "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p", "-g", "50", "-b:v", "900k",
		"-c:a", "aac", "-b:a", "96k",
		// A broadcast's clock runs on whoever tunes in: the time of day, so a stream opened again
		// carries on from where it was, as a recording that rejoins a channel expects.
		"-output_ts_offset", strconv.FormatFloat(demoClock(), 'f', 3, 64), "-f", "mpegts", "-mpegts_service_id", "1", "-metadata", "service_name="+c.name, "-metadata", "service_provider=WittyTuner demo", "pipe:1")
	return args
}

func (d *demoDev) handleStream(w http.ResponseWriter, r *http.Request) {
	number := strings.TrimPrefix(r.PathValue("ch"), "v")
	c, ok := d.find(number)
	if !ok {
		w.Header().Set("X-HDHomeRun-Error", "801 Unknown Channel")
		http.Error(w, "no such channel", http.StatusNotFound)
		return
	}
	if c.protected {
		// A real box says this for a protected channel. Nothing is sent: the demo has nothing to protect.
		w.Header().Set("X-HDHomeRun-Error", "811 Content Protection Required")
		http.Error(w, "protected", http.StatusForbidden)
		return
	}
	set := d.settings()
	d.mu.Lock()
	env := d.env
	if d.playing >= set.tuners {
		d.mu.Unlock()
		w.Header().Set("X-HDHomeRun-Error", "805 All Tuners In Use")
		http.Error(w, "all tuners in use", http.StatusServiceUnavailable)
		return
	}
	d.playing++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.playing--
		d.mu.Unlock()
	}()
	if env.FFmpeg == "" {
		http.Error(w, "FFmpeg isn't known yet", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	if set.slowTune > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(set.slowTune):
		}
	}
	if set.dropEvery > 0 {
		// Lose the signal after about that long: somewhere from three quarters of it to all of it.
		after := set.dropEvery*3/4 + time.Duration(rand.Int64N(int64(set.dropEvery/4)+1))
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, after)
		defer cancel()
	}
	cmd := env.command(ctx, env.FFmpeg, demoArgs(c)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	stderr := &stderrTail{}
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		http.Error(w, "FFmpeg could not start", 500)
		return
	}
	defer func() {
		_ = cmd.Wait()
	}()
	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 188*64)
	for {
		n, err := out.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				env.logger().Warn("a demo channel stopped", "topic", "tuner", "channel", c.name, "stderr", stderr.String())
			}
			return
		}
	}
}

// The demo guide: programmes from yesterday to three days ahead, in half hours and hours, the same
// every time for the same day.
type xmltvDoc struct {
	XMLName    xml.Name         `xml:"tv"`
	Generator  string           `xml:"generator-info-name,attr"`
	Channels   []xmltvChannel   `xml:"channel"`
	Programmes []xmltvProgramme `xml:"programme"`
}

type xmltvChannel struct {
	ID    string   `xml:"id,attr"`
	Names []string `xml:"display-name"`
}

type xmltvProgramme struct {
	Start    string `xml:"start,attr"`
	Stop     string `xml:"stop,attr"`
	Channel  string `xml:"channel,attr"`
	Title    string `xml:"title"`
	Desc     string `xml:"desc"`
	Category string `xml:"category"`
}

// DemoGuideID is a demo channel's id in its guide.
func DemoGuideID(number string) string { return "demo" + number + ".wittytuner" }

func (d *demoDev) handleGuide(w http.ResponseWriter, _ *http.Request) {
	doc := xmltvDoc{Generator: "WittyTuner demo tuner"}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	const layout = "20060102150405 -0700"
	for _, c := range d.channels() {
		id := DemoGuideID(c.number)
		doc.Channels = append(doc.Channels, xmltvChannel{ID: id, Names: []string{c.name, c.number}})
		slot := 30 * time.Minute
		if c.clip {
			slot = time.Hour
		}
		cat := "News"
		switch {
		case c.clip:
			cat = "Documentary"
		case c.number == "2" || c.number == "4":
			cat = "Educational"
		case c.number == "3":
			cat = "Nature"
		}
		for t, i := day, 0; t.Before(day.AddDate(0, 0, 4)); t, i = t.Add(slot), i+1 {
			title := c.topics[i%len(c.topics)]
			if c.number == "1" {
				title = c.topics[(t.Hour()/4)%len(c.topics)]
			}
			doc.Programmes = append(doc.Programmes, xmltvProgramme{Start: t.Format(layout), Stop: t.Add(slot).Format(layout), Channel: id,
				Title: title, Desc: "A demo programme on the demo tuner: made on this server, for trying live TV. Nothing here is real TV.", Category: cat})
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	_ = enc.Encode(doc)
}

// Channels names each demo channel's guide id, so the guide fills without matching.
func (t *demoTuner) Channels(ctx context.Context) ([]Channel, error) {
	list, err := t.netTuner.Channels(ctx)
	for i := range list {
		list[i].EPGID = DemoGuideID(list[i].Number)
	}
	return list, err
}

func (t *demoTuner) Scan(ctx context.Context, o ScanOptions, p func(Progress)) ([]Channel, error) {
	list, err := t.netTuner.Scan(ctx, o, p)
	for i := range list {
		list[i].EPGID = DemoGuideID(list[i].Number)
	}
	return list, err
}

// demoClock is the seconds since midnight, as the demo channels' clock.
func demoClock() float64 {
	t := time.Now()
	return t.Sub(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())).Seconds()
}
