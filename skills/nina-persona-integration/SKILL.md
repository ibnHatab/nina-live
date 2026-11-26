---
name: nina-persona-integration
description: How Nina the persona (nina-live) plugs into a deployment — her two config files (the core's nina.yaml and the dialogue's yaml), the Home Assistant client and MCP servers, her tools and keys, the panel's HTTP/SSE contract that screens and cards use, and which repo owns what. Use when wiring Nina into a new home or role, adding a config section, writing a screen or card that listens to her, or deciding whether a change belongs in nina-live, fsttm-live or acasa.
---

# Integrating Nina the persona

## Who owns what

| repo | role | change it for |
|---|---|---|
| **nina-live** | the persona: `cmd/nina`, the event log, the rules (`catalog/domains`), her tools (agenda, timers, music, screen, whatsapp, youtube, tasks) | how she reasons, what she can do |
| **fsttm-live** | her voice: floor machine, attention, wake word, Gemini Live ears and voice, the panel (web plugin) | how she hears, speaks, takes turns |
| **acasa** | the adaptation to a home or role: Home Assistant (`hass/`), dashboards and cards (`ha-ui/`), kiosk, machine install/update, per-machine configs (`live/`) | where she lives and how she looks there |

A new user/role is mostly acasa work (configs, dashboards); nina-live changes only when she needs a new
capability. Planned: an external domain catalog, so a role can bring its own domains.

## Her configs (kept in acasa `live/`, per machine)

- **core** `nina-HOST.yaml` (desktop: `nina.yaml`), passed as `bin/nina -config FILE`:
  `hass` (url + token *file*), `dialogue` (path to the dialogue yaml), `persona`/`voice` (prompts),
  `tasks` (model, `fallback` models, key file), `search`, `mcp` (Home Assistant's MCP servers:
  `assist`, `hacfg` via ha-mcp), `agenda` (target handle → entity), `music` (levels, `languages`),
  `youtube` (`key` file, `playlists` by name), `whatsapp` (`enabled`, `answer`, `keep`, `show`,
  throttles), `levels`.
- **dialogue** `HOST.yaml` (fsttm-live's): `models` (`ears`, `mind`, `vocab`), `audio` (device, out,
  levels, VAD), `dialog.wake` (local spotter), `prefs`, `plugins.web` (the panel: `static`,
  `remote`, `allow_origin`).
- Secrets are **files** (`~/.config/fsttm/*.key|token`, `~/.config/nina/*.key`), referenced by path;
  never inline (see acasa's credential skills).

## The panel: what screens and cards consume (fsttm-live web plugin, :8770)

- `GET /api/events` (SSE): `ev` events (her dialog: `heard`, `turn`, `tool`, `reply_done`, `said`,
  `attention`, `stage`, …) and `status` (floor, awake, speaking). Draw `turn.text` only (the person's
  words); `turn.context` is her internal note — never show it.
- `POST /api/do {"action": …}`: `talk`, `wake`, `sleep`, `stop`, `mic`, `voice`, `ui`, `type`.
- `GET /api/state`: facts and settings (e.g. `facts.whatsapp.linked`).
- `GET /api/audio` (websocket): a remote microphone/voice (her screen on a phone; wake-cycle).
- `stage` events (from her `screen` tool): `show: videos|photos|close|next|previous|pause|play`, with
  `videos` (ids), `playlist`, `album`, `title`. `attention {awake:false, told:true}` means she was
  told to sleep (screens close what they show); without `told` she dozed off (keep it).

## Home Assistant as her tool

`hass.New(url, tokenFile)`: REST service calls (`Service(ctx, domain, service, data, response)`),
the websocket feed (states → her world), media-source browsing (Google Photos albums). Calls that
need a response use `?return_response` (e.g. `todo.get_items`). Assist/MCP gives her generic
Home Assistant tools; the rules' hosts use direct service calls.

## Adding a capability: checklist

1. Which repo (table above)? Voice/turn-taking → fsttm-live; a tool or rule → nina-live; how it
   looks or which entity → acasa.
2. Config: a new section in the core yaml with a safe default when absent; a secret by file path.
3. Events: if screens should react, emit an event the panel forwards (fsttm-live web `shown` map) and
   document its fields here.
4. Test silently (nina-persona-testing), deploy with acasa's update.
