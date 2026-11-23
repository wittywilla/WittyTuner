// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Network tuners: boxes on the home network with an HTTP interface, in the
// style of HDHomeRun devices. The interface is documented by the maker:
// discover.json, lineup.json, lineup_status.json, lineup.post and MPEG-TS over
// HTTP. Devices are found by the UDP discovery protocol on port 65001 and, for
// devices that emulate one, SSDP.

func init() {
	register(Driver{
		ID: "network", Name: "Network tuner (HDHomeRun-style)", Status: "mock",
		Description: "Antenna or cable boxes on your network that stream over HTTP. Usually found by themselves.",
		Scans:       true,
		Fields: []Field{
			{Key: "address", Label: "Address", Kind: "text", Required: true, Help: "The device's address, such as 192.168.1.50 or hdhomerun.local. The HDHomeRun app or your router's list of devices shows it."},
			{Key: "source", Label: "Signal", Kind: "select", Options: []string{"Antenna", "Cable"}, Default: "Antenna", Help: "Which signal a channel scan looks at."},
		},
		build: func(c Config, e Env) (Source, error) {
			base, err := deviceBase(c.Options["address"])
			if err != nil {
				return nil, err
			}
			return &netTuner{cfg: c, env: e, base: base}, nil
		},
	})
}

// deviceBase turns what someone typed into http://host[:port].
func deviceBase(addr string) (string, error) {
	addr = strings.TrimSuffix(strings.TrimSpace(addr), "/")
	if addr == "" {
		return "", errors.New("fill in the device's address")
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("that is not a device address")
	}
	return u.Scheme + "://" + u.Host, nil
}

type netTuner struct {
	cfg  Config
	env  Env
	base string
	mu   sync.Mutex // holds dev, which streams opened together may both fill in
	dev  discoverJSON
}

type discoverJSON struct {
	FriendlyName string `json:"FriendlyName"`
	ModelNumber  string `json:"ModelNumber"`
	DeviceID     string `json:"DeviceID"`
	BaseURL      string `json:"BaseURL"`
	LineupURL    string `json:"LineupURL"`
	TunerCount   int    `json:"TunerCount"`
	Firmware     string `json:"FirmwareVersion"`
	Legacy       int    `json:"Legacy"`
}

type lineupEntry struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	Tags        string `json:"Tags"` // comma-separated: "drm" is copy protection
	URL         string `json:"URL"`
	// Not in the maker's published guide, but sent by devices in use.
	VideoCodec string `json:"VideoCodec"`
	AudioCodec string `json:"AudioCodec"`
	HD         int    `json:"HD"`
	DRM        int    `json:"DRM"`
	Favorite   int    `json:"Favorite"`
}

// protected: the published marking is a tag, and some firmware sends a field.
func (l lineupEntry) protected() bool {
	if l.DRM != 0 {
		return true
	}
	for _, t := range strings.Split(l.Tags, ",") {
		if strings.EqualFold(strings.TrimSpace(t), "drm") {
			return true
		}
	}
	return false
}

// atsc3: these devices name the codecs of a channel, and only ATSC 3.0 uses these.
func (l lineupEntry) atsc3() bool {
	return strings.EqualFold(l.VideoCodec, "hevc") || strings.EqualFold(l.AudioCodec, "ac4") || strings.EqualFold(l.AudioCodec, "ac-4")
}

func getJSON(ctx context.Context, c *http.Client, addr string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", addr, nil)
	if err != nil {
		return err
	}
	res, err := c.Do(req)
	if err != nil {
		return withoutAddress(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("the device answered %s", res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}

func (t *netTuner) Info() Info {
	t.mu.Lock()
	defer t.mu.Unlock()
	i := Info{Driver: "network", Name: t.cfg.Name, MaxStreams: t.dev.TunerCount, Detail: t.base}
	if t.dev.ModelNumber != "" {
		i.Detail = strings.TrimSpace(t.dev.FriendlyName + " " + t.dev.ModelNumber + " at " + t.base)
	}
	return i
}

func (t *netTuner) discover(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dev.DeviceID != "" || t.dev.ModelNumber != "" {
		return nil
	}
	var d discoverJSON
	if err := getJSON(ctx, t.env.http(), t.base+"/discover.json", &d); err != nil {
		return fmt.Errorf("no tuner answered at %s: %w", t.base, err)
	}
	t.dev = d
	t.env.logger().InfoContext(ctx, "network tuner", "topic", "tuner", "model", d.ModelNumber, "firmware", d.Firmware, "tuners", d.TunerCount, "legacy", d.Legacy)
	return nil
}

func (t *netTuner) Channels(ctx context.Context) ([]Channel, error) {
	if err := t.discover(ctx); err != nil {
		return nil, err
	}
	t.mu.Lock()
	legacy, model := t.dev.Legacy != 0, t.dev.ModelNumber
	t.mu.Unlock()
	if legacy {
		return nil, fmt.Errorf("%s is an older model that its maker calls legacy: it has no channel list or streams over HTTP, which is how WittyTuner uses network tuners, so it can't be used here yet", firstNonEmpty(model, "this device"))
	}
	var lineup []lineupEntry
	if err := getJSON(ctx, t.env.http(), t.base+"/lineup.json", &lineup); err != nil {
		return nil, fmt.Errorf("the channel list could not be read: %w", err)
	}
	out := make([]Channel, 0, len(lineup))
	for _, l := range lineup {
		if l.GuideNumber == "" {
			continue
		}
		c := Channel{ID: l.GuideNumber, Number: l.GuideNumber, Name: firstNonEmpty(l.GuideName, l.GuideNumber), Locator: l.URL, HD: l.HD != 0,
			Checked: true, ATSC3: l.atsc3(), VideoCodec: l.VideoCodec, AudioCodec: l.AudioCodec}
		if l.protected() {
			c.Encrypted = "the device marks it as copy protected"
		}
		out = append(out, c)
	}
	return out, nil
}

// Scan asks the device to look for channels, and follows its progress.
func (t *netTuner) Scan(ctx context.Context, o ScanOptions, progress func(Progress)) ([]Channel, error) {
	if err := t.discover(ctx); err != nil {
		return nil, err
	}
	source := firstNonEmpty(o.Source, t.cfg.Options["source"], "Antenna")
	if source != "Antenna" && source != "Cable" {
		return nil, errors.New("the signal is Antenna or Cable")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.base+"/lineup.post?scan=start&source="+source, nil)
	if err != nil {
		return nil, err
	}
	res, err := t.env.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("the scan could not start: %w", withoutAddress(err))
	}
	res.Body.Close()
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("the device refused the scan: %s (it may be busy)", res.Status)
	}
	tick := time.NewTicker(scanPoll)
	defer tick.Stop()
	for {
		var st struct {
			ScanInProgress int `json:"ScanInProgress"`
			Progress       int `json:"Progress"`
			Found          int `json:"Found"`
		}
		if err := getJSON(ctx, t.env.http(), t.base+"/lineup_status.json", &st); err != nil {
			return nil, err
		}
		if progress != nil && st.ScanInProgress != 0 {
			progress(Progress{Percent: st.Progress, Found: st.Found})
		}
		if st.ScanInProgress == 0 {
			// A device says nothing of progress once it's done: it's done.
			ch, err := t.Channels(ctx)
			if err == nil && progress != nil {
				progress(Progress{Percent: 100, Found: len(ch)})
			}
			return ch, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

var scanPoll = 2 * time.Second

func (t *netTuner) Open(ctx context.Context, ch Channel) (*Stream, error) {
	if ch.Encrypted != "" {
		return nil, &EncryptedError{ch.Name, ch.Encrypted}
	}
	addr, err := t.streamURL(ctx, ch)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", addr, nil)
	if err != nil {
		return nil, err
	}
	res, err := t.env.stream().Do(req)
	if err != nil {
		return nil, fmt.Errorf("the tuner could not be reached: %w", withoutAddress(err))
	}
	if res.StatusCode != 200 {
		defer res.Body.Close()
		// The device explains itself in a header, as its maker's guide lists.
		code := res.Header.Get("X-HDHomeRun-Error")
		num, _, _ := strings.Cut(code, " ")
		// What the device said goes in the log as it said it: these lines are what a report needs.
		t.mu.Lock()
		model, firmware := t.dev.ModelNumber, t.dev.Firmware
		t.mu.Unlock()
		t.env.logger().InfoContext(ctx, "the network tuner refused a channel", "topic", "tuner", "channel", ch.Number,
			"status", res.Status, "deviceError", code, "model", model, "firmware", firmware)
		switch num {
		case "811":
			return nil, &EncryptedError{ch.Name, "the device says it needs copy protection"}
		case "803", "804", "805", "809":
			return nil, ErrBusy
		case "801":
			return nil, errors.New("the device no longer has this channel: scan again")
		case "806":
			return nil, errors.New("the tuner could not tune this channel: the signal may be too weak")
		case "807":
			return nil, errors.New("the tuner found no picture on this channel: the signal may be too weak")
		}
		if res.StatusCode == 503 {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("the tuner answered %s %s", res.Status, code)
	}
	return guarded(res.Body), nil
}

// streamURL uses the address from the lineup, but always on the device's own host, the one it
// was added by: a lineup is data from the network, not a reason to go elsewhere. A device may name
// itself in its lineup differently from how it was added (current firmware writes
// http://hdhr-<id>.local:5004/..., older firmware its IP address, and either can be added by the
// other): that's the same device, so its port and path are kept and the host is the known one.
// A lineup naming some other machine is refused.
func (t *netTuner) streamURL(ctx context.Context, ch Channel) (string, error) {
	base, _ := url.Parse(t.base)
	u, err := url.Parse(ch.Locator)
	if err != nil || u.Host == "" {
		return "http://" + net.JoinHostPort(base.Hostname(), "5004") + "/auto/v" + url.PathEscape(ch.Number), nil
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
		return "", errors.New("the device's channel list points at another address")
	}
	if !strings.EqualFold(u.Hostname(), base.Hostname()) {
		if t.discover(ctx) != nil || !t.ownName(u.Hostname()) {
			return "", errors.New("the device's channel list points at another address")
		}
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		u.Host = net.JoinHostPort(base.Hostname(), port)
	}
	return u.String(), nil
}

// ownName: a name the device gives itself, in its discover.json (BaseURL, LineupURL) or by the
// maker's naming (hdhr-<device id>.local, <device id>.local, hdhomerun.local).
func (t *netTuner) ownName(host string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	for _, a := range []string{t.dev.BaseURL, t.dev.LineupURL} {
		if u, err := url.Parse(a); err == nil && u.Hostname() != "" && strings.EqualFold(u.Hostname(), host) {
			return true
		}
	}
	id := strings.ToLower(strings.TrimSpace(t.dev.DeviceID))
	return host == "hdhomerun.local" || (id != "" && (host == "hdhr-"+id+".local" || host == id+".local"))
}

func (t *netTuner) Close() error { return nil }

// --------------------------------------------------------------------------
// Discovery
// --------------------------------------------------------------------------

const (
	hdhrPort        = 65001
	hdhrDiscoverReq = 0x0002
	hdhrDiscoverRes = 0x0003
	hdhrTagType     = 0x01
	hdhrTagID       = 0x02
	hdhrTagTuners   = 0x10
	hdhrTagBaseURL  = 0x2a
	hdhrTypeTuner   = 0x00000001
	hdhrWildcard    = 0xffffffff
)

// hdhrPacket frames a discovery packet: type, length, tags, little-endian CRC.
func hdhrPacket(typ uint16, tags []byte) []byte {
	b := make([]byte, 4, 8+len(tags))
	binary.BigEndian.PutUint16(b, typ)
	binary.BigEndian.PutUint16(b[2:], uint16(len(tags)))
	b = append(b, tags...)
	return binary.LittleEndian.AppendUint32(b, crc32.ChecksumIEEE(b))
}

func hdhrDiscoverRequest() []byte {
	tags := []byte{hdhrTagType, 4, 0, 0, 0, hdhrTypeTuner, hdhrTagID, 4, 0xff, 0xff, 0xff, 0xff}
	return hdhrPacket(hdhrDiscoverReq, tags)
}

type hdhrReply struct {
	id      uint32
	tuners  int
	baseURL string
}

func parseHDHRReply(b []byte) (r hdhrReply, ok bool) {
	if len(b) < 8 {
		return r, false
	}
	if binary.BigEndian.Uint16(b) != hdhrDiscoverRes {
		return r, false
	}
	n := int(binary.BigEndian.Uint16(b[2:]))
	if 4+n+4 > len(b) || binary.LittleEndian.Uint32(b[4+n:]) != crc32.ChecksumIEEE(b[:4+n]) {
		return r, false
	}
	tags := b[4 : 4+n]
	for len(tags) >= 2 {
		tag := tags[0]
		l := int(tags[1])
		i := 2
		if l&0x80 != 0 {
			if len(tags) < 3 {
				return r, false
			}
			l = l&0x7f | int(tags[2])<<7
			i = 3
		}
		if i+l > len(tags) {
			return r, false
		}
		v := tags[i : i+l]
		switch tag {
		case hdhrTagID:
			if l == 4 {
				r.id = binary.BigEndian.Uint32(v)
			}
		case hdhrTagTuners:
			if l == 1 {
				r.tuners = int(v[0])
			}
		case hdhrTagBaseURL:
			r.baseURL = string(v)
		}
		tags = tags[i+l:]
	}
	return r, r.id != 0
}

// lanHost keeps discovery to the local network: a reply cannot send the
// server off to the internet, or to where a cloud host answers about itself.
func lanHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) && reachable(ip, true)
}

// discoveredAt reads the address in an answer to a search. Anyone on the network can answer, and
// name anything, so it must be the machine that answered, by its address: otherwise a device could
// send this server off to fetch addresses elsewhere.
func discoveredAt(loc string, from net.Addr) (*url.URL, bool) {
	u, err := url.Parse(loc)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return nil, false
	}
	host, err := netip.ParseAddr(u.Hostname())
	if err != nil {
		return nil, false
	}
	sender, ok := from.(*net.UDPAddr)
	if !ok {
		return nil, false
	}
	addr, ok := netip.AddrFromSlice(sender.IP)
	if !ok || host.Unmap().WithZone("") != addr.Unmap().WithZone("") || !lanHost(host.WithZone("").String()) {
		return nil, false
	}
	return u, true
}

// noRedirect keeps a device from sending the question on: it may name itself and nothing else.
func noRedirect(c *http.Client) *http.Client {
	out := *c
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

func discoverDevice(ctx context.Context, env Env, host string) (discoverJSON, error) {
	var d discoverJSON
	err := getJSON(ctx, noRedirect(env.http()), "http://"+host+"/discover.json", &d)
	return d, err
}

// Where discovery asks: the maker's port, on every broadcast address and this machine. Tests point
// them at a device of their own.
var (
	hdhrDiscoverPort    = hdhrPort
	hdhrDiscoverTargets = func() []net.IP { return append(broadcastAddrs(), net.IPv4(127, 0, 0, 1)) }
	// hdhrNames are names the maker's devices answer to by multicast DNS (info.hdhomerun.com,
	// http_api): asked by name, the question goes through the system's own resolver, which a
	// firewall that stops the UDP search's answers lets through.
	hdhrNames = []string{"hdhomerun.local"}
	// discoverListen is where the search's own socket listens. Tests keep it on this machine, so
	// Windows' firewall never stops to ask about a test program.
	discoverListen = ":0"
)

func discoverNetworkTuners(ctx context.Context, env Env) []Found {
	var mu sync.Mutex
	hosts := map[string]bool{}
	add := func(h string) {
		mu.Lock()
		hosts[h] = true
		mu.Unlock()
	}
	var wg sync.WaitGroup
	// The maker's UDP protocol.
	wg.Go(func() {
		conn, err := net.ListenPacket("udp4", discoverListen)
		if err != nil {
			env.logger().InfoContext(ctx, "the network tuner search couldn't start", "topic", "tuner", "err", err)
			return
		}
		defer conn.Close()
		req := hdhrDiscoverRequest()
		// This machine too: a tuner emulated here (the demo tuner) doesn't hear a broadcast.
		for _, dst := range hdhrDiscoverTargets() {
			_, _ = conn.WriteTo(req, &net.UDPAddr{IP: dst, Port: hdhrDiscoverPort})
		}
		_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				break
			}
			if rep, ok := parseHDHRReply(buf[:n]); ok {
				if u, ok := from.(*net.UDPAddr); ok && lanHost(u.IP.String()) {
					add(replyHost(rep, u))
				}
			}
		}
	})
	// SSDP, which devices that emulate one answer.
	wg.Go(func() {
		for _, loc := range ssdpSearch(ctx, "urn:schemas-upnp-org:device:MediaServer:1", 1500*time.Millisecond) {
			if u, err := url.Parse(loc); err == nil {
				add(u.Host)
			}
		}
	})
	// The device's own name.
	for _, name := range hdhrNames {
		wg.Go(func() {
			c, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			ips, err := net.DefaultResolver.LookupIP(c, "ip4", name)
			if err != nil {
				return
			}
			for _, ip := range ips {
				if lanHost(ip.String()) {
					add(ip.String())
				}
			}
		})
	}
	wg.Wait()
	var out []Found
	seen := map[string]bool{} // one device found two ways is one device
	for _, h := range slices.Sorted(maps.Keys(hosts)) {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		d, err := discoverDevice(c, env, h)
		cancel()
		if err != nil || (d.DeviceID == "" && d.ModelNumber == "") {
			if err != nil {
				env.logger().InfoContext(ctx, "a network tuner answered the search, but not its discover.json", "topic", "tuner", "err", withoutAddress(err))
			}
			continue
		}
		if d.DeviceID != "" {
			if seen[d.DeviceID] {
				continue
			}
			seen[d.DeviceID] = true
		}
		if d.ModelNumber == DemoModel {
			if _, port, err := net.SplitHostPort(h); err == nil {
				out = append(out, Found{Driver: "demo", Name: "Demo tuner (built in)", Detail: fmt.Sprintf("Pretend channels made on this server, %d tuners", d.TunerCount),
					Options: map[string]string{"port": port}})
			}
			continue
		}
		env.logger().InfoContext(ctx, "network tuner found", "topic", "tuner", "model", d.ModelNumber, "firmware", d.Firmware, "tuners", d.TunerCount, "at", h)
		out = append(out, Found{Driver: "network", Name: firstNonEmpty(d.FriendlyName, "Network tuner"),
			Detail: fmt.Sprintf("%s, %d tuners, %s", d.ModelNumber, d.TunerCount, h), Options: map[string]string{"address": h}})
	}
	return out
}

// replyHost is where to ask a device that answered discovery: its own address, with the port its
// base URL names when that URL is the same machine (a device emulated on another port). An answer
// can't send the question anywhere else.
func replyHost(r hdhrReply, from *net.UDPAddr) string {
	ip := from.IP.String()
	if r.baseURL == "" {
		return ip
	}
	u, err := url.Parse(r.baseURL)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return ip
	}
	host, err := netip.ParseAddr(u.Hostname())
	sender, ok := netip.AddrFromSlice(from.IP)
	if err != nil || !ok || host.Unmap() != sender.Unmap() {
		return ip
	}
	if p := u.Port(); p != "" && p != "80" {
		return net.JoinHostPort(ip, p)
	}
	return ip
}

func broadcastAddrs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip := n.IP.To4()
			b := make(net.IP, 4)
			for k := range b {
				b[k] = ip[k] | ^n.Mask[len(n.Mask)-4+k]
			}
			out = append(out, b)
		}
	}
	return out
}

// ssdpSearch multicasts an M-SEARCH and collects the LOCATION of each answer that names
// the machine that answered.
func ssdpSearch(ctx context.Context, st string, wait time.Duration) []string {
	conn, err := net.ListenPacket("udp4", discoverListen)
	if err != nil {
		return nil
	}
	defer conn.Close()
	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: " + st + "\r\nUSER-AGENT: WittyTuner/1 UPnP/1.1\r\n\r\n"
	dst := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	_, _ = conn.WriteTo([]byte(msg), dst)
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	seen := map[string]bool{}
	var out []string
	buf := make([]byte, 4096)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return out
		}
		if loc := ssdpLocation(buf[:n]); loc != "" && !seen[loc] {
			if _, ok := discoveredAt(loc, from); !ok {
				continue
			}
			seen[loc] = true
			out = append(out, loc)
		}
	}
}

func ssdpLocation(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "location") {
			v = strings.TrimSpace(v)
			if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
				return v
			}
		}
	}
	return ""
}
