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
`setup_signin`, `setup_link`, `setup_say`, `setup_narrate on|off`, `setup_restart`.

The settings event's `setup`:

```
{integrations: [{id, title, why, optional, needs, steps, status: {ok, detail, hint}}], narrate, restart,
 active: {integration, title, step, of, done, skipped, status,
          current: {id, title, text, say, url, input: "" | secret | client | signin | whatsapp, shortcut, manual},
          result: {ok, detail, hint}, clients: [file names], signin: {url, redirect}}}
```

## Not yet

- Gmail (a restricted scope: Google's verification).
- The sign-in from another device's browser (the tailnet's https redirect).
- Switching her backends to Google (`services.agenda: google`, `photos: google`) from the page: her config
  still says it (the agenda and photos stay on Home Assistant until it does).
