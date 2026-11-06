package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- suggestions under a message ----
//
// Without any AI, Chat reads a message for a plan (a day and maybe a time),
// a cost (an amount with a currency) and a request ("can you…", "kupi…").
// Members see quiet suggestions under it: Add to calendar, Split, Make a
// task. The words are English and Bosnian/Croatian/Serbian. Pocket AI can
// sharpen the details when the person opens the suggestion.

type hint struct {
	Kind     string `json:"kind"` // event | expense | task
	Date     string `json:"date,omitempty"`
	Time     string `json:"time,omitempty"`
	Label    string `json:"label,omitempty"`
	Amount   string `json:"amount,omitempty"` // "40.00"
	Minor    int64  `json:"minor,omitempty"`
	Currency string `json:"currency,omitempty"`
	Title    string `json:"title,omitempty"`
}

var weekdays = map[string]time.Weekday{
	"monday": time.Monday, "mon": time.Monday, "ponedjeljak": time.Monday, "ponedeljak": time.Monday, "ponedjeljka": time.Monday, "ponedeljka": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday, "utorak": time.Tuesday, "utorka": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday, "srijeda": time.Wednesday, "srijedu": time.Wednesday, "sreda": time.Wednesday, "sredu": time.Wednesday, "srijede": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "thurs": time.Thursday, "cetvrtak": time.Thursday, "cetvrtka": time.Thursday,
	"friday": time.Friday, "fri": time.Friday, "petak": time.Friday, "petka": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday, "subota": time.Saturday, "subotu": time.Saturday, "subote": time.Saturday,
	"sunday": time.Sunday, "sun": time.Sunday, "nedjelja": time.Sunday, "nedjelju": time.Sunday, "nedelja": time.Sunday, "nedelju": time.Sunday, "nedjelje": time.Sunday,
}

var months = map[string]time.Month{
	"january": 1, "jan": 1, "januar": 1, "januara": 1, "february": 2, "feb": 2, "februar": 2, "februara": 2,
	"march": 3, "mar": 3, "mart": 3, "marta": 3, "april": 4, "apr": 4, "aprila": 4, "may": 5, "maj": 5, "maja": 5,
	"june": 6, "jun": 6, "juni": 6, "juna": 6, "july": 7, "jul": 7, "juli": 7, "jula": 7, "august": 8, "aug": 8, "avgust": 8, "august_": 8, "augusta": 8, "avgusta": 8,
	"september": 9, "sep": 9, "sept": 9, "septembar": 9, "septembra": 9, "october": 10, "oct": 10, "oktobar": 10, "oktobra": 10,
	"november": 11, "nov": 11, "novembar": 11, "novembra": 11, "december": 12, "dec": 12, "decembar": 12, "decembra": 12,
}

var (
	reWord      = regexp.MustCompile(`[\p{L}]+`)
	reNumDate   = regexp.MustCompile(`\b(\d{1,2})[./](\d{1,2})(?:[./](\d{2,4}))?\.?(?:\s|$|[,!?)])`)
	reDayMonth  = regexp.MustCompile(`\b(\d{1,2})(?:st|nd|rd|th|\.)?\s+(?:of\s+)?([a-z]+)\b`)
	reMonthDay  = regexp.MustCompile(`\b([a-z]+)\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
	reTime24    = regexp.MustCompile(`\b([01]?\d|2[0-3])[:.]([0-5]\d)\s*(am|pm|h)?\b`)
	reTimeAmPm  = regexp.MustCompile(`\b(1[0-2]|0?[1-9])\s*(am|pm)\b`)
	reTimeH     = regexp.MustCompile(`\b(?:u|at|oko|around)\s+([01]?\d|2[0-3])\s*(?:h|sati|casova|uhr|o'?clock)?\b`)
	reTimeBareH = regexp.MustCompile(`\b([01]?\d|2[0-3])h\b`)
	reMoney     = regexp.MustCompile(`(?i)(?:(€|\$|£|eur|usd|gbp|chf|bam|km|kn|rsd|din)\s?(\d{1,6}(?:[.,]\d{1,2})?)|(\d{1,6}(?:[.,]\d{1,2})?)\s?(€|\$|£|eura?|euros?|eur|usd|dollars?|gbp|pounds?|chf|bam|km|maraka|marke|kn|kuna|rsd|din|dinara))\b?`)
	reURL       = regexp.MustCompile(`https?://[^\s<>"'()]+`)
)

var taskStarts = []string{"can you", "could you", "would you", "please ", "pls ", "don't forget", "dont forget", "remember to", "we need", "need to", "pick up", "buy ", "get some", "remind me",
	"mozes li", "mozete li", "hoces li", "molim te", "ne zaboravi", "nemoj zaboraviti", "treba ", "trebamo", "trebas", "kupi ", "kupite", "uzmi ", "pokupi", "podsjeti", "podseti", "javi "}

var currencyOf = map[string]string{"€": "EUR", "eur": "EUR", "euro": "EUR", "euros": "EUR", "eura": "EUR", "$": "USD", "usd": "USD", "dollar": "USD", "dollars": "USD",
	"£": "GBP", "gbp": "GBP", "pound": "GBP", "pounds": "GBP", "chf": "CHF", "bam": "BAM", "km": "BAM", "maraka": "BAM", "marke": "BAM", "kn": "HRK", "kuna": "HRK",
	"rsd": "RSD", "din": "RSD", "dinara": "RSD"}

// detectHints reads a message for a plan, a cost and a request.
func detectHints(text string, now time.Time) []hint {
	folded := store.Fold(text)
	var out []hint
	date, label := findDate(folded, now)
	tm := findTime(folded)
	if date == "" && tm != "" && planWords(folded) {
		// "dinner at 7?" — today, or tomorrow when that hour is gone
		d := now
		if h, _ := strconv.Atoi(tm[:2]); h < now.Hour() {
			d = d.AddDate(0, 0, 1)
		}
		date, label = d.Format("2006-01-02"), dayLabel(d, now)
	}
	if date != "" {
		h := hint{Kind: "event", Date: date, Time: tm, Label: label}
		if tm != "" {
			h.Label += ", " + tm
		}
		out = append(out, h)
	}
	if m := findMoney(text); m != nil {
		out = append(out, *m)
	}
	trimmed := strings.TrimSpace(folded)
	for _, p := range taskStarts {
		if strings.HasPrefix(trimmed, p) || strings.Contains(folded, " "+strings.TrimSpace(p)+" ") && len(trimmed) < 200 {
			out = append(out, hint{Kind: "task", Title: taskTitle(text)})
			break
		}
	}
	return out
}

func planWords(f string) bool {
	for _, w := range []string{"meet", "dinner", "lunch", "breakfast", "coffee", "party", "call", "pick", "game", "movie", "appointment", "meeting",
		"vecera", "veceru", "rucak", "rucku", "dorucak", "kafa", "kafu", "kava", "kavu", "nadjemo", "vidimo", "sastanak", "termin", "dodji", "dolazim", "zurka", "rodjendan", "utakmica"} {
		if strings.Contains(f, w) {
			return true
		}
	}
	return false
}

func dayLabel(d, now time.Time) string {
	today := now.Format("2006-01-02")
	switch d.Format("2006-01-02") {
	case today:
		return "Today"
	case now.AddDate(0, 0, 1).Format("2006-01-02"):
		return "Tomorrow"
	}
	if d.Year() != now.Year() {
		return d.Format("Mon 2 Jan 2006")
	}
	return d.Format("Mon 2 Jan")
}

func findDate(f string, now time.Time) (string, string) {
	set := func(d time.Time) (string, string) { return d.Format("2006-01-02"), dayLabel(d, now) }
	switch {
	case strings.Contains(f, "day after tomorrow") || strings.Contains(f, "prekosutra"):
		return set(now.AddDate(0, 0, 2))
	case strings.Contains(f, "tomorrow") || regexp.MustCompile(`\bsutra\b`).MatchString(f):
		return set(now.AddDate(0, 0, 1))
	case strings.Contains(f, "tonight") || strings.Contains(f, "today") || strings.Contains(f, "veceras") || regexp.MustCompile(`\bdanas\b`).MatchString(f):
		return set(now)
	}
	if m := reNumDate.FindStringSubmatch(f + " "); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y := now.Year()
		if m[3] != "" {
			y, _ = strconv.Atoi(m[3])
			if y < 100 {
				y += 2000
			}
		}
		if d >= 1 && d <= 31 && mo >= 1 && mo <= 12 && (m[3] != "" || strings.Contains(m[0], ".") || strings.Contains(m[0], "/")) {
			t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, now.Location())
			if m[3] == "" && t.Before(now.AddDate(0, 0, -1)) {
				t = t.AddDate(1, 0, 0)
			}
			if t.Day() == d {
				return set(t)
			}
		}
	}
	for _, re := range []*regexp.Regexp{reDayMonth, reMonthDay} {
		for _, m := range re.FindAllStringSubmatch(f, -1) {
			ds, ms := m[1], m[2]
			if re == reMonthDay {
				ds, ms = m[2], m[1]
			}
			mo, ok := months[ms]
			if !ok {
				continue
			}
			d, _ := strconv.Atoi(ds)
			if d < 1 || d > 31 {
				continue
			}
			t := time.Date(now.Year(), mo, d, 0, 0, 0, 0, now.Location())
			if t.Before(now.AddDate(0, 0, -1)) {
				t = t.AddDate(1, 0, 0)
			}
			if t.Day() == d {
				return set(t)
			}
		}
	}
	words := reWord.FindAllString(f, -1)
	for i, w := range words {
		wd, ok := weekdays[w]
		if !ok || (len(w) <= 3 && i > 0 && words[i-1] != "on" && words[i-1] != "next") {
			continue
		}
		if len(w) <= 3 && i == 0 {
			continue
		}
		diff := (int(wd) - int(now.Weekday()) + 7) % 7
		if diff == 0 && !(i > 0 && (words[i-1] == "this" || words[i-1] == "ovaj" || words[i-1] == "ovu")) {
			diff = 7
		}
		if i > 0 && (words[i-1] == "next" || words[i-1] == "sljedeci" || words[i-1] == "sledeci" || words[i-1] == "iduci" || words[i-1] == "sljedecu" || words[i-1] == "iducu") && diff < 7 && diff <= 2 {
			diff += 7
		}
		return set(now.AddDate(0, 0, diff))
	}
	return "", ""
}

func findTime(f string) string {
	f = reNumDate.ReplaceAllString(f+" ", " ") // "15.11." is a date, not 15:11
	if m := reTime24.FindStringSubmatch(f); m != nil {
		h, _ := strconv.Atoi(m[1])
		if m[3] == "pm" && h < 12 {
			h += 12
		}
		if m[3] == "am" && h == 12 {
			h = 0
		}
		return fmt.Sprintf("%02d:%s", h, m[2])
	}
	if m := reTimeAmPm.FindStringSubmatch(f); m != nil {
		h, _ := strconv.Atoi(m[1])
		if m[2] == "pm" && h < 12 {
			h += 12
		}
		if m[2] == "am" && h == 12 {
			h = 0
		}
		return fmt.Sprintf("%02d:00", h)
	}
	for _, re := range []*regexp.Regexp{reTimeH, reTimeBareH} {
		if m := re.FindStringSubmatch(f); m != nil {
			h, _ := strconv.Atoi(m[1])
			// "at 7" in the evening context is 19:00
			if h >= 1 && h <= 11 && (strings.Contains(f, "veceras") || strings.Contains(f, "tonight") || strings.Contains(f, "evening") || strings.Contains(f, "uvece") || strings.Contains(f, "navecer") || strings.Contains(f, "dinner") || strings.Contains(f, "vecer")) {
				h += 12
			}
			return fmt.Sprintf("%02d:00", h)
		}
	}
	return ""
}

func findMoney(text string) *hint {
	for _, m := range reMoney.FindAllStringSubmatch(text, -1) {
		sym, val := strings.ToLower(m[1]), m[2]
		if sym == "" {
			sym, val = strings.ToLower(m[4]), m[3]
		}
		// "5 km" is a distance unless written KM (the Bosnian mark)
		if sym == "km" && !strings.Contains(m[0], "KM") {
			continue
		}
		cur := currencyOf[sym]
		if cur == "" {
			continue
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(val, ",", "."), 64)
		if err != nil || f <= 0 {
			continue
		}
		minor := int64(math.Round(f * 100))
		return &hint{Kind: "expense", Amount: fmt.Sprintf("%.2f", f), Minor: minor, Currency: cur, Title: taskTitle(text)}
	}
	return nil
}

// taskTitle is a message made into a short task: first line, no greeting.
func taskTitle(text string) string {
	t := strings.TrimSpace(strings.Split(text, "\n")[0])
	t = regexp.MustCompile(`(?i)^(hey|hi|ej|hej|cao|ćao|zdravo)[,!]?\s+`).ReplaceAllString(t, "")
	t = regexp.MustCompile(`(?i)^(can you|could you|would you|please|pls|možeš li|mozes li|molim te)[,]?\s+`).ReplaceAllString(t, "")
	t = strings.TrimRight(t, "?!. ")
	if t != "" {
		r := []rune(t)
		t = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	return clip(t, 140)
}

// ---- link previews ----

var reMeta = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
var reAttr = regexp.MustCompile(`(?is)(property|name|content)\s*=\s*("([^"]*)"|'([^']*)')`)
var reTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// previewLinks adds a preview of the first web link in a message: a
// Pocket app's own address becomes a quiet "Pocket Tasks" chip, a public
// page its title, description and picture. Pages on private addresses are
// never fetched.
func (s *Server) previewLinks(ctx context.Context, m *store.Message) {
	u := reURL.FindString(m.Body)
	if u == "" {
		return
	}
	u = strings.TrimRight(u, ".,;:!?")
	pu, err := url.Parse(u)
	if err != nil || pu.Hostname() == "" {
		return
	}
	var p map[string]any
	if app := s.appForHost(ctx, pu.Hostname()); app != nil {
		p = map[string]any{"url": u, "site": app.name, "app": app.slug, "title": app.name}
	} else {
		p, err = s.fetchPreview(ctx, u)
		if err != nil || p == nil {
			s.log.Debug("link preview", "url", u, "err", err)
			return
		}
	}
	if _, err := s.store.SetMessageData(ctx, m.ID, func(d map[string]any) error { d["preview"] = p; return nil }); err != nil {
		return
	}
	if full, err := s.store.GetMessage(ctx, m.UserID, m.ID); err == nil {
		s.decorate(full)
		s.toMembers(ctx, m.ConversationID, "message_update", shared(full))
	}
}

// appForHost is the sibling Pocket app at this host name, if any.
func (s *Server) appForHost(ctx context.Context, host string) *pocketApp {
	for i, a := range pocketApps {
		for _, base := range []string{s.resolvePublic(ctx, a.slug)} {
			if bu, err := url.Parse(base); err == nil && bu.Hostname() != "" && strings.EqualFold(bu.Hostname(), host) {
				return &pocketApps[i]
			}
		}
	}
	if strings.HasPrefix(host, "core.") || strings.HasPrefix(host, "hub.") {
		return nil
	}
	return nil
}

var errPrivate = errors.New("private address")

// publicOnly refuses connections to loopback, private, link-local and
// shared (CGNAT, tailnet) addresses.
func publicOnly(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return errPrivate
	}
	if _, cgnat, _ := net.ParseCIDR("100.64.0.0/10"); cgnat.Contains(ip) {
		return errPrivate
	}
	return nil
}

var previewClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, Control: publicOnly}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        4,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return errors.New("too many redirects")
		}
		return nil
	},
}

func (s *Server) fetchPreview(ctx context.Context, u string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent+" link preview")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	res, err := previewClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "html") {
		return nil, fmt.Errorf("HTTP %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 512<<10))
	page := string(b)
	meta := map[string]string{}
	for _, tag := range reMeta.FindAllString(page, 80) {
		var key, val string
		for _, a := range reAttr.FindAllStringSubmatch(tag, -1) {
			v := a[3] + a[4]
			switch strings.ToLower(a[1]) {
			case "property", "name":
				key = strings.ToLower(v)
			case "content":
				val = v
			}
		}
		if key != "" && val != "" {
			if _, ok := meta[key]; !ok {
				meta[key] = html.UnescapeString(strings.TrimSpace(val))
			}
		}
	}
	title := firstNonEmpty(meta["og:title"], meta["twitter:title"])
	if title == "" {
		if m := reTitle.FindStringSubmatch(page); m != nil {
			title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
	}
	if title == "" {
		return nil, errors.New("no title")
	}
	final := res.Request.URL
	p := map[string]any{"url": u, "title": clip(strings.Join(strings.Fields(title), " "), 200), "site": clip(firstNonEmpty(meta["og:site_name"], strings.TrimPrefix(final.Hostname(), "www.")), 80)}
	if d := firstNonEmpty(meta["og:description"], meta["description"], meta["twitter:description"]); d != "" {
		p["description"] = clip(strings.Join(strings.Fields(d), " "), 300)
	}
	if img := firstNonEmpty(meta["og:image"], meta["twitter:image"], meta["og:image:url"]); img != "" {
		if iu, err := final.Parse(img); err == nil && iu.Scheme == "https" {
			p["image"] = iu.String()
		}
	}
	return p, nil
}
