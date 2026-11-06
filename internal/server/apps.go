package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- sharing from the other apps, and making things from messages ----
//
// "Share from your apps" lists things in one of the person's Pocket apps
// (through their own Core link or token) and posts the chosen one as a
// card: a snapshot everyone in the conversation can read, with a link back
// for the sharer. "Make…" turns a message into a task, an event, a note, a
// split cost, a wish or a saved place in the person's own apps, and the
// message remembers it for them.

// shareApps are the apps things can be shared from, with what they share.
var shareApps = []struct{ slug, label, noun string }{
	{"calendar", "An event", "event"}, {"tasks", "A task", "task"}, {"notes", "A note", "note"},
	{"places", "A place", "place"}, {"kitchen", "A recipe", "recipe"}, {"wishlist", "A wish or gift idea", "wish"},
	{"travel", "A trip", "trip"}, {"parcels", "A delivery", "parcel"}, {"bookshelf", "A book", "book"},
	{"watch", "A film or show", "film"}, {"weather", "Today's weather", "weather"},
}

// shareItem is one thing that can be shared, with its card ready.
type shareItem struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle,omitempty"`
	Date     string         `json:"date,omitempty"`
	Card     map[string]any `json:"card"`
}

func (s *Server) handleShareApps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	out := []map[string]any{}
	for _, sa := range shareApps {
		a, _ := appBySlug(sa.slug)
		_, via, detail := s.connStatus(ctx, u, st, a)
		out = append(out, map[string]any{"slug": a.slug, "name": a.name, "label": sa.label, "connected": via != "", "detail": detail})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleShareItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok := appBySlug(chiParam(r, "app"))
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown app")
		return
	}
	uid := actorID(ctx)
	c := s.clients(ctx, uid)
	sib, err := c.conn(ctx, a, readScopes(a)...)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.shareList(ctx, sib, a, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeErr(w, http.StatusBadGateway, a.name+": "+err.Error())
		return
	}
	for _, it := range items {
		b, _ := json.Marshal(it.Card)
		_ = s.store.CachePut(ctx, fmt.Sprintf("share:%d:%s:%s", uid, a.slug, it.ID), string(b))
	}
	writeJSON(w, http.StatusOK, items)
}

func readScopes(a pocketApp) []string {
	var out []string
	for _, sc := range a.scopes {
		if strings.HasSuffix(sc, ":read") {
			out = append(out, sc)
		}
	}
	return out
}

// handleShare posts a card the person picked a moment ago.
func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	var in struct {
		App      string `json:"app"`
		ID       string `json:"id"`
		Text     string `json:"text"`
		ClientID string `json:"client_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	raw, at, ok := s.store.CacheGet(ctx, fmt.Sprintf("share:%d:%s:%s", me.ID, in.App, in.ID))
	if t, err := time.Parse(time.RFC3339Nano, at); !ok || err != nil || time.Since(t) > time.Hour {
		writeErr(w, http.StatusGone, "pick it again from the list")
		return
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		writeErr(w, http.StatusGone, "pick it again from the list")
		return
	}
	m, status, err := s.send(ctx, me, idParam(r, "id"), sendInput{Text: in.Text, Card: card, ClientID: in.ClientID})
	if err != nil {
		if status == 0 {
			s.fail(w, err, "share")
		} else {
			writeErr(w, status, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func money(minor float64, cur string) string {
	if minor == 0 {
		return ""
	}
	v := strconv.FormatFloat(minor/100, 'f', 2, 64)
	v = strings.TrimSuffix(v, ".00")
	if cur == "" {
		return v
	}
	return v + " " + cur
}

func stars(r float64) string {
	if r <= 0 {
		return ""
	}
	n := int(math.Round(r))
	if n > 5 {
		n = 5
	}
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

func when(start, end string, allDay bool) string {
	parse := func(v string) (time.Time, bool) {
		for _, f := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.Parse(f, v); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	st, ok := parse(start)
	if !ok {
		return start
	}
	if allDay || len(start) == 10 {
		return st.Format("Mon 2 Jan")
	}
	out := st.Format("Mon 2 Jan, 15:04")
	if et, ok := parse(end); ok && et.After(st) {
		if et.Format("2006-01-02") == st.Format("2006-01-02") {
			out += "–" + et.Format("15:04")
		}
	}
	return out
}

func httpsOnly(u string) string {
	if strings.HasPrefix(u, "https://") {
		return u
	}
	return ""
}

func (s *Server) shareList(ctx context.Context, sib *siblingConn, a pocketApp, q string) ([]shareItem, error) {
	vals := url.Values{}
	link := func(path string) string {
		if sib.public == "" {
			return ""
		}
		return strings.TrimRight(sib.public, "/") + path
	}
	var items []shareItem
	add := func(id, title, sub, date string, card map[string]any) {
		card["app"], card["title"] = a.slug, title
		if sub != "" {
			card["subtitle"] = sub
		}
		card["ref"] = id
		items = append(items, shareItem{ID: id, Title: title, Subtitle: sub, Date: date, Card: card})
	}
	match := func(parts ...string) bool {
		if q == "" {
			return true
		}
		return strings.Contains(store.Fold(strings.Join(parts, " ")), store.Fold(q))
	}
	switch a.slug {
	case "tasks":
		vals.Set("status", "open")
		vals.Set("limit", "60")
		if q != "" {
			vals.Set("q", q)
		}
		v, err := getJSONFrom(ctx, sib, "/api/v1/tasks", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "tasks") {
			due := str(m, "due")
			sub := joinNonEmpty(" · ", when(due, "", len(due) == 10), str(obj(m, "list"), "name"))
			add(idOf(m), str(m, "title"), sub, day10(due), map[string]any{"kind": "task", "icon": "☑️", "text": clip(str(m, "notes"), 600),
				"url": link("/today?task=" + idOf(m)), "fields": map[string]any{"title": str(m, "title"), "due": due, "notes": clip(str(m, "notes"), 500)}})
		}
	case "calendar":
		now := time.Now()
		vals.Set("from", now.Format("2006-01-02"))
		vals.Set("to", now.AddDate(0, 0, 90).Format("2006-01-02"))
		vals.Set("tz", time.Local.String())
		v, err := getJSONFrom(ctx, sib, "/api/v1/events", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "events") {
			if !match(str(m, "title"), str(m, "location")) {
				continue
			}
			allDay, _ := m["all_day"].(bool)
			start, end := str(m, "start"), str(m, "end")
			sub := joinNonEmpty(" · ", when(start, end, allDay), str(m, "location"))
			add(idOf(m), firstNonEmpty(str(m, "title"), "Event"), sub, day10(start), map[string]any{"kind": "event", "icon": "📅", "text": clip(str(m, "description"), 600),
				"url": link("/event/" + idOf(m)), "date": start,
				"fields": map[string]any{"title": str(m, "title"), "start": start, "end": end, "all_day": allDay, "location": str(m, "location")}})
			if len(items) >= 60 {
				break
			}
		}
	case "notes":
		path := "/api/v1/notes"
		if q != "" {
			path = "/api/v1/search"
			vals.Set("q", q)
			vals.Set("limit", "30")
		} else {
			vals.Set("sort", "modified")
			vals.Set("limit", "40")
		}
		v, err := getJSONFrom(ctx, sib, path, vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "notes") {
			text := firstNonEmpty(str(m, "excerpt"), str(m, "snippet"))
			add(idOf(m), firstNonEmpty(str(m, "title"), "Note"), str(m, "category"), day10(m["modified"]), map[string]any{"kind": "note", "icon": "📝", "text": clip(text, 800),
				"url": link("/note/" + idOf(m)), "fields": map[string]any{"title": str(m, "title"), "content": clip(text, 1400)}})
		}
	case "places":
		vals.Set("limit", "60")
		if q != "" {
			vals.Set("q", q)
		}
		v, err := getJSONFrom(ctx, sib, "/api/v1/places", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "places") {
			sub := joinNonEmpty(" · ", strings.ReplaceAll(str(m, "kind"), "_", " "), str(m, "cuisine"), str(m, "city"))
			card := map[string]any{"kind": "place", "icon": "📍", "text": clip(str(m, "notes"), 400), "url": link("/places/" + idOf(m)),
				"lines": compact(str(m, "address"), str(m, "hours")),
				"fields": map[string]any{"name": str(m, "name"), "kind": str(m, "kind"), "cuisine": str(m, "cuisine"), "city": str(m, "city"), "country": str(m, "country"),
					"address": str(m, "address"), "website": str(m, "website"), "map_url": str(m, "map_url"), "lat": num(m, "lat"), "lng": num(m, "lng")}}
			if r := num(m, "rating"); r > 0 {
				card["rating"] = r
			}
			add(idOf(m), str(m, "name"), sub, day10(m["last_visit"]), card)
		}
	case "kitchen":
		vals.Set("limit", "60")
		if q != "" {
			vals.Set("q", q)
		}
		v, err := getJSONFrom(ctx, sib, "/api/v1/recipes", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "recipes") {
			mins := int(num(m, "total_minutes"))
			sub := joinNonEmpty(" · ", minutes(mins), str(m, "course"), str(m, "cuisine"))
			var lines []string
			for _, ing := range rows(map[string]any{"items": m["ingredients"]}) {
				lines = append(lines, joinNonEmpty(" ", str(ing, "qty"), str(ing, "unit"), str(ing, "name")))
			}
			card := map[string]any{"kind": "recipe", "icon": "🍲", "text": clip(str(m, "description"), 400), "url": link("/recipes/" + idOf(m)),
				"lines": compact(lines...), "image": httpsOnly(str(m, "image_url"))}
			if r := num(m, "rating"); r > 0 {
				card["rating"] = r
			}
			add(idOf(m), str(m, "title"), sub, day10(m["last_cooked"]), card)
		}
	case "wishlist":
		vals.Set("status", "wanted")
		vals.Set("limit", "60")
		if q != "" {
			vals.Set("q", q)
		}
		v, err := getJSONFrom(ctx, sib, "/api/v1/wishes", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "wishes") {
			price := money(num(m, "price"), str(m, "currency"))
			add(idOf(m), str(m, "title"), joinNonEmpty(" · ", price, str(m, "store"), str(m, "list_name")), "", map[string]any{"kind": "wish", "icon": "🎁",
				"text": clip(str(m, "notes"), 300), "url": firstNonEmpty(str(m, "url"), link("/wishes/"+idOf(m))), "image": httpsOnly(str(m, "image_url")), "badge": price,
				"fields": map[string]any{"title": str(m, "title"), "url": str(m, "url"), "price": num(m, "price"), "currency": str(m, "currency"), "store": str(m, "store")}})
		}
	case "travel":
		v, err := getJSONFrom(ctx, sib, "/api/v1/trips", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "trips") {
			if !match(str(m, "name"), str(m, "destination")) {
				continue
			}
			sub := joinNonEmpty(" · ", str(m, "destination"), dateRange(str(m, "start_date"), str(m, "end_date")))
			add(idOf(m), str(m, "name"), sub, str(m, "start_date"), map[string]any{"kind": "trip", "icon": "✈️", "text": clip(str(m, "description"), 500),
				"url": link("/trips/" + idOf(m)), "badge": str(m, "status"),
				"fields": map[string]any{"name": str(m, "name"), "start": str(m, "start_date"), "end": str(m, "end_date"), "destination": str(m, "destination")}})
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].Date > items[j].Date })
	case "parcels":
		vals.Set("view", "active")
		if q != "" {
			vals.Set("q", q)
			vals.Set("view", "all")
		}
		v, err := getJSONFrom(ctx, sib, "/api/v1/parcels", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "parcels") {
			eta := ""
			if e := str(m, "expected_on"); e != "" {
				eta = "expected " + when(e, "", true)
			}
			sub := joinNonEmpty(" · ", str(m, "status_label"), eta, str(m, "carrier_name"))
			add(idOf(m), firstNonEmpty(str(m, "title"), str(m, "shop"), str(m, "tracking_number")), sub, str(m, "expected_on"), map[string]any{"kind": "parcel", "icon": "📦",
				"text": str(m, "status_text"), "url": firstNonEmpty(link(str(m, "url")), link("/parcels/"+idOf(m))), "badge": str(m, "status_label")})
		}
	case "bookshelf":
		vals.Set("sort", "recent")
		vals.Set("limit", "60")
		if q != "" {
			vals.Set("q", q)
		}
		v, err := getJSONFrom(ctx, sib, "/api/v1/books", vals)
		if err != nil {
			return nil, err
		}
		for _, m := range rows(v, "books") {
			card := map[string]any{"kind": "book", "icon": "📚", "url": link("/books/" + idOf(m)), "image": httpsOnly(str(m, "cover")), "badge": str(m, "status")}
			if r := num(m, "rating"); r > 0 {
				card["rating"] = r
			}
			add(idOf(m), str(m, "title"), str(m, "authors"), day10(m["finished_on"]), card)
		}
	case "watch":
		vals.Set("limit", "80")
		if q != "" {
			vals.Set("q", q)
		}
		v, err := getJSONFrom(ctx, sib, "/api/v1/history", vals)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, m := range rows(v, "items") {
			title, id := str(m, "title"), firstNonEmpty(str(m, "item_id"), idOf(m))
			if str(m, "kind") == "episode" && str(m, "series") != "" {
				title, id = str(m, "series"), firstNonEmpty(str(m, "series_id"), id)
			}
			if seen[title] {
				continue
			}
			seen[title] = true
			add(id, title, joinNonEmpty(" · ", str(m, "year"), str(m, "genres")), day10(m["watched_at"]), map[string]any{"kind": "film", "icon": "🎬", "url": link("/title/" + id)})
		}
	case "weather":
		v, err := getJSONFrom(ctx, sib, "/api/v1/summary", vals)
		if err != nil {
			return nil, err
		}
		m, _ := v.(map[string]any)
		if m == nil || m["place"] == nil {
			return []shareItem{}, nil
		}
		place := firstNonEmpty(str(obj(m, "place"), "name"), str(m, "place"))
		add("today", "Weather in "+place, str(m, "text"), time.Now().Format("2006-01-02"), map[string]any{"kind": "weather", "icon": "🌤️", "text": str(m, "text"), "url": link("/")})
	default:
		return nil, errors.New("nothing to share from this app")
	}
	if items == nil {
		items = []shareItem{}
	}
	return items, nil
}

func compact(parts ...string) []any {
	var out []any
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return out
}

func minutes(n int) string {
	switch {
	case n <= 0:
		return ""
	case n < 60:
		return fmt.Sprintf("%d min", n)
	case n%60 == 0:
		return fmt.Sprintf("%d h", n/60)
	}
	return fmt.Sprintf("%d h %d min", n/60, n%60)
}

func dateRange(a, b string) string {
	if a == "" {
		return ""
	}
	if b == "" || b == a {
		return when(a, "", true)
	}
	return when(a, "", true) + " – " + when(b, "", true)
}

// ---- making things from a message ----

// makeTargets are what a message can become, and where.
var makeTargets = map[string]struct {
	app   pocketApp
	scope string
	label string
}{
	"task":    {appTasks, "tasks:write", "Task"},
	"event":   {appCalendar, "calendar:write", "Event"},
	"note":    {appNotes, "notes:write", "Note"},
	"expense": {appSplit, "expenses:write", "Split cost"},
	"wish":    {appWishlist, "wishes:write", "Wish"},
	"place":   {appPlaces, "places:write", "Place"},
}

// handleMakeOptions says which "Make…" targets work for the person, with
// their Split groups.
func (s *Server) handleMakeOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	out := map[string]any{}
	for k, t := range makeTargets {
		_, via, detail := s.connStatus(ctx, u, st, t.app)
		out[k] = map[string]any{"connected": via != "", "detail": detail, "app": t.app.name, "label": t.label}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSplitGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.clients(ctx, actorID(ctx))
	sib, err := c.conn(ctx, appSplit, "groups:read")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/groups", url.Values{"archived": {"0"}})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Pocket Split: "+err.Error())
		return
	}
	out := []map[string]any{}
	for _, g := range rows(v, "groups") {
		var members []map[string]any
		for _, m := range rows(map[string]any{"items": g["members"]}) {
			members = append(members, map[string]any{"id": num(m, "id"), "name": str(m, "name")})
		}
		out = append(out, map[string]any{"id": num(g, "id"), "name": str(g, "name"), "currency": str(g, "currency"), "my_member_id": num(g, "my_member_id"), "members": members})
	}
	writeJSON(w, http.StatusOK, out)
}

// makeInput is what the person checked in the "Make…" sheet.
type makeInput struct {
	Target   string  `json:"target"`
	Title    string  `json:"title"`
	Notes    string  `json:"notes"`
	Date     string  `json:"date"` // YYYY-MM-DD
	Time     string  `json:"time"` // HH:MM
	Minutes  int     `json:"minutes"`
	Location string  `json:"location"`
	Amount   float64 `json:"amount"` // major units: 12.5
	Currency string  `json:"currency"`
	Group    int64   `json:"group"`
	PaidBy   int64   `json:"paid_by"`
	Members  []int64 `json:"members"` // Split members to share with (empty = everyone)
	URL      string  `json:"url"`
	Kind     string  `json:"kind"`
	City     string  `json:"city"`
	Address  string  `json:"address"`
	Folder   string  `json:"folder"`
}

func (s *Server) handleMake(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	m, err := s.store.GetMessage(ctx, me.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "message")
		return
	}
	var in makeInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	t, ok := makeTargets[in.Target]
	if !ok {
		writeErr(w, http.StatusBadRequest, "make a task, event, note, expense, wish or place")
		return
	}
	in.Title = clip(strings.TrimSpace(in.Title), 200)
	if !validDate(in.Date) {
		writeErr(w, http.StatusBadRequest, "the date is YYYY-MM-DD")
		return
	}
	c := s.clients(ctx, me.ID)
	sib, err := c.conn(ctx, t.app, t.scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	conv := s.store.ConversationTitle(ctx, me.ID, m.ConversationID)
	from := firstNonEmpty(m.AuthorName, "someone")
	origin := fmt.Sprintf("From %s in Pocket Chat (%s): “%s”", from, conv, clip(m.Body, 600))
	if m.Body == "" {
		if card, ok := m.Data["card"].(map[string]any); ok {
			origin = fmt.Sprintf("Shared by %s in Pocket Chat (%s): %s", from, conv, str(card, "title"))
		}
	}
	link := strings.TrimRight(s.cfg.BaseURL, "/") + fmt.Sprintf("/c/%d?m=%d", m.ConversationID, m.ID)
	var res map[string]any
	var path string
	switch in.Target {
	case "task":
		if in.Title == "" {
			writeErr(w, http.StatusBadRequest, "the task needs a title")
			return
		}
		body := map[string]any{"title": in.Title, "notes": strings.TrimSpace(joinNonEmpty("\n\n", in.Notes, origin)), "url": link}
		if in.Date != "" {
			due := in.Date
			if in.Time != "" {
				due += "T" + in.Time
			}
			body["due"] = due
		}
		res, err = postJSONTo(ctx, sib, "/api/v1/tasks", body)
		path = "/today?task=" + idOf(res)
	case "event":
		if in.Title == "" || in.Date == "" {
			writeErr(w, http.StatusBadRequest, "the event needs a title and a day")
			return
		}
		body := map[string]any{"title": in.Title, "description": joinNonEmpty("\n\n", in.Notes, origin, link), "location": in.Location, "tz": time.Local.String()}
		if in.Time == "" {
			body["start"], body["all_day"] = in.Date, true
		} else {
			start, err := time.ParseInLocation("2006-01-02 15:04", in.Date+" "+in.Time, time.Local)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "the time is HH:MM")
				return
			}
			mins := in.Minutes
			if mins <= 0 {
				mins = 60
			}
			body["start"] = start.Format("2006-01-02T15:04")
			body["end"] = start.Add(time.Duration(mins) * time.Minute).Format("2006-01-02T15:04")
		}
		res, err = postJSONTo(ctx, sib, "/api/v1/events", body)
		path = "/event/" + idOf(res)
	case "note":
		content := strings.TrimSpace(in.Notes)
		if content == "" {
			content = m.Body
		}
		content = joinNonEmpty("\n\n", content, "— "+origin+"\n"+link)
		body := map[string]any{"title": firstNonEmpty(in.Title, clip(firstLine(m.Body), 80)), "content": content}
		if in.Folder != "" {
			body["category"] = in.Folder
		}
		res, err = postJSONTo(ctx, sib, "/api/v1/notes", body)
		path = "/note/" + idOf(res)
	case "expense":
		if in.Group <= 0 || in.Amount <= 0 || in.Title == "" {
			writeErr(w, http.StatusBadRequest, "pick a Split group, an amount and what it was for")
			return
		}
		var g map[string]any
		gv, gerr := getJSONFrom(ctx, sib, fmt.Sprintf("/api/v1/groups/%d", in.Group), nil)
		if gerr != nil {
			// a link with only expenses:write cannot read the group
			if gs, err2 := c.conn(ctx, appSplit, "groups:read"); err2 == nil {
				gv, gerr = getJSONFrom(ctx, gs, fmt.Sprintf("/api/v1/groups/%d", in.Group), nil)
			}
		}
		if gerr != nil {
			writeErr(w, http.StatusBadGateway, "Pocket Split: "+gerr.Error())
			return
		}
		g, _ = gv.(map[string]any)
		paid := in.PaidBy
		if paid <= 0 {
			paid = int64(num(g, "my_member_id"))
		}
		want := map[int64]bool{}
		for _, id := range in.Members {
			want[id] = true
		}
		var shares []map[string]any
		for _, mm := range rows(map[string]any{"items": g["members"]}) {
			id := int64(num(mm, "id"))
			if len(want) == 0 || want[id] {
				shares = append(shares, map[string]any{"member_id": id, "share": 1})
			}
		}
		if len(shares) == 0 || paid <= 0 {
			writeErr(w, http.StatusBadRequest, "pick who paid and who shares it")
			return
		}
		date := firstNonEmpty(in.Date, time.Now().Format("2006-01-02"))
		body := map[string]any{"title": in.Title, "amount": int64(math.Round(in.Amount * 100)), "paid_by": paid, "date": date,
			"split_mode": "even", "shares": shares, "notes": origin}
		if cur := cleanCurrency(in.Currency); cur != "" {
			body["currency"] = cur
		}
		res, err = postJSONTo(ctx, sib, fmt.Sprintf("/api/v1/groups/%d/expenses", in.Group), body)
		path = fmt.Sprintf("/groups/%d/expenses/%s", in.Group, idOf(res))
	case "wish":
		u, ok := cleanURL(in.URL)
		if !ok || (u == "" && in.Title == "") {
			writeErr(w, http.StatusBadRequest, "a wish needs a name or a shop link")
			return
		}
		body := map[string]any{"url": u, "notes": joinNonEmpty("\n\n", in.Notes, origin)}
		if in.Title != "" {
			body["title"] = in.Title
		}
		res, err = postJSONTo(ctx, sib, "/api/v1/wishes", body)
		path = "/wishes/" + idOf(res)
	case "place":
		if in.Title == "" {
			writeErr(w, http.StatusBadRequest, "the place needs a name")
			return
		}
		kind := in.Kind
		if !oneOf(kind, []string{"restaurant", "cafe", "bar", "bakery", "street_food", "dessert", "market", "shop", "museum", "sight", "park", "viewpoint", "beach", "nature", "activity", "hotel", "other"}) {
			kind = "restaurant"
		}
		site, _ := cleanURL(in.URL)
		body := map[string]any{"name": in.Title, "kind": kind, "status": "want", "city": in.City, "address": in.Address, "website": site,
			"notes": joinNonEmpty("\n\n", in.Notes, origin), "recommended_by": from, "source_ref": fmt.Sprintf("message:%d", m.ID)}
		res, err = postJSONTo(ctx, sib, "/api/v1/places", body)
		path = "/places/" + idOf(res)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, t.app.name+": "+err.Error())
		return
	}
	d := &store.Made{MessageID: m.ID, Target: in.Target, App: t.app.slug, Ref: idOf(res), Title: firstNonEmpty(str(res, "title", "name"), in.Title)}
	if sib.public != "" && d.Ref != "" {
		d.URL = strings.TrimRight(sib.public, "/") + path
	}
	if err := s.store.AddMade(ctx, me.ID, d); err != nil {
		s.fail(w, err, "remember")
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.Split(strings.TrimSpace(s), "\n")[0])
}

func (s *Server) handleMade(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.MadeList(r.Context(), actorID(r.Context()), 200)
	if err != nil {
		s.fail(w, err, "made")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ---- voice messages written out by Pocket Voice ----

// transcribe sends a voice message to the sender's Pocket Voice (whisper on
// this server) and shows its words to everyone.
func (s *Server) transcribe(ctx context.Context, uid int64, m *store.Message) {
	if len(m.Attachments) != 1 || m.Attachments[0].Transcript != "" {
		return
	}
	a := m.Attachments[0]
	c := s.clients(ctx, uid)
	sib, err := c.conn(ctx, appVoice, "voice:transcribe")
	if err != nil {
		return
	}
	data, err := readFile(s.cfg.FilesDir, a.File)
	if err != nil {
		return
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := sib.client.Upload(ctx, "/api/v1/transcribe", "file", "voice"+fileExt(a.File), a.Mime, data, &out); err != nil {
		s.log.Info("voice message: Pocket Voice could not write it out", "err", err)
		return
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return
	}
	if err := s.store.SetTranscript(ctx, a.ID, clip(text, 8000)); err != nil {
		return
	}
	_, _ = s.store.SetMessageData(ctx, m.ID, func(d map[string]any) error { d["text"] = clip(text, 8000); return nil })
	if full, err := s.store.GetMessage(ctx, uid, m.ID); err == nil {
		s.decorate(full)
		s.toMembers(ctx, m.ConversationID, "message_update", shared(full))
	}
}

func readFile(dir, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, filepath.Base(name)))
}

func fileExt(name string) string { return strings.ToLower(filepath.Ext(name)) }
