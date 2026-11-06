package server

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
)

// ---- demo data (DEMO_DATA=true): a family and friends who chat ----
//
// The owner, their partner Ana, the kids Luka and Mila and a friend Marko:
// a family group with plans, a poll, a shared to-do list, cards from the
// apps and Pocket AI answering; a hiking group; direct chats; the owner's
// own space. Some of it unread, so the catch-up has something to say.

type demoMsg struct {
	who     string // owner | ana | luka | mila | marko | ai
	minsAgo int
	text    string
	kind    string
	data    map[string]any
	react   map[string][]string // emoji → who
	pin     bool
	reply   int // index of an earlier message in the same list (1-based)
}

func (s *Server) seedDemo(ctx context.Context, uid int64) error {
	ctx = context.WithoutCancel(ctx)
	if _, _, done := s.store.CacheGet(ctx, "demo:seeded"); done {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	people := map[string]int64{"owner": uid}
	for _, p := range []struct{ key, name, email string }{
		{"ana", "Ana Kovač", "ana@demo.pocket"}, {"luka", "Luka", "luka@demo.pocket"}, {"mila", "Mila", "mila@demo.pocket"}, {"marko", "Marko Hadžić", "marko@demo.pocket"},
	} {
		u, err := s.store.GetUserByEmail(ctx, p.email)
		if err != nil {
			u = &store.User{Email: p.email, Name: p.name, Role: "member"}
			if err := s.store.CreateUser(ctx, u); err != nil {
				return err
			}
		}
		people[p.key] = u.ID
	}
	now := time.Now()
	day := 24 * 60
	owner, err := s.store.GetUser(ctx, uid)
	if err != nil {
		return err
	}
	ownerFirst := firstNonEmpty(firstWord(owner.Name), strings.Split(owner.Email, "@")[0])

	family := &store.Conversation{Title: "Family", Emoji: "🏡", Color: "#128c7e", About: "Plans, shopping and everything at home", AI: true, Smart: true}
	if err := s.store.CreateGroup(ctx, uid, family, []int64{people["ana"], people["luka"], people["mila"]}); err != nil {
		return err
	}
	hikers := &store.Conversation{Title: "Weekend hikers", Emoji: "🥾", Color: "#4d7c0f", About: "Bjelašnica, Treskavica and the long way back", AI: true, Smart: true}
	if err := s.store.CreateGroup(ctx, people["marko"], hikers, []int64{uid, people["ana"]}); err != nil {
		return err
	}
	withAna, _, err := s.store.DirectWith(ctx, uid, people["ana"])
	if err != nil {
		return err
	}
	withMarko, _, err := s.store.DirectWith(ctx, uid, people["marko"])
	if err != nil {
		return err
	}
	me, _, err := s.store.SelfFor(ctx, uid)
	if err != nil {
		return err
	}

	recipe := map[string]any{"card": map[string]any{"app": "kitchen", "kind": "recipe", "icon": "🍲", "title": "Grandma's stuffed peppers (punjene paprike)",
		"subtitle": "1 h 30 min · Main · Bosnian", "lines": []any{"8 peppers", "500 g minced beef", "100 g rice", "1 onion", "400 g tomato passata", "paprika, salt, pepper"}, "rating": 5.0}}
	concert := map[string]any{"card": map[string]any{"app": "calendar", "kind": "event", "icon": "📅", "title": "Mila's school concert",
		"subtitle": now.AddDate(0, 0, 3).Format("Mon 2 Jan") + ", 18:00–19:30 · Music school, Mehmeda Spahe", "fields": map[string]any{"title": "Mila's school concert",
			"start": now.AddDate(0, 0, 3).Format("2006-01-02") + "T18:00", "end": now.AddDate(0, 0, 3).Format("2006-01-02") + "T19:30", "location": "Music school, Mehmeda Spahe"}}}
	cottage := map[string]any{"title": "Weekend at the cottage", "next": 7, "items": []any{
		map[string]any{"id": 1, "text": "Firewood from the shed", "done": true, "assignee": people["luka"], "done_by": people["luka"]},
		map[string]any{"id": 2, "text": "Groceries for two days", "done": false, "assignee": uid},
		map[string]any{"id": 3, "text": "Board games and the speaker", "done": true, "assignee": people["mila"], "done_by": people["mila"]},
		map[string]any{"id": 4, "text": "Check the water heater", "done": false, "assignee": people["ana"]},
		map[string]any{"id": 5, "text": "Sleeping bags for Luka's friend", "done": false},
		map[string]any{"id": 6, "text": "Charge the power bank", "done": false},
	}}
	poll := map[string]any{"question": "Where for Saturday lunch?", "options": []any{"Ćevabdžinica Želja", "Pizza at Mrkva", "We cook at home"}, "multi": false, "closed": false}
	place := map[string]any{"card": map[string]any{"app": "places", "kind": "place", "icon": "📍", "title": "Planinarski dom Bjelašnica",
		"subtitle": "Mountain hut · Bjelašnica", "lines": []any{"Opened all year, cash only", "Bean soup and pita until 17:00"}, "rating": 4.5,
		"fields": map[string]any{"name": "Planinarski dom Bjelašnica", "kind": "activity", "city": "Bjelašnica", "country": "Bosnia and Herzegovina"}}}
	weather := map[string]any{"card": map[string]any{"app": "weather", "kind": "weather", "icon": "🌤️", "title": "Weather on Bjelašnica",
		"subtitle": "Saturday: sunny, 4–11 °C, wind 15 km/h", "text": "Sunny most of the day, a cold start. Rain chance under 10%."}}
	parcel := map[string]any{"card": map[string]any{"app": "parcels", "kind": "parcel", "icon": "📦", "title": "Hiking boots (Decathlon)",
		"subtitle": "Out for delivery · expected today", "badge": "Out for delivery"}}

	conversations := []struct {
		id   int64
		msgs []demoMsg
		read map[string]int // who has read how many from the end unread
	}{
		{family.ID, []demoMsg{
			{who: "ana", minsAgo: 13*day + 300, text: "Good morning! Who took the car keys? 🙃"},
			{who: "luka", minsAgo: 13*day + 296, text: "Me, sorry. They're on the shelf now"},
			{who: "ana", minsAgo: 12*day + 140, text: "Here's the recipe Grandma sent, let's make it on Sunday", data: recipe, react: map[string][]string{"😋": {"luka", "mila", "owner"}}},
			{who: "mila", minsAgo: 11*day + 600, text: "My concert is on " + now.AddDate(0, 0, 3).Format("Monday") + "! Please all come 🎻", data: concert, react: map[string][]string{"❤️": {"ana", "owner"}}},
			{who: "owner", minsAgo: 11*day + 590, text: "Wouldn't miss it. Added it to my calendar"},
			{who: "ana", minsAgo: 9*day + 30, text: "The Wi-Fi at the cottage: network Vikendica, password is on the fridge 😄", pin: true},
			{who: "owner", minsAgo: 7*day + 420, text: "", kind: store.MsgTodo, data: cottage},
			{who: "luka", minsAgo: 7*day + 380, text: "Firewood done 💪"},
			{who: "ana", minsAgo: 6*day + 200, text: "I paid 42 € for the groceries at Konzum, adding it to Split"},
			{who: "owner", minsAgo: 3*day + 80, text: "", kind: store.MsgPoll, data: poll},
			{who: "mila", minsAgo: 3*day + 70, text: "Pizza!!! 🍕"},
			{who: "luka", minsAgo: 3*day + 65, text: "Želja, obviously"},
			{who: "ana", minsAgo: 2*day + 500, text: "Can you pick up bread and milk on the way home tomorrow?", reply: 0},
			{who: "owner", minsAgo: 2*day + 490, text: "Sure 👍", reply: 13},
			{who: "owner", minsAgo: 2*day + 100, text: "Saturday looks sunny, let's eat outside 🌤️"},
			{who: "ana", minsAgo: 300, text: "Dinner at Kod Muje on Friday at 8? My treat for Mila's good grades 🎉"},
			{who: "mila", minsAgo: 250, text: "YES"},
			{who: "luka", minsAgo: 40, text: "@" + "ana can I bring Emir to the cottage on the weekend?"},
			{who: "ana", minsAgo: 25, text: "Fine by me if Dad agrees. @owner?"},
		}, map[string]int{"owner": 3}},
		{hikers.ID, []demoMsg{
			{who: "marko", minsAgo: 6*day + 600, text: "Saturday: Bjelašnica, from Babin Do to the hut and back. Who's in?"},
			{who: "ana", minsAgo: 6*day + 560, text: "In! 🥾"},
			{who: "owner", minsAgo: 6*day + 540, text: "In, if the weather holds"},
			{who: "marko", minsAgo: 6*day + 500, text: "", data: weather},
			{who: "marko", minsAgo: 5*day + 100, text: "Lunch at the hut, their bean soup is the best", data: place, react: map[string][]string{"👍": {"ana", "owner"}}},
			{who: "owner", minsAgo: 4*day + 60, text: "My new boots are finally coming 😅", data: parcel},
			{who: "marko", minsAgo: 4*day + 50, text: "Just in time"},
			{who: "marko", minsAgo: 180, text: "Leaving at 7:00 from the Zetra parking on Saturday. Fuel is 30 KM, we split it"},
			{who: "ana", minsAgo: 170, text: "I'll bring coffee and the first aid kit"},
			{who: "marko", minsAgo: 90, text: "Also: @owner can you bring the big thermos?"},
			{who: "marko", minsAgo: 88, text: "And check https://www.bjelasnica.ba for the lift times"},
		}, map[string]int{"owner": 4}},
		{withAna, []demoMsg{
			{who: "ana", minsAgo: 5*day + 60, text: "Don't forget the dentist on Thursday at 16:30 🦷"},
			{who: "owner", minsAgo: 5*day + 55, text: "Thanks, I forgot completely"},
			{who: "owner", minsAgo: 2*day + 30, text: "Gift idea for your mum: that blue scarf from the market?"},
			{who: "ana", minsAgo: 2*day + 25, text: "She'd love it ❤️", react: map[string][]string{"❤️": {"owner"}}},
			{who: "ana", minsAgo: 60, text: "Running 10 minutes late, start without me"},
		}, map[string]int{"owner": 1}},
		{withMarko, []demoMsg{
			{who: "marko", minsAgo: 8*day + 100, text: "Thanks for the help with the move yesterday!"},
			{who: "owner", minsAgo: 8*day + 90, text: "Any time. The sofa is still my enemy though 😂"},
			{who: "marko", minsAgo: 8*day + 85, text: "Haha. Beer on me next week"},
		}, nil},
		{me, []demoMsg{
			{who: "owner", minsAgo: 10*day + 30, text: "Ideas for Ana's birthday: concert tickets, the cooking class, a weekend in Mostar"},
			{who: "owner", minsAgo: 4*day + 10, text: "Read later: https://en.wikipedia.org/wiki/Bjela%C5%A1nica"},
			{who: "owner", minsAgo: 30, text: "Buy batteries for the smoke detector"},
		}, nil},
	}

	for _, c := range conversations {
		var ids []int64
		for i, dm := range c.msgs {
			at := now.Add(-time.Duration(dm.minsAgo)*time.Minute - time.Duration(rng.Intn(50))*time.Second)
			m := &store.Message{ConversationID: c.id, Kind: firstNonEmpty(dm.kind, store.MsgText), Body: strings.ReplaceAll(dm.text, "@owner", "@"+ownerFirst), Data: map[string]any{}, CreatedAt: at.UTC().Format(time.RFC3339Nano)}
			for k, v := range dm.data {
				m.Data[k] = v
			}
			if _, ok := m.Data["card"]; ok && m.Kind == store.MsgText {
				m.Kind = store.MsgCard
			}
			m.UserID = people[dm.who]
			if dm.reply > 0 && dm.reply <= len(ids) {
				m.ReplyTo = ids[dm.reply-1]
			}
			if conv, err := s.store.GetConversation(ctx, uid, c.id); err == nil {
				body := m.Body
				m.Mentions = mentionsIn(body, conv.Members, m.UserID)
				if body != "" {
					if hints := detectHints(body, at); len(hints) > 0 {
						m.Data["hints"] = hints
					}
				}
			}
			if _, err := s.store.InsertMessage(ctx, m); err != nil {
				return fmt.Errorf("demo message %d: %w", i, err)
			}
			ids = append(ids, m.ID)
			for e, who := range dm.react {
				for _, w := range who {
					_, _ = s.store.ToggleReaction(ctx, m.ID, people[w], e)
				}
			}
			if dm.pin {
				_ = s.store.Pin(ctx, m.ID, people[dm.who], true)
				_ = s.store.Star(ctx, uid, m.ID, true)
			}
			if m.Kind == store.MsgPoll {
				for w, o := range map[string]int{"mila": 1, "luka": 0, "ana": 0} {
					_ = s.store.SetVotes(ctx, m.ID, people[w], []int{o})
				}
			}
		}
		// everyone has read everything, except the last few for some
		members, _ := s.store.MemberIDs(ctx, c.id)
		for _, mid := range members {
			upto := ids[len(ids)-1]
			for who, n := range c.read {
				if people[who] == mid && n < len(ids) {
					upto = ids[len(ids)-1-n]
				}
			}
			_, _ = s.store.DB.ExecContext(ctx, `UPDATE members SET last_read_id = ? WHERE user_id = ? AND conversation_id = ?`, upto, mid, c.id)
		}
	}
	_ = s.store.CachePut(ctx, "demo:seeded", "1")
	s.log.Info("demo data seeded", "conversations", len(conversations))
	return nil
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}
