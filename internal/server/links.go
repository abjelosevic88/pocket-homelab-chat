package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-chat/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Chat as
// the target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "chat:read", Name: "See your chats", Description: "Read your conversations and messages (only the ones you are in), what you have not read yet, where you were mentioned, your saved messages and a day's chat activity — for your journal, your briefing, search and Pocket AI."},
	{Scope: "chat:write", Name: "Send messages for you", Description: "Post a message, a card or a picture into one of your conversations, to a person or to your own space \"Me\" (a reminder from Pocket Flows, an answer from Pocket AI you asked for). It may change or delete only the messages that app sent."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(summary|conversations|search|unread|people|saved|day/\d{4}-\d{2}-\d{2})$`), "chat:read"},
	{"GET", regexp.MustCompile(`^/api/v1/conversations/\d+(/messages|/pinned|/catchup)?$`), "chat:read"},
	{"POST", regexp.MustCompile(`^/api/v1/send$`), "chat:write"},
	{"POST", regexp.MustCompile(`^/api/v1/conversations/\d+/messages$`), "chat:write"},
	{"POST", regexp.MustCompile(`^/api/v1/conversations/\d+/upload$`), "chat:write"},
	{"PUT DELETE", regexp.MustCompile(`^/api/v1/messages/\d+$`), "chat:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "chat" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
