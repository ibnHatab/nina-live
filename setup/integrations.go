package setup

import "context"

// The Google Cloud APIs her project enables, in one page of Google's (the console's enable flow).
const enableAPIs = "https://console.cloud.google.com/flows/enableapi?apiid=calendar-json.googleapis.com,tasks.googleapis.com," +
	"photoslibrary.googleapis.com,photospicker.googleapis.com,youtube.googleapis.com"

// Integrations are what Nina can be given, in the order a new machine takes them: her voice's key first
// (without it she cannot speak, and the rest is narrated), then her Google project and what is made in it,
// then her channels.
func Integrations() []Integration {
	return []Integration{
		{ID: "gemini", Title: "Gemini key", Why: "her voice, her ears and her tasks: without it she cannot talk",
			Steps: []Step{{
				ID: "key", Title: "A Gemini API key", Input: "secret", URL: "https://aistudio.google.com/apikey",
				Text: "In AI Studio: Create API key, copy it, paste it here.",
				Say: "Open Google AI Studio's page. Sign in with your Google account, press Create API key, " +
					"choose your Nina project if you made one, or let it make one, then copy the key and paste it into my settings.",
				Check: checkGemini, Save: saveKey(func(p Paths) string { return p.Gemini }, ""),
			}}},
		{ID: "google_project", Title: "Google Cloud project", Why: "the one project her Google sign-in and her YouTube key are made in",
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
		{ID: "google", Title: "Google account", Why: "her calendar and tasks, her photo albums, without Home Assistant",
			Needs: []string{"google_project"}, Status: now(checkGoogle),
			Steps: []Step{
				{ID: "client", Title: "Her OAuth client", Input: "client", URL: "https://console.cloud.google.com/auth/clients/create",
					Text: "Web application, name Nina, redirect http://localhost:8765/, Create, Download JSON, paste it here (or pick one found).",
					Say: "Now the key to your account: an OAuth client. On its page choose Web application and name it Nina. Under Authorized redirect URIs press Add URI " +
						"and type http colon slash slash localhost colon eight seven six five slash. Press Create, then Download JSON. Open that file and paste all of it into my settings. " +
						"If you made one before, you can pick it from the list instead.",
					Check: checkClient, Save: saveClient},
				{ID: "signin", Title: "Sign in with Google", Input: "signin",
					Text: "Sign in: allow every box. If it says the app is not verified: Advanced, Go to Nina.",
					Say: "Press Sign in with Google, choose your account and allow every box. If Google says the app is not verified, press Advanced, then Go to Nina.",
					Check: checkGoogle},
			}},
		{ID: "youtube", Title: "YouTube key", Why: "videos found by search on her screen", Needs: []string{"google_project"}, Optional: true,
			Steps: []Step{{
				ID: "key", Title: "A YouTube API key", Input: "secret", URL: "https://console.cloud.google.com/apis/credentials",
				Text: "Create credentials, API key; restrict it to the YouTube Data API v3; copy, paste here.",
				Say: "In the Credentials page of your Nina project, press Create credentials, then API key. Edit the key: under API restrictions choose Restrict key, " +
					"tick YouTube Data API v3, and save. Then copy the key and paste it into my settings.",
				Check: checkYouTube, Save: saveKey(func(p Paths) string { return p.YouTube }, ""),
			}}},
		{ID: "search", Title: "Web search", Why: "what she finds on the web: her own search engine first, then Google's through Gemini", Optional: true,
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
		{ID: "openrouter", Title: "OpenRouter key", Why: "backup models for her tasks when Google's are busy", Optional: true,
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
		{ID: "home_assistant", Title: "Home Assistant", Why: "the home's devices and sensors (lights, climate, music, presence)", Optional: true,
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
