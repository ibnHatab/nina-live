# Nina's domains: the catalog and its progress

Nina's domains are the Metis packages of her one composition (`catalog/domains/`, the director over
them). This page says which domains Nina should have, in what order, how each would be encoded, and
where each one stands. It began as a draft on 2026-11-24 and is updated as domains land.

The goal: everyday usefulness in the home (an Alexa replacement first), autonomous execution of
open-ended goals (what OpenClaw does), and conversation worth having. Underneath sits Metis's
persistent symbolic model of the person's world.

The voice runs on a Flash model, so what to encode matters. Structured state, procedures,
relationships and constraints held in Metis make up for a smaller model's limits. Then a larger
model isn't needed for every turn.

## Where things stand

| | Domains | State |
|---|---|---|
| **Running** | `home` (lights, air, media, scenes, comfort), under the director | composed and on the tick since 2026-11-24 |
| **Partly** | `memory` (episodic), `tasks` (System 2) | the log's FTS5 episodes, a `recall` tool for tasks; pi tasks from `delegate`. No Metis domain for either yet |
| **Composed** | `timers` (with reminders for leaving, bedtime, coming back) | under the director, its host in nina-live (metis-agent bf4ba97 takes it, with the call's payload and the handles' names) |
| **Next** | `media` (music) | the rest of release 0.1 |
| **Planned** | the rest of the catalog | |

### Today's home in detail

| Package | What it does | Real devices on Pascal |
|---|---|---|
| `lights` | on, off; a scene's sweep; off when a room is vacant | none in Home Assistant yet (the rules say so) |
| `air` | the two Prana units: on, off, night, boost, normal, heater, winter; a scene's sweep; advice when it is cold outside | both units, through acasa's hass (one fan per call; boost capped at 10 min) |
| `media` | the TV: on, off, volume, IR keys; a scene's sweep | the TV is not in Home Assistant yet |
| `scenes` | home, night, away; away suggested when the home empties, the night's end at dawn | no devices: it puts out the scene's wishes, routed by the director |
| `comfort` | a room's temperature and humidity, advice | no thermometers yet: "the lab has no thermometer yet" |
| `director` | routes the scene's wishes (`scene_lights`, `scene_air`, `scene_media`) to their packages | none (its effects are the agent's verbs) |

## The catalog

The domains fall into three worlds and one cross-cutting group. "Metis gain" is how much the domain
gains from symbolic encoding, compared with leaving its reasoning to Flash.

### The physical world

| Domain | What it covers | Metis gain | Priority | State |
|---|---|---|---|---|
| `home` | lights, ventilation, appliances, scenes | very high | P0 | running |
| `media` | music, internet radio, podcasts, audiobooks, the TV | very high | P0 | the TV in `home/media`; music planned |
| `weather` | forecasts, warnings, the outdoors | high | P0 | planned (`sun` and the Prana's outside sensor are in the world already) |
| `comfort` | temperature, humidity, air quality, preferences | very high | P1 | in `home`, no sensors yet |
| `inventory` | food, consumables, supplies | very high | P1 | planned |
| `cooking` | recipes, ingredients, steps with their timers | very high | P1 | planned |
| `shopping` | lists, products wanted | high | P1 | planned |
| `energy` | consumption, tariffs, when to run things | very high | P2 | planned |
| `maintenance` | filters, servicing, repairs | very high | P2 | planned (the Prana's filters first) |
| `pets` | feeding, walks, appointments, supplies | very high | P2 | planned |

### The personal world

| Domain | What it covers | Metis gain | Priority | State |
|---|---|---|---|---|
| `timers` | timers, alarms, countdowns; reminders for when the home goes away, to night, back home | very high | P0 | composed (2026-11-24): `catalog/domains/timers`, its host `timers/` rebuilt from the log; the director routes a scene's change to it; integration-tested, not yet tried by voice |
| `reminders` | by time, and by condition ("when I go shopping") | very high | P0 | planned |
| `calendar` | appointments, availability, travel time | very high | P0 | planned |
| `tasks` | to-dos, priorities, dependencies | very high | P0 | System 2 runs; the to-do domain is planned |
| `memory` | preferences, decisions, what was discussed | high | P0 | episodic: the log (FTS5), `recall`; semantic: planned |
| `commitments` | promises, things left unfinished | very high | P1 | planned |
| `briefing` | the morning and evening summary | high | P1 | planned |
| `people` | contacts, relationships, birthdays | high | P1 | planned |
| `communication` | email, messages: summaries, drafts | high | P1 | planned (reading first; sending asks the person) |
| `notes` | ideas captured, found again | medium | P1 | planned |
| `travel` | trips, transport, itineraries | very high | P2 | planned |
| `finance` | bills, subscriptions, budgets | very high | P2 | planned |
| `wellbeing` | activity, sleep, the reminders the person sets | high | P2 | planned |
| `documents` | finding and filing documents | medium | P2 | planned |

### The intellectual world

Nina lives in the home and talks, so this world matters more for her than for a messaging agent.

| Domain | What it covers | Metis gain | Priority | State |
|---|---|---|---|---|
| `conversation` | discussion, follow-ups in context | medium | P0 | the voice's own (Gemini Live); continuity from `memory` |
| `news` | the stories the person follows and how they develop | high | P0 | planned |
| `interests` | what the person cares about: what Nina should pay attention to | very high | P1 | planned |
| `projects` | engineering projects: decisions, open questions, progress | very high | P1 | planned |
| `knowledge` | facts, explanations | medium | P1 | the models' own; nothing to encode |
| `research` | longer investigations, comparing sources | medium | P1 | System 2 (pi), with web tools to come |
| `learning` | tutoring, progress, exercises | high | P1 | planned |
| `books` | reading, recommendations, discussion | high | P1 | planned |
| `language` | practice in another language (Nina is multilingual) | high | P2 | planned |
| `games` | quizzes, puzzles, interactive stories | very high | P2 | planned |
| `culture`, `debate` | films, music, history; structured argument | medium | P2 | planned |

### Across the worlds: autonomy

These are capabilities over the other domains more than domains of their own. None needs its own
process or plugin.

| Capability | What it does | Where it lives | State |
|---|---|---|---|
| `routines` | morning, evening, contextual sequences | rules plus the scheduler; pi for the open-ended steps | planned |
| `monitoring` | watching outside conditions and changes | hosts feeding the world | the home's feed only |
| `notifications` | whether a thing is worth saying, and when | Metis decides whether it is worth saying; FSTTM decides when she may speak | levels per kind today (`nina.yaml` levels); the world through the quantized port (`port/`: a line passed on at a new value or confidence level); a domain contract planned (below) |
| `goals`, `opportunities` | long-term objectives; relevant events, products, articles | rules over interests, projects, news | planned |
| `diagnostics` | finding why something failed or disagrees | the world's surprises plus pi | the semantic world records surprises |
| `refinement` | new rules learned from surprises | metis-loop's gates; the conversations kept for mining (ARCHITECTURE F7) | the log keeps everything |

Not first: browser automation, sending email, buying anything. They are attractive and complex,
and less central to the voice.

## The first ten

| Rank | Domain | Effort | Daily use | Metis gain |
|---:|---|---|---|---|
| 1 | time and reminders | low | very high | very high |
| 2 | media (music, radio) | low to medium | very high | very high |
| 3 | calendar | medium | very high | very high |
| 4 | weather | low | high | high |
| 5 | news | medium | high | high |
| 6 | memory (semantic) | medium to high | very high | very high |
| 7 | tasks (to-dos) | low to medium | very high | very high |
| 8 | interests and conversation | medium | very high | high |
| 9 | projects | medium | high | very high |
| 10 | cooking and shopping | medium | high | very high |

## Who reasons about what

| Responsibility | Who |
|---|---|
| understanding the person's words, resolving ambiguity | Flash (Gemini Live) |
| the structured world's state; the domain's constraints; resources consumed; lifecycles | Metis |
| noticing a change that matters | Metis (its conclusions, the world's surprises) |
| fetching outside information; open-ended work | pi (System 2) with Flash |
| proposing something new | Flash; Metis checks it |
| saying it naturally | Flash |
| finding what the domains do not know | the surprises, mined (metis-loop) |

General world knowledge is not encoded. The persistent state, the relationships and the rules that
change what Nina does are.

## How a domain would be encoded

The sketches below are illustrative, not checked Metis syntax. Each real package goes through
`metisc --link` and the composition's checks. The linker's rules learnt so far:
- every rule has a `%%` line;
- a member never consumes its own `out`: the agent retracts a crossed signal;
- a type name means one thing across the members;
- generated include files are named per package;
- the capacity declares `type domain`.

### Time and reminders

The best first linear-logic domain: it is all about lifecycles.

```text
timer(T, name)            % running → paused → expired → acknowledged
reminder(R, subject)      % its trigger: a time, or a condition another domain concludes
alarm(A, schedule)
```

The scheduler writes time as events (a stream in the log, so a replay sees it), and Metis turns
them into the reminders' transitions. No model watches the clock.

- "Remind me in twenty minutes to check the printer."
- "Another five minutes."
- "Remind me to buy batteries when I go shopping." This is a condition: the trigger is a conclusion
  of `shopping`.

**As built (2026-11-24, `catalog/domains/timers/timers.llp`).** Six slots `k1`..`k6`: `free →
counting | waiting(T, S) → free`, taken by the rules so two timers never share one. The host
(`timers/`) holds the minutes, the subject and the deadline, which come with the call as its
payload. It asserts `due(T)` when the time has come, `full` when no slot is free, and `held(T)`
after a restart; a slot's name in a line is its subject. A reminder waits for a scene: `scenes`
emits `scene_timers(S)` with its other wishes and the director routes it here, so "remind me to
take the keys when I leave" fires on the away mode. The voice keeps "timers set: ..." as silent
context. One stage: a cycle of stage links would spin, so what lasts one beat (a cancel's
`clearing`, a scene's wish) is retracted by the host on the next. Still to do: "another five
minutes", alarms at a clock time, a timer named in a cancel.

### Media

```text
playing(source, item)   paused(source, position)   volume(level)
favourite(station)      preferred(genre)           queued(item, position)
```

Hosts: MPD (playback and the queue), the Radio Browser API, Audiobookshelf, Home Assistant's media
players, Jellyfin. Metis keeps the playback state and resolves "the station from yesterday".

With the floor machine, while the person holds the floor, the media rule lowers the volume, and it
puts it back when the conversation ends. That needs no model.

### Calendar

```text
event(E, start, end, place)   busy(interval)   travel_time(from, to, minutes)   margin(minutes)
```

"Remind me to leave before my appointment" becomes a reminder at the appointment's start, minus the
travel time, minus the person's margin. The calendar's host fetches the facts; Metis holds the
constraints.

### News

```text
interest(topic, weight)   story(S, topic)   revision(S, n)   told(S, n)   source_trusted(src)
```

The rule that matters tells a new story apart from an update to a known one, from one already
discussed, and from contradictory reports. pi gathers; Metis keeps the stories' lifecycle; Flash
tells it.

### Memory

| Kind | Where | Example |
|---|---|---|
| episodic | the log (SQLite, FTS5): every event | what we discussed yesterday |
| semantic | Metis facts with their source, confidence and age (the semantic world's beliefs) | the person prefers hard science fiction |
| procedural | rules, and pi's skills | how the morning briefing is made |

A preference Nina inferred is never mistaken for one the person stated: each fact carries its
source, as every belief in the semantic world already does (sensor, command, person, metis).

### Interests

This domain is separate from memory. Memory is what Nina knows; interests are what she should pay
attention to.

```text
interest(topic, weight)   active(topic)   discussed(topic, when)   open_question(topic)   depth(topic, level)
```

"Yesterday we discussed camera-only BEV perception; there is a new lightweight architecture." That
is said only when novelty, relevance and the narration level allow it, and when the floor is free.

### Tasks and commitments

```text
todo(T) pending → running → blocked → done    depends_on(T1, T2)    deadline(T, when)
```

"When the print finishes, remind me to check the dimensions" becomes a dependency between a
printer's event and a to-do, not a question asked of the model again and again. pi carries out the
to-dos that need investigation; Metis keeps their lifecycle.

### Projects

```text
project(P, status)   goal(P, g)   decided(P, subject, value)   constraint(P, c)   open(P, question)   depends(P, component)
```

"Where did we stop with the wheelchair perception project?" "The camera geometry is settled; the
temporal BEV architecture and its cost are still open." The sources: GitHub, the project's
documents, past conversations.

### Cooking and inventory

Ingredients are resources consumed, which is linear logic's home ground.

```text
have(item, qty)   needs(recipe, item, qty)   step(recipe, n)   step_done(recipe, n)
```

What can be cooked, what is missing, which steps can run at once, and when a timer starts.

## Across domains

The value is in the relations between domains:

| Domains | What follows |
|---|---|
| calendar + weather | an outdoor appointment and the rain |
| calendar + tasks | what to prepare before a meeting |
| cooking + inventory | meals from what is there |
| inventory + shopping | the shopping list kept up |
| home + energy | when to run the appliances |
| interests + news | the developments worth raising |
| projects + research | the research relevant to a project |
| media + the floor | the music lowered while they talk |
| calendar + pets | the vet's appointment |
| memory + conversation | continuity across days |

In the composition, a relation is an interface package between the members, and the director routes
its kinds, the way `iface/home.llp` carries a scene's wishes today. Flash doesn't rebuild the
relation from a long context each time.

## A domain package

Every domain is a Metis package in `catalog/domains/<name>/`, as the home's are. Marked (new): what
is still to be built:

```text
catalog/domains/<name>/
├── domain.yaml        the manifest: display, line, voice, program, host, clock, shared (the composition's), status
├── <name>.llp         the rules (each with its %% line)
├── <name>_*.llp       generated parts (from the map: which, fan)
├── intent.yaml        the card: verbs, slots, the words for each role
├── goldens/           the package's day and intents, as metispy writes them
├── steering.yaml      (new) which conclusions go where (below)
└── SKILL.md           (new) for pi: how the domain's open-ended work is done
```

The host, the Go code that reaches the outside world, lives with its tool: acasa's `hass` for the
home, nina-live's `tools/` for the agent's own, pi's for pi's.

`steering.yaml` would replace today's one `levels` table in `nina.yaml` with a contract per domain:

```yaml
domain: media
steering:
  playback_changed:   {to: voice, as: context, key: media.playback}     # silent context, keyed
  playback_failed:    {to: task, as: steer}                             # into the running task that used media
  favourite_found:    {to: memory, as: fact}                            # a belief, no words
  requested_ready:    {to: voice, as: note, level: important}           # said when the floor is free
```

The routes map onto what exists: the voice's `Context` and `Note`, a task's `Steer` and `FollowUp`,
and the world's `Report` and `Conclude`.

## Home Assistant is the integration layer

The user's plan (2026-11-24): Nina does not write connectors for outside services that Home
Assistant already integrates. Each layer has one job:
- **Home Assistant** connects to the world: the integrations, the entities, the event bus and the
  services.
- **Metis** gives the domains their meaning: the state, the constraints and the relations between
  domains, and it steers the LLMs.
- **pi and Flash** interpret, do the open-ended work and answer.

One adapter per standard entity class, never per service (acasa `hass/classes.go`): the `todo`
class covers the shopping list, Local To-do, Google Tasks and Todoist; the `calendar` class covers
Google Calendar, Local Calendar and CalDAV. The adapter reads the feed (the websocket's
`state_changed`, which acasa already followed) and the services that answer (`weather.get_forecasts`,
`todo.get_items`, `calendar.get_events`, with `return_response`). Each entity becomes a line for
the voice's context and the tasks' world. The voice and the tasks also get the read tools `lists`,
`calendar` and `weather`.

A change made through Home Assistant (adding an item, creating an event) goes through a Metis
domain, as the home's commands do: the domain's rules decide, and its host calls the service.

| Nina domain | Home Assistant class / integration | On Pascal now | Metis side |
|---|---|---|---|
| home, comfort | `light`, `fan`, `switch`, `climate`, `sensor` (Prana, later Zigbee, Matter) | the two Prana units | running (the five packages) |
| weather | `weather` (Met.no now; Open-Meteo) | `weather.home` (Met.no): now and three days, in the context | the lines; rules to come (weather + calendar) |
| tasks | `todo` (Shopping List, Local To-do, Google Tasks) | Google Tasks `todo.my_tasks`, `todo.shopping_list`, the local test `todo.nina_local` | running: the lines and `lists`; the `agenda` domain adds Nina's to-dos to your tasks |
| calendar, reminders | `calendar` (Google Calendar, Local Calendar) | Google: your calendar, Birthdays, Holidays, and Nina's own `calendar.nina`; the local test `calendar.nina_local` | running: the lines and `calendar`; the `agenda` domain writes Nina's events on her own calendar |
| time | `timer`, `schedule` | none in use | Nina's own `timers` stay (her scheduler must not depend on Home Assistant) |
| media | `media_player` (MPD, Radio Browser; Music Assistant later) | MPD (`media_player.music_player_daemon`, a user service), the Echo Dot, Radio Browser | running: the `music` domain (taste per time of day, ducking, scenes) |
| people | `person`, `device_tracker` | `person.nina` | the lines |
| news | Feedreader | none | later |
| cooking, inventory | Mealie, Grocy (servers of their own) | none | later |
| diagnostics | System Monitor, Repairs, `update` | none | later |
| memory, interests, projects, goals, commitments, learning, refinement, notification policy | none suitable | | Nina's own |

### Google is the exchange point with the phone

The user's requirement (2026-11-24): what Nina derives locally shows up on the Android phone, and
what is entered on the phone reaches Nina, both ways, through Google's services.
- **Google Calendar** (Home Assistant's `google` integration): its calendars become `calendar.*`
  entities. Events entered on the phone reach Nina through `calendar.get_events`, and Home
  Assistant polls Google every few minutes. Nina's own items (a reminder at a time, a commitment
  with a date) go out with `calendar.create_event` and appear on the phone.
- **Google Tasks** (`google_tasks`): each task list becomes a `todo.*` entity, read and written
  through the same todo adapter. A to-do or a reminder without a time goes there.
- **Who owns what.** An item Nina created carries a mark of her own, so she never imports it back
  as the person's. An item the person changes on the phone is a person's report: it updates Nina's
  belief, the way a sensor contradicts a command.
- **What it needs from the person.** The Google sign-in is the person's: a Google Cloud OAuth
  client added under Home Assistant's application credentials, then the two integrations added in
  Home Assistant's settings. Nina never sees the password.

**As built (2026-11-24).** Home Assistant's Google Calendar and Google Tasks integrations run
under the person's Google Cloud project `home-assistant-nina`; Home Assistant's name is
`homeassistant.local` (the Raspberry Pi's hostname to come). `catalog/domains/agenda` decides where
Nina may write: her calendar (`calendar.nina`, Google), the person's tasks (`todo.my_tasks`), the
local test pair; the person's own calendar is read and never written. Its host (`agenda/`) calls
`calendar.create_event` and `todo.add_item` with the payload (summary, start, minutes, due), marks
the item "(set by Nina)" and records it in the log (stream `agenda`); a write Home Assistant refuses
is said. The voice's card is `home_agenda`.

### Music and habits: domains that adapt to the person (2026-11-24)

The user: "think out of the box: rules that capture the person's intent and preferences; fuzzy
matching on the last use is just another domain, a cross-domain skill".

- **`music`** (radio on MPD through Home Assistant; host `music/`). Taste is a multiset of linear
  tokens: a station kept on for 10 minutes leaves `liked(G, P)` for the genre and the time of day; a
  station changed within 90 s takes one back. The host reads the multiplicity and asserts the hour's
  favourite and runner-up, or a genre never kept at this hour to explore: "play something" is the
  favourite, "something else" the runner-up or something new, "again" the last genre of this hour.
  What the card cannot name is the person's own words: a fuzzy search, and once kept, those words
  find that station again. The floor machine ducks the music while anyone talks; a scene's wish
  fades it at night and stops it when everyone is out. The taste is the log's: it outlives restarts.
- **`habits`** ("again", "the usual", in any domain; host `habits/`). Every card call the rules
  answer done is kept (stream `call`). "Again" is the most recent, "the usual" the most frequent at
  this time of day, the person's words narrowing either (a fuzzy match over the calls' words). The
  rules decide what may be repeated: home commands, scenes, music, timers; never a calendar entry.

### Nina explains herself (2026-11-24, the user's priority)

When high-level domains and tool domains work together there will be inconsistencies; they are
used, not hidden, by narrating the inference trace (`explain/`). The trace is the log's story:
the person's requests, each domain's lines (what a rule said when it fired), the director's routing,
the world's and the Metis runtime's surprises, the tasks. The scripts are the Metis sources of the
domains in the trace, each rule with its `%%` meaning and what it takes and leaves (the lifted bases
included). A model (the task runner's: DeepSeek) narrates the chain and names what did not fit.
Two ways in: the `explain` tool ("why did you do that?", "what does it mean?"), and a surprise,
narrated by itself with the trace that led to it, at most once every three minutes, as a note.
First live narration: "The music dropped to a whisper because you set the home to night mode, and
the music followed. The ventilation did not: the lab unit hasn't confirmed whether it's running, so
I left it as it was rather than guess."

### What stays native

Nina keeps her own scheduler and timers: a conditional reminder and an autonomous task must not
depend on Home Assistant being up. Home Assistant's timers are shown, not relied on. Also native:
memory, interests, research, learning, projects, goals, commitments, refinement and the
notification policy. These are where Nina is more than Home Assistant.

### Order

| Phase | In Home Assistant (the person installs) | In Nina |
|---|---|---|
| 1 | — | the class adapters (done: weather, todo, calendar, timer, person, media_player) |
| 2 | Google Calendar, Google Tasks (done 2026-11-24) | the `agenda` domain (done): events on Nina's calendar, to-dos on your tasks, your calendar refused by the rules; next: the timers' reminders mirrored, items from the phone as the person's reports |
| 3 | MCP Server | the tools of the rest of Home Assistant to pi, a mutating call checked by the rules |
| 4 | Music Assistant | media reasoning (sessions, preferences, volume lowered while the person talks) |
| 5 | Feedreader | news and interests |
| 6 | Mealie, Grocy, Shopping List | cooking, inventory, shopping |
| 7 | GitHub, System Monitor | projects, diagnostics |

## Releases

| Release | Domains | Nina becomes | State |
|---|---|---|---|
| 0.1 | home, time, reminders, media | an Alexa replacement | home running; time and media next |
| 0.2 | calendar, tasks (to-dos), weather | an everyday personal assistant | |
| 0.3 | memory (semantic), interests, news, briefing | a companion with continuity | |
| 0.4 | projects, research, books, learning | an intellectual and engineering partner | |
| 0.5 | cooking, shopping, inventory, energy | the household managed | |
| 0.6 | monitoring, commitments, goals | proactive | |
| 0.7 | refinement from surprises | adaptive | |

After 0.1 and 0.2, the intellectual world comes before more household tools. Persistent interests,
continuity, news, memory and projects do more for the sense of intelligence than another hundred
conventional tools.

## References

[OpenClaw's integrations](https://openclaw.ai/integrations), [ClawHub's skills](https://clawhub.ai/),
[Alexa+](https://www.aboutamazon.com/news/devices/new-alexa-top-features),
[Gemini for Home](https://home.google.com/gemini-for-home-voice-assistant/),
[ChatGPT's use cases](https://learn.chatgpt.com/use-cases).
