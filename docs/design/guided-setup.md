# Guided setup: her keys, her Google account, her channels (2026-11-28)

The user: until now a Claude agent in Chrome did most of a machine's setup. A person on their own has no
coding agent in their browser, so Nina guides them herself. On her settings page the person adds
integrations one at a time, the way Home Assistant adds them, so a long setup is a few short ones. Each
integration is a loop over its steps:

1. the step's page opens in a new tab (Google Cloud's console, AI Studio, OpenRouter, Home Assistant);
2. Nina says what to do there, word for word (her SAY). The narration goes on while the person works in
   the other tab, and the screen shows only the step's short line (the full text on asking);
3. the step's check proves it (the key answers, the sign-in gives its scopes, the device is linked), and
   a failed check says why and what to try (said, and shown);
4. a step the person cannot do is skipped. That integration stays incomplete, and the others go on.

Package `setup` (nina-live), wired in `app/setup.go`. There are no new HTTP endpoints, so fsttm-live does
not change. The panel's `POST /api/do` takes the actions, and the sticky `settings` event carries the
state in its `setup` field.

## The integrations

| id | what it gives her | page | check |
|---|---|---|---|
| `gemini` | her voice, ears and tasks (first: without it she cannot talk) | aistudio.google.com/apikey | the key lists Gemini's models |
| `google_project` | the one Cloud project her sign-in and YouTube key are made in | console: create, enable APIs, consent screen, publish | the person's word (checked when the sign-in uses it); "I have one already" is one step |
| `google` | calendar, tasks, her albums, without Home Assistant (needs the project) | console: OAuth client (Web, redirect `http://localhost:8765/`), then Sign in | the token's Calendar, Tasks and Photos calls |
| `youtube` | videos found by search (optional) | console: Credentials, API key restricted to YouTube Data v3 | `videoCategories` (1 unit of quota) |
| `search` | the web: her SearXNG first, then Google's through Gemini (optional) | SearXNG's install docs | SearXNG answers JSON |
| `openrouter` | backup models for her tasks (optional) | openrouter.ai/settings/keys, with a weekly limit | `/api/v1/key`, with the limit left |
| `whatsapp` | her own chat on the person's number (optional) | the QR on her screen | the device linked |
| `home_assistant` | the home's devices and sensors (optional) | `{hass}/profile/security` (long-lived token) | `GET /api/` |

The Google project's steps:
1. create it, named Nina;
2. enable Calendar, Tasks, Photos Library, Photos Picker and YouTube Data (one `flows/enableapi` page);
3. the consent screen: Get started, app name Nina, Audience External;
4. publish the app. In Testing, Google ends a sign-in after 7 days.

The OAuth client is a Web application with the redirect `http://localhost:8765/`, the same loopback that
nina-google-auth uses. The sign-in therefore runs in a browser on the machine Nina runs on. From a tablet
or a phone it needs Pascal's tailnet https (later).

## Keys

- Each key is a file. Its path comes from her config: the dialogue's `key:`, `youtube.key`, the
  openrouter fallback's `key`, `services.google.token`, acasa's `hass.token`. Where the config names none,
  it is `~/.config/nina/…`.
- What a machine has is found first, by checking every integration at the start and when the page asks.
  On the sunny day a key that works needs only Next.
- A new key is checked before it is written. It is written with chmod 600, and the file it replaces is
  kept beside it as `.prev`: a key already allocated is never removed.
- Nothing secret goes to her screens or her log (stream `setup`), only set / works and what its check
  found. A key written takes effect at her next start: the page offers Restart, which ends her with exit
  75 so that systemd's `Restart=on-failure` starts her again.

## Actions (`POST /api/do {action, arg}`)

`setup_status`, `setup_add ID`, `setup_value KEY|CLIENT-JSON|file:NAME`, `setup_check`, `setup_done`
(a step without a check, or the shortcut), `setup_next`, `setup_skip`, `setup_back`, `setup_close`,
`setup_services IDS`, `setup_signin`, `setup_signout`, `setup_link`, `setup_say`, `setup_narrate on|off`, `setup_restart`.

The settings event's `setup`:

```
{integrations: [{id, title, why, optional, needs, steps, pages, status: {ok, detail, hint}}], narrate, restart,
 active: {integration, title, step, of, done, skipped, status,
          current: {id, title, text, say, url, input: "" | secret | client | signin | whatsapp, shortcut, manual},
          result: {ok, detail, hint}, clients: [file names], signin: {url, redirect}}}
```

## Google: the personal path, her credential, her choice (claudia, 2026-11-28)

### What Google's personal setup shows (checked 2026-11-28, on the person's own account, not continued)

`console.cloud.google.com/auth/overview/personal?apis=ws,wsmcp,tasksmcp&clientType=desktop&isAgent=true`
opens "Personal mode onboarding": *"This will create an OAuth client that only works for you and does not
require verification."* It offers Select Project / Create a new project (*"A project named
personal-mode-app will be created automatically."*), Application type **Desktop app** (so `clientType=desktop`
takes), the client's Name, *"Use this client for an AI-powered agent"*, and the checkbox *"I agree to the
Google API Services: User Data Policy."* before Continue. The page says nothing of billing or a consent
screen. Still to settle on an EMPTY test account (the person's own browser): what Continue creates, the
client's ID and secret shown, whether the sign-in then warns, and whether its refresh token outlives 7 days
(the token file records `granted`; check it on or after 2026-12-05 and write the dated result here).

The client step accepts only a Desktop app client (the personal path's): a Web client pasted is refused, and
a Web client kept from an older setup shows as not this step's, never as a ✓.

### Her credential: package `creds`

One `creds.Store` (at `services.google.token`, default `~/.config/nina/google-token.json`) is the only reader
and writer of the token file. Her Google backends (`services/google.FromSource`), the setup's checks, sign-in
and sign-out, `nina-photos` and `nina-google-auth` all go through it. It refreshes access tokens (one call an
hour), learns the granted scopes from Google's answers, and reports `Health`: `ok`, `none`, `invalid_grant`,
`scope_missing` (a chosen service's scope not granted), `error`. The file: chmod 600, written atomically, the
one it replaces kept as `.prev`; JSON `{client_id, client_secret, refresh_token, scopes, account, granted}`
(the first three as before). No token reaches her screens, her log, the rules or the LLMs: the Store gives
access tokens to HTTP clients only.

### The sign-in

The loopback `http://localhost:8765/` (a Desktop client needs no registered redirect), with PKCE (S256),
`access_type=offline`, `prompt=consent` and `include_granted_scopes=true`. It asks for the chosen services'
scopes plus `openid email` (the account shown on the card). Signed in already with the same client, ticking a
service asks only for its missing scopes; the grant kept is the union. On the tablet the card opens the page
in its own browser (`NinaApp.open`), and the redirect comes back to the tablet's persona on its loopback.

### The choice turns her backends on

At her start (`app.New`), the services last chosen (her log, stream `setup`, kind `services`) put the agenda on
Google (Calendar or Tasks: `services.agenda: google`, with `agenda.GoogleDefault` handles and the primary
calendar when her config has none) and her albums on Google (Photos), only with a sign-in kept and only where
her config names no backend. Nothing chosen, or no sign-in: as before (Home Assistant's, or none). A choice or
sign-in made later takes effect at her next start (the page offers Restart).

### Sign out

`setup_signout`: the refresh token revoked at Google (`oauth2.googleapis.com/revoke`), the file kept as
`.prev`, her screens told (`setup.google: {signed_in, account, scopes, granted, prev}`), Restart offered so her
backends drop it.

## Not yet

- Gmail (a restricted scope: Google's verification).
- The sign-in from another device's browser (the tailnet's https redirect).
- The 7-day refresh-token result in personal mode (see above: on or after 2026-12-05).
