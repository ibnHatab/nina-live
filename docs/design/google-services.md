# Design: Google services inside Nina's loop, without Home Assistant

Status: **proposal for review** (claudia → nina-lead, 2026-11-27). Depends on
[channel-streamlining.md](channel-streamlining.md) (the three channels) and lands under its
measurement plan: **measured against today's behaviour, no regression merged.**

## 1. Motivation

- **The persona must run where there is no Home Assistant.** Nina now runs on an Android tablet
  (fsttm-live's app, nina-live `mobile/`, 2026-11-27): her voice, pi's task loop and the composition
  all run on a Fire 7. A tablet has no place for Home Assistant, yet the services she uses most
  (her calendar, the person's tasks, the weather) reach her today only through it.
- **Home Assistant is a gateway, not a need, for these services.** Its Google integration mostly saves
  the OAuth registration; the calls behind it are plain REST APIs that an agent can make itself.
- **The dependency points the wrong way.** `nina-live/cmd/nina` imports `acasa/hass`: the persona
  depends on one of its own adaptations. The persona/deployment split (2026-11-27) separated the
  builds but not the code.
- **The loop should see every action.** Done inside a Metis package, a calendar entry is a command the
  rules judge, the world predicts and a surprise can contradict. Done through an MCP server or a
  tool that calls Home Assistant directly, it is invisible to all three.

## 2. What goes through Home Assistant today

| Use | Today (nina-live / acasa) | Kind |
|---|---|---|
| Device commands (lights, air, media, comfort, scenes) | `hass.NewDevice` | home only |
| The world's feed (state websocket) | `ha.Follow` → `obs.Feed(k.World.Apply)` | home only |
| Radar presence | `/api/states/sensor.nina_viewer` | home only |
| Glow (the Echo silenced) | `script.alexa_quiet` | home only |
| Her persona on the dashboard | POST `sensor.nina_persona` | home only |
| People, location | `person`, `device_tracker` observed | home only |
| MCP (Assist, ha-mcp) | `hatools.Dial` | home only |
| **Google Calendar** (hers, the person's) | `calendar.*`: `get_events`, create services (agenda) | **service** |
| **Google Tasks** (`todo.my_tasks`) | `todo.add_item/remove_item/get_items` (agenda) | **service** |
| Local to-do, shopping list, local calendar | Home Assistant's local integrations | service (replaceable) |
| **Weather** | `weather.get_forecasts` (Met.no) | **service** |
| **Google Photos albums** | `media_source/browse_media` (`cmd/nina/albums.go`) | service, see 6 |
| Music | `media_player.music_player_daemon` (MPD) | home / device |

Already direct: WhatsApp (whatsmeow), YouTube (Data API key), models (Gemini, OpenRouter), web
search (SearXNG), timers and alarms (Nina's own host).

## 3. Where a service goes (the proper place)

A service touches all three channels of the streamlining design, so it has a place in each:

| Part | Place | Why |
|---|---|---|
| Actions (add an event, add or remove a task) | the Metis package's **Go host** (`agenda`), its backend calling the service | one path for voice and pi; refusals with reasons; the world's prediction; surprises |
| State (today's events, open tasks, the weather) | **observations into the semantic world** (as `hass.Observer` does now) | both LLMs see it through the view port without calling anything |
| Open-ended reads ("what is on Friday afternoon?") | **read tools for pi** (as `lists`/`calendars` now) | reads change nothing: no rule needed |
| Anything without a package | **MCP, the residual**, logged `uncovered` | a measurement of missing domains, not a way to integrate |

**Not MCP for our own services:** a process boundary inside one Go binary (Android runs no
subprocesses), and calls the rules and the world never see.

## 4. Structure

1. **Interfaces in nina-live**, beside the packages that use them (the packages and their rules do
   not change, only the far end):
   - `agenda.Backend`: Events, AddEvent, Items, AddItem, RemoveItem;
   - `weather.Source`: Now, Forecast;
   - `photos.Source`: Albums, Items;
   - `music.Player`: Play(url), Stop, Volume;
   - `world.Observer`: lines into the world.
2. **`nina-live/app`**: the assembly now in `cmd/nina/main.go` (core, dialogue, tasks, WhatsApp, the
   clock, the panel) as a library that takes the backends.
3. **Backends:**
   - direct, in nina-live: `services/google` (Calendar, Tasks), `services/weather` (Open-Meteo or
     Met.no, no key);
   - Home Assistant, in acasa: `hass` implements the same interfaces, plus the home-only parts.
4. **Builds:** acasa's `cmd/nina` (moved there: app + Home Assistant backends + devices; pero and
   Pascal); nina-live `mobile` (app + direct backends; the tablet); mixed at home (Calendar direct,
   devices through Home Assistant).

## 5. Google Calendar and Tasks

- **API:** Calendar v3 (events list/insert/delete), Tasks v1 (tasklists, tasks insert/patch/delete).
- **OAuth:** the person's own OAuth client (skill `acasa-cred-google-oauth`), scopes
  `calendar.events` and `tasks`. The sign-in cannot happen in the tablet's WebView (Google blocks
  OAuth in embedded browsers) and the limited-input device flow does not allow these scopes, so:
  one loopback sign-in on Pascal, the refresh token stored as a credential file
  (`~/.config/nina/google-token.json`, chmod 600), put on a device as the keys are. A new skill
  `nina-cred-google` (nina-live `skills/`) describes it.
- **agenda's backend**: the entity names in `agenda.Default` (`calendar.nina`, `todo.my_tasks`, …)
  become backend-neutral handles (nina's calendar, the person's calendar, my tasks, shopping); the
  Home Assistant backend maps them to entities, the Google one to calendar and task-list ids.
- **Observations:** today's and tomorrow's events and the open tasks, polled (5 min) into the world as
  the observer's lines are now.
- **Local lists:** the shopping list and Nina's own to-do become Google Tasks lists, or entries in her
  event log (her own store) where no Google account is linked.

## 6. The other services

| Service | Backend | Note |
|---|---|---|
| Weather | `services/weather`, Open-Meteo or Met.no | an observation every 30 min, a forecast read tool |
| WhatsApp | `whatsapp/` as is | on the tablet: linked by QR on her panel, its store in the app's private files |
| Music | home: MPD through Home Assistant; tablet: `music.Player` → the app's bridge plays the stream (Android MediaPlayer) | a `Media(url, volume)` method on fsttm-live's bridge |
| Photos | **to decide** | Google restricted the Photos Library API in 2025: an app reads only its own uploads, or what the person picks in the Picker. Options: a Google Drive folder (Drive API, `drive.readonly`), the Picker, or Home Assistant's integration at home only |
| Web search | Gemini grounding on the tablet; SearXNG at home | already behind the `websearch` tool |

## 7. Steps and measurement

| Step | Change | Admitted when |
|---|---|---|
| 1 | Interfaces + `app` assembly; acasa's `hass` implements them; `cmd/nina` moved to acasa | pero and Pascal behave as before: nina-live and fsttm-live suites green, the media battery (s-21866507) passes, SITL metrics within the baseline's noise |
| 2 | `services/google` (Calendar, Tasks); Pascal on the direct backend instead of Home Assistant's | the agenda tests (add, remove, read, the shopping list) pass against both backends; SITL agenda scenarios ≥ baseline; a day's canary on Pascal |
| 3 | Weather direct; WhatsApp on the tablet | weather lines equal Home Assistant's (same hour, same place); the self-chat test passes on the tablet |
| 4 | Music through the bridge; Photos once decided | the media battery on the tablet's subset |
| 5 | Fakes of every backend for the SITL | the SITL runs with no network service |

Every backend has a fake (for the SITL and the tests). Each step's report: the commit, the metrics
against the baseline, the verdict.

## 8. Decisions needed

1. Moving `cmd/nina` into acasa changes how pero and Pascal are built (`persona-build.sh`, acasa's
   `update.sh`): agreed, behind the gates above?
2. Photos: Drive folder, Picker, or Home Assistant at home only?
3. The user sets up the OAuth client and does the one-time sign-in on Pascal ("I manage", 2026-11-27).
