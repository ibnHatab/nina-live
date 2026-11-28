package setup

import (
	"context"
	"strings"
	"time"
)

// The Google Cloud APIs her project enables, in one page of Google's (the console's enable flow).
const enableAPIs = "https://console.cloud.google.com/flows/enableapi?apiid=calendar-json.googleapis.com,tasks.googleapis.com," +
	"photoslibrary.googleapis.com,photospicker.googleapis.com,youtube.googleapis.com"

// Integrations are what Nina can be given. Her settings page shows two (the user, 2026-11-28): Google, its
// services by the person's choice, and WhatsApp; the rest stay for a machine's own setup (hidden).
func Integrations() []Integration {
	return []Integration{
		{ID: "gemini", Title: "Gemini key", Hidden: true, Why: "her voice, her ears and her tasks: without it she cannot talk",
			Steps: []Step{{
				ID: "key", Title: "A Gemini API key", Input: "secret", URL: "https://aistudio.google.com/apikey",
				Text: "In AI Studio: Create API key, copy it, paste it here.",
				Say: "Open Google AI Studio's page. Sign in with your Google account, press Create API key, " +
					"choose your Nina project if you made one, or let it make one, then copy the key and paste it into my settings.",
				Check: checkGemini, Save: saveKey(func(p Paths) string { return p.Gemini }, ""),
			}}},
		{ID: "google_project", Title: "Google Cloud project", Hidden: true, Why: "the one project her Google sign-in and her YouTube key are made in",
			Status: func(ctx context.Context, s *Setup) Result {
				if s.declared("google_project") {
					return Result{OK: true, Detail: "made (your word: it is checked when her sign-in uses it)"}
				}
				if r := checkClient(ctx, s, ""); r.OK {
					return Result{OK: true, Detail: "an OAuth client is kept: the project is made"}
				}
				if len(s.clients()) > 0 {
					return Result{OK: true, Detail: "OAuth clients found: the project is made"}
				}
				return Result{Detail: "no project seen yet (no OAuth client kept)"}
			},
			Steps: []Step{
				{ID: "have", Title: "Your Google Cloud project", Shortcut: "I have one already",
					URL:  "https://console.cloud.google.com/projectselector2/home/dashboard",
					Text: "Made a project for Nina before (APIs, consent screen, published)? I have one already. If not: Done, and we make one.",
					Say: "My Google services live in one Google Cloud project of yours. If you made one for me before, " +
						"press I have one already. If not, press Done and I will walk you through it: about five minutes."},
				{ID: "create", Title: "Make the project", URL: "https://console.cloud.google.com/projectcreate",
					Text: "Name it Nina, Create, and make sure it is the project selected at the top.",
					Say: "On Google Cloud's page, sign in with your account. If it asks, accept its terms of service. " +
						"Name the project Nina and press Create. When it is made, check that Nina is the project selected at the top of the page, then come back and press Done."},
				{ID: "apis", Title: "Enable her APIs", URL: enableAPIs,
					Text: "Calendar, Tasks, Photos Library, Photos Picker, YouTube Data: Next, then Enable.",
					Say: "This page enables the five APIs I use: Calendar, Tasks, Photos Library, Photos Picker and the YouTube Data API. " +
						"Check that the project is Nina, press Next, then Enable. When it says they are enabled, come back and press Done."},
				{ID: "consent", Title: "Her consent screen", URL: "https://console.cloud.google.com/auth/overview",
					Text: "Get started: app name Nina, your email, Audience External, your email again, agree, Create.",
					Say: "Now the consent screen: what Google shows when you sign me in. Press Get started. Name the app Nina and choose your email as the support email. " +
						"For the audience choose External. Give your email as the contact, agree to the policy, and press Create. Then come back and press Done."},
				{ID: "publish", Title: "Publish the app", URL: "https://console.cloud.google.com/auth/audience",
					Text: "Publish app, Confirm. (In Testing, Google ends her sign-in every seven days.)",
					Say: "On the Audience page, press Publish app and confirm. While an app is in testing, Google ends its sign-ins after seven days, and I would lose your calendar every week. " +
						"Publishing does not make anything public: it is still only your account. Press Done when it says In production."},
			}},
		{ID: "google", Title: "Google", Why: "your calendar, tasks and photos, YouTube's search and Gemini: the ones you choose",
			Status: googleStatus,
			Steps: []Step{
				{ID: "services", Title: "What I may use", Input: "services",
					Text:  "Tick the Google services I may use. Gmail is not available yet.",
					Say:   "Choose which of your Google services I may use, then press Next. Gmail is not available yet: Google keeps sending mail for its partners.",
					Check: checkServices},
				{ID: "client", Title: "Google's personal setup", Input: "client", When: (*Setup).needsSignIn, Page: personalPage,
					Text: "One Google page makes the project and my client: Nina, AI-powered agent ticked, Continue; copy the client ID and secret here.",
					Say: "On Google's personal setup page, choose a project or create one named Nina, name the client Nina, keep Use this client for an AI-powered agent ticked, " +
						"accept the policy and press Continue. Then copy the client ID and the client secret into my settings.",
					Check: checkClient, Save: saveClient},
				{ID: "apis", Title: "Enable the rest", When: func(s *Setup) bool { return len(s.moreAPIs()) > 0 }, Page: apisPage,
					Text: "For Photos, YouTube or Gemini: check the project is yours, Next, Enable; then Done.",
					Say:  "This page enables the APIs for the services you chose beyond the calendar and tasks. Check that the project is the one you just made, press Next, then Enable, and come back to press Done."},
				{ID: "signin", Title: "Sign in with Google", Input: "signin", When: (*Setup).needsSignIn,
					Text:  "Sign in: allow every box. If it says the app is not verified: Advanced, Go to Nina.",
					Say:   "Press Sign in with Google, choose your account and allow every box. If Google says the app is not verified, press Advanced, then Go to Nina.",
					Check: checkGoogle},
				{ID: "key", Title: "An API key", Input: "secret", When: (*Setup).needsKey, URL: "https://console.cloud.google.com/apis/credentials",
					Text: "Credentials: Create credentials, API key; copy it and paste it here. One key serves YouTube and Gemini.",
					Say: "For YouTube and Gemini I need an API key from the same project. On the Credentials page press Create credentials, then API key, " +
						"copy it and paste it into my settings.",
					Check: checkKeys, Save: saveKeys},
			}},
		{ID: "youtube", Title: "YouTube key", Hidden: true, Why: "videos found by search on her screen", Needs: []string{"google_project"}, Optional: true,
			Steps: []Step{{
				ID: "key", Title: "A YouTube API key", Input: "secret", URL: "https://console.cloud.google.com/apis/credentials",
				Text: "Create credentials, API key; restrict it to the YouTube Data API v3; copy, paste here.",
				Say: "In the Credentials page of your Nina project, press Create credentials, then API key. Edit the key: under API restrictions choose Restrict key, " +
					"tick YouTube Data API v3, and save. Then copy the key and paste it into my settings.",
				Check: checkYouTube, Save: saveKey(func(p Paths) string { return p.YouTube }, ""),
			}}},
		{ID: "search", Title: "Web search", Hidden: true, Why: "what she finds on the web: her own search engine first, then Google's through Gemini", Optional: true,
			Status: func(ctx context.Context, s *Setup) Result {
				if r := checkSearXNG(ctx, s, ""); r.OK {
					return r
				}
				if g := checkGemini(ctx, s, ""); g.OK {
					return Result{OK: true, Detail: "Google's search through the Gemini key (no SearXNG of her own)"}
				}
				return Result{Detail: "no search: neither SearXNG nor a Gemini key"}
			},
			Steps: []Step{{
				ID: "searxng", Title: "Her search engine", URL: "https://docs.searxng.org/admin/installation-docker.html",
				Text: "SearXNG on this machine answers? Else her searches go through Gemini (skip).",
				Say: "My web search tries a search engine of my own first, SearXNG, then Google's through the Gemini key. I am checking whether SearXNG answers on this machine. " +
					"If you have none, skip this: Gemini's search is enough.",
				Check: checkSearXNG,
			}}},
		{ID: "openrouter", Title: "OpenRouter key", Hidden: true, Why: "backup models for her tasks when Google's are busy", Optional: true,
			Steps: []Step{{
				ID: "key", Title: "An OpenRouter key", Input: "secret", URL: "https://openrouter.ai/settings/keys",
				Text: "Create key, name Nina, set a weekly limit, copy it at once, paste here.",
				Say: "OpenRouter gives me other models when Google's are busy. Sign in, press Create key, name it Nina, and give it a weekly credit limit, " +
					"so I can never spend more than you allow. Copy the key at once, it is shown only once, and paste it into my settings.",
				Check: checkOpenRouter, Save: saveKey(func(p Paths) string { return p.OpenRouter }, "OPENROUTER_API_KEY"),
			}}},
		{ID: "whatsapp", Title: "WhatsApp", Why: "her own chat on your number: messages to and from her", Optional: true,
			Status: now(checkWhatsApp),
			Steps: []Step{{
				ID: "link", Title: "Link her as a device", Input: "whatsapp",
				Text: "Show the code; on the phone: WhatsApp, Settings, Linked devices, Link a device; scan it.",
				Say: "Press Show the code. Then on your phone open WhatsApp, go to Settings, Linked devices, and press Link a device. Point the phone at the code on my screen. " +
					"When it is linked I will say so.",
				Check: checkWhatsApp,
			}}},
		{ID: "home_assistant", Title: "Home Assistant", Hidden: true, Why: "the home's devices and sensors (lights, climate, music, presence)", Optional: true,
			Steps: []Step{{
				ID: "token", Title: "A long-lived token", Input: "secret", URL: "{hass}/profile/security",
				Text: "Long-lived access tokens: Create token, name Nina, copy it at once, paste here.",
				Say: "In Home Assistant's security page of your profile, scroll to Long-lived access tokens and press Create token. Name it Nina, copy it at once, " +
					"it is shown only once, and paste it into my settings.",
				Check: checkHass, Save: saveKey(func(p Paths) string { return p.HassToken }, ""),
			}}},
	}
}

func checkWhatsApp(_ context.Context, s *Setup, _ string) Result {
	w := s.whatsApp()
	if w == nil {
		return Result{Detail: "WhatsApp is not enabled in her config (whatsapp.enabled)"}
	}
	if w.Linked() {
		return Result{OK: true, Detail: "my device is linked"}
	}
	return Result{Detail: "not linked yet"}
}

// now is a step's check as an integration's status (what is there now).
func now(check func(context.Context, *Setup, string) Result) func(context.Context, *Setup) Result {
	return func(ctx context.Context, s *Setup) Result { return check(ctx, s, "") }
}

// personalPage: Google's personal-mode onboarding (one page: the project, the Workspace APIs and their MCP
// services, an OAuth client for an AI-powered agent, no consent screen to set up): a Desktop client, so its
// sign-in's loopback needs no redirect registered
func personalPage(*Setup) string {
	return "https://console.cloud.google.com/auth/overview/personal?apis=ws,wsmcp,tasksmcp&clientType=desktop&isAgent=true"
}

// apisPage: the console's enable flow for the chosen services' APIs beyond personal mode's
func apisPage(s *Setup) string {
	return "https://console.cloud.google.com/flows/enableapi?apiid=" + strings.Join(s.moreAPIs(), ",")
}

func checkServices(_ context.Context, s *Setup, _ string) Result {
	var titles []string
	for _, id := range s.Chosen() {
		sv, _ := service(id)
		titles = append(titles, sv.Title)
	}
	if len(titles) == 0 {
		return Result{Detail: "nothing chosen", Hint: "Tick at least one of the services."}
	}
	return Result{OK: true, Detail: "chosen: " + strings.Join(titles, ", "), Say: "You chose " + andList(titles) + "."}
}

// checkKeys: the chosen key services (YouTube, Gemini) answer with the key given, or with the ones she has
func checkKeys(ctx context.Context, s *Setup, value string) Result {
	var ok, bad []string
	var hint string
	for _, c := range []struct {
		id    string
		check func(context.Context, *Setup, string) Result
	}{{"youtube", checkYouTube}, {"gemini", checkGemini}} {
		if !s.chose(c.id) {
			continue
		}
		r := c.check(ctx, s, value)
		if r.OK {
			ok = append(ok, r.Detail)
		} else {
			bad = append(bad, r.Detail)
			hint = firstOf(hint, r.Hint)
		}
	}
	if len(bad) > 0 {
		return Result{Detail: strings.Join(append(ok, bad...), "; "), Hint: hint}
	}
	return Result{OK: true, Detail: strings.Join(ok, "; ")}
}

// saveKeys: the key kept for YouTube; for Gemini only where the key she has does not work (a key already
// allocated is kept: her voice runs on it)
func saveKeys(s *Setup, v string) error {
	if s.chose("youtube") {
		if err := writeFile(s.P.YouTube, []byte(v+"\n")); err != nil {
			return err
		}
	}
	if s.chose("gemini") {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if !checkGemini(ctx, s, "").OK {
			if err := writeFile(s.P.Gemini, []byte(v+"\n")); err != nil {
				return err
			}
		}
	}
	return nil
}

// googleStatus: each part the chosen services need, set and working
func googleStatus(ctx context.Context, s *Setup) Result {
	var parts []string
	if s.needsSignIn() {
		r := checkGoogle(ctx, s, "")
		if !r.OK {
			return r
		}
		parts = append(parts, r.Detail)
	}
	if s.needsKey() {
		r := checkKeys(ctx, s, "")
		if !r.OK {
			return r
		}
		parts = append(parts, r.Detail)
	}
	if len(parts) == 0 {
		return Result{Detail: "no Google service chosen"}
	}
	return Result{OK: true, Detail: strings.Join(parts, "; ")}
}

func andList(xs []string) string {
	switch len(xs) {
	case 0:
		return "nothing"
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
