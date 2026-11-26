---
name: nina-persona-domains
description: Add or change one of Nina's rule domains in nina-live's Metis catalog (catalog/domains/<pkg>: the .llp rules, intent.yaml verbs/slots/handles, domain.yaml, the Go host that performs the effects) and test it. Use when teaching Nina a new kind of request (an alarm, removing a list item, a new calendar or list), when the voice model routes a request to the wrong tool or the wrong slot, or when a tool reports success that did not happen.
---

# Changing Nina's domains (the rules)

Each domain is a Metis package in `catalog/domains/<pkg>/`, served as one composition
(`catalog/domains/composition.yaml`: `specialists:`). Her voice model sees each package as a tool
`home_<pkg>` whose parameters are the package's `intent.yaml` slots and whose description is
`domain.yaml`'s `line:`.

| file | what it holds |
|---|---|
| `<pkg>.llp` | types, facts (`bwd`), preds, rules: `name [dyn] : premises -o conclusions.` with a `%%` sentence (what she says) above each |
| `intent.yaml` | `verbs` (what can be asked), `slots`, `guidance`, `examples`, `map` (verb → command atom), `handles` (spoken names of each target) |
| `domain.yaml` | `line:` (the tool description), `commands:` (rules the host acts on), `program`, `intent` |
| host (Go) | `nina-live/<pkg>/` (agenda, timers, music…): `OnEvent(rule, args, payload)` performs the effect |

The rules decide *whether*; the host does *what* (a Home Assistant call) with the payload the voice
sent. A host failure asserts `failed(T)` and a rule says it ("It did not reach your tasks").

## Recipe: a new verb (e.g. REMOVE on lists, ALARM on timers)

1. **llp**: `pred cmd_remove(target) : home.`, an outcome pred, and the rules — the allowed case and
   every refusal (`remove_cal [dyn] : cmd_remove(L) * $calendar(L) -o refused(agenda).`).
2. **domain.yaml**: add the rule name to `commands:` so the host hears it.
3. **intent.yaml**: the verb's `meaning`, any new slot, `guidance`, 2–3 `examples` in the person's
   words, and `map` lines — the slot form first, then the default:
   `{verb: REMOVE, atom: "cmd_remove({list:target})"}`, `{verb: REMOVE, atom: "cmd_remove(my_tasks)"}`.
4. **host**: handle the rule name in `OnEvent`; validate the payload (an empty summary is an error,
   not a default item).
5. **image**: `./build.sh`; a meant hash change → `image.golden` (see nina-persona-build).
6. **tests**: a core rig test through `r.k.Home.Call(ctx, "home_<pkg>", args)` with a fake host `Call`
   (assert the exact service, entity and fields), plus unit tests for parsing; run with METISC.
7. **live**: a wake-cycle script (nina-persona-testing), then read the `tool` events' `args`.

## The voice model does not fill slots the way the card says

Observed with Gemini Live, and fixed in the rules/host rather than hoped away:
- it put a **list** in `calendar` (`calendar: "shopping"`) → map `cmd_todo({calendar:target})` too;
- it put an event's **time** in `due` → the host reads `start`, else `due`;
- it sent times as `"tomorrow 13:00"` → the host's `parseTime` reads today/tomorrow HH:MM;
- with no verb for a request it picks the **nearest tool** ("set an alarm" → the night scene!) →
  add the verb and name it in `domain.yaml`'s `line:` (the tool description);
- an empty query became a TODO with no summary → the host refuses empty items;
- **ambiguous handle names** across targets ("your calendar" for hers, "local") sent "my own
  calendar" to the test calendar → make spoken names distinct; say in `guidance` which wins.

So after any change, read what she actually sent: `sqlite3 ~/.local/share/nina/nina.db "select
data from events where kind='tool' order by seq desc limit 5"` and the host's own stream
(`stream='agenda'`).

## Targets come from config, not code

A handle (`my_tasks`, `shopping`, `nina_cal`) maps to a Home Assistant entity in the machine's
`nina.yaml` `agenda:` (or the host's `Default`). A target with no entity on a machine fails at the
host and is said ("It did not reach the shopping list") — fine for a role that lacks it (an office
has no shopping list). Calendars the person owns are readable, never writable (`person_cal`: refused).

## Home Assistant details that bit

- `todo.remove_item` needs the item's exact summary: look it up (`todo.get_items`, response
  service) and match case/punctuation-insensitively (agenda `Match`).
- Home Assistant's own Shopping list rejects `description` (HTTP 400): retry without Nina's mark.
- `calendar.create_event` takes local `start_date_time`/`end_date_time` strings; deleting an event is
  websocket-only (`calendar/event/delete` with its uid).
