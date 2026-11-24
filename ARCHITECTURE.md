# Nina: architecture (refactoring step, 2026-11-24)

Nina is a personal agent. Speech is one of the ways to reach her; it is not her core. Her core
is three things:

- **a memory that is a log:** every event, in SQLite;
- **a world rebuilt from that log:** the semantic world;
- **an agent loop:** pi, with Metis as its symbolic reasoning.

Every tool she has is a member of one Metis composition, and the composition's director routes
between them.

## 1. Roles

| Project | Role | Owns | Gives up |
|---|---|---|---|
| **nina-live** (new) | the agent runtime | the event log, its projections (world, episodes, tasks, preferences), pi's agent, the Metis composition, the tool registry, the channels (voice, panel, buttons), the scheduler, `cmd/nina` | — |
| **fsttm-live** | the dialogue library: how Nina talks | the floor machine, attention, audio, endpointing, the local wake word, Gemini Live (the ears, and the voice session as System 1), the local voices; `pkg/dialogue` (the facade); `cmd/fsttm-live` (a test harness with a demo tool) | the plugin host, the web panel, the buttons, the config and preferences file, the SOURCES (Metis) adapter: all to nina-live |
| **sky-valley/pi** | the agent loop (System 2) | `agent` + `ai` (+ `ai/providers`); pinned, unmodified | — |
| **metis-lang** | the language | `metisc`, `libmetis_rt`, the specs (15: compositions and interface packages; 16: beliefs and levels; 17: images) | — |
| **metis-agent** | the embedding | a **public** Go package over `internal/rt` (Load, BindWeight, Do, Tick, Host, Counts), and the **Go director tick**: specialists first, then the director, as a Moore latch (`py/metis_agent/service.py` today) | the `metis-home` binary: its parts go to acasa and nina-live |
| **semantic-world** (metis-worlds `libs/semantic-world/go`) | the world model | `semworld` (generic) + `home` (adapter); **new:** `Apply(Event)`, deterministic replay, a checkpoint hash | its JSON-file `Store` (the log replaces it) |
| **metis-worlds / metis-catalog** | the domains | the home packages; the director base (catalog); **new:** nina's composition, i.e. the director instance and one specialist per tool family | — |
| **acasa** | the home | the **Home Assistant tool**: the websocket feed, the REST commands (the Prana's one-fan-per-call), the home map, the home host (metis-home's families and views); the home's composition config; the deployment (systemd, HA's compose, `nina.yaml`) | `live/run.sh`'s two services become one |

Dependencies point one way:

```
                         nina-live (cmd/nina)
     ┌───────────┬───────────┼─────────────┬──────────────┐
     ▼           ▼           ▼             ▼              ▼
 fsttm-live   sky-valley/pi  metis-agent  semantic-world  acasa/hass (the HA tool)
 (pkg/dialogue)              (public pkg)                   │
                                 │                          ▼
                             libmetis_rt              Home Assistant (container)
```

There is one process, `nina` (cgo: libmetis_rt, sherpa-onnx, the WebRTC VAD), beside Home
Assistant. `metis-home` and its two TCP ports (tools 8768, the door 8766) go away. Their protocol
was a process boundary that had no other user; MCP comes back later, for outside clients only.

## 2. The event log

### 2.1 The table

One SQLite file (WAL), append only:

```sql
CREATE TABLE events (
  seq    INTEGER PRIMARY KEY,   -- the order; the only clock replay trusts
  at     REAL    NOT NULL,      -- world time (s): the `now` every world call takes
  stream TEXT    NOT NULL,      -- world | dialog | agent | task | prefs | sys
  kind   TEXT    NOT NULL,      -- see 2.2
  key    TEXT,                  -- the entity/var, the task id, the utterance id
  cause  TEXT,                  -- the seq or id that caused it (a turn, a tool call, a rule)
  data   TEXT    NOT NULL,      -- JSON
  v      INTEGER NOT NULL       -- the kind's schema version
);
CREATE TABLE snapshots (seq INTEGER PRIMARY KEY, code TEXT, hash TEXT, blob BLOB);  -- a cache, never truth
CREATE VIRTUAL TABLE episodes USING fts5(text, kind, at UNINDEXED, seq UNINDEXED);  -- a projection
```

### 2.2 The world stream: the semantic world's input alphabet

The world is a function of these and nothing else:

| kind | data | applied as |
|---|---|---|
| `genesis` | the map (home.map.json, hashed), `Params`, the manifests joined, the code version | `home.New(map, params)`, `Board.Join` |
| `ha_state` | entity_id, state, attributes (mapped entities only) | `Home.Ingest` |
| `feed_down` / `feed_up` | — | `Home.FeedDown` |
| `command` | id, var, value, by (tool call or rule) | `Home.Command` (written **before** the REST call: the command is the world's prediction) |
| `report` | person, id, var, value, utterance id | `World.Report` |
| `conclude` | member, fact, on, rule | `World.Conclude` |
| `settle` | — | `Home.Settle` (once a beat) |
| `checkpoint` | hash of the world | not applied: replay **verifies** it |

Surprises and projected lines are *outputs*. They are logged with `cause` for the record, and
replay recomputes them instead of reading them.

### 2.3 The determinism contract

This is what makes "reconstructable" true, and tests hold it:

1. **Time is an argument.** The world never reads the clock; every call takes the event's
   `at`. This already holds (`semworld` has no `time.Now`). `Store.Journal`'s wall-clock
   stamp goes with the store.
2. **No map order.** `home.go:345` (FeedDown), `:419` (the pending past grace) and `:458`
   iterate Go maps. They are changed to sorted keys, so surprises and their order replay the same.
3. **Listeners are pure.** `World.OnChange` subscribers may only read; any write is an event.
4. **One writer.** Only the log's apply loop changes the world. The HA feed, the tools and
   Metis append events, and the apply loop folds them. This is also the world's mutex
   discipline.
5. **The tests:**
   - `replay(log)` equals the live world at every `checkpoint` (a hash over sorted entries).
     A mismatch is a bug report with its seq.
   - Golden logs (the semworld-replay days) replay to golden lines.
   - A snapshot plus its tail equals a replay from genesis.

When the world's code changes (a new code version), snapshots are dropped and the world replays
from genesis. The log is the only thing kept.

### 2.4 The other streams: episodic memory

- **dialog:**
  - `heard` (final text, voiced/pitched, the wake word's hit)
  - `turn`, `said`, `cut`, `note`, `attention`
- **agent:** pi's `message_end` messages, with their role and tool calls/results.
  - This replaces pi's JSONL session (its storage is a file, not an interface).
  - A run resumes with `Agent.SetMessages(projection)`.
- **task:** `created`, `progress`, `done`, `failed`.
- **prefs:** `set`. The preferences become a projection, and the YAML keeps only the
  machine's settings.

Projections are rebuilt on start:

- the world (in memory);
- `episodes` (FTS5 over what was said and done);
- the open tasks;
- the preferences;
- each agent's transcript.

The Live session's context on resume is rebuilt from them (the world's lines, the last
episodes), not trusted to the session handle.

## 3. Metis in pi: every tool a member of one composition

### 3.1 The composition

The user's design: not each tool backed by its own domain, but each tool *represented* in one
hierarchical Metis interpreter. It is metis-catalog's director, lifted:

```
nina (director: the catalog's base + nina's instance)
├── home (director over the home's packages; nested: a director is a specialist of another)
│   ├── lights   air   media   comfort
│   └── scenes   (its fan-out: the director's rules, not home.go's homeSweeps table)
├── web      (search, fetch: read-only)
├── memory   (recall: the episodes projection)
├── tasks    (delegate, status, cancel)
├── calendar, mail, …  (later)
└── iface/   nina.llp, home_world.llp, protocol_domain.llp  (interface packages: the crossing vocabulary, with natures)
```

- **A tool family is a specialist.**
  - Its verbs are its `world.in`; its results are `out`.
  - Its crossing vocabulary is an interface package with natures (`signal | resource | report`).
  - A read-only family gets a thin specialist, *generated* from the tool's schema (as
    `intent.yaml` generates the cards).
  - Its Go function is the host's effector for `call(...)`, and its result comes back as an
    `in` line.
- **The director routes:**
  - `handles(D, kind)` (generated from the manifests), attention, `detour`, `escalate`,
    `ask` ("the lab or the bedroom?"), `refused` with its `%%` line.
  - It never acts in the world; its effects are the agent's own verbs.
- **`metisc build` / `--link` certifies the whole as one image** (I1–I13, L13–L25). A tool
  that does not fit the interface fails the build, not the conversation.

### 3.2 Metis is a second reasoning loop, not a tool

A tool runs when the LLM decides to call it. Metis does not wait to be called: it beats on its
own clock and on every change of the ports it reads, and its conclusions change what both LLMs
know next. Nina's cognitive core is the pair of loops, coupled through the log:

- **Metis's loop:** events → world → beat → conclusions and surprises → events.
- **The LLMs' loops:**
  - Gemini Live: heard → turn → tool calls → said.
  - pi: context → model → tool calls → results → context.

**How the LLMs see Metis.** Both see the same registry, through two adapters:

- **System 1, Gemini Live (fsttm-live):**
  - Its tools are **two cards: the director's and the attended specialist's** (DIRECTOR.md's
    point 4). It no longer gets one tool per package.
  - It also gets `delegate(goal)`, the tasks specialist's verb: an open-ended goal becomes a task.
- **System 2, pi:**
  - Each card's verbs are `agent.AgentTool`s.
  - `Execute` submits the intent frame and returns the outcome. The outcome is `accepted`,
    then a result on a later beat, as metis-loop's `intent_<verb>` does today.
  - Acting cards run `ExecutionMode: ToolSequential`: a batch of mutating calls goes in order,
    the way the tick would take them anyway.

**What each LLM receives from Metis**, by the kind of output:

| Metis output | Gemini Live (fsttm-live) | pi (a running task) |
|---|---|---|
| the current beliefs (projected lines) | silent context, keyed (`ContextKey`: a newer line replaces the older) | `TransformContext`: the projection replaces the previous one before each request, filtered to the task's specialists; nothing piles up in the transcript |
| a new conclusion | silent context, or a NOTE by its level | `Steer`: delivered after the current tool batch, before the next request (loop.go:247) |
| a contradiction or surprise | a NOTE if its level passes the knob | `Steer`; a surprise with no task may *start* one (below) |
| a refusal, with its reason | the tool's result | the tool's result |
| an outcome on a later beat | a NOTE | `FollowUp` if the run is ending, else `Steer` |

- **Autonomy:** Metis runs when neither LLM does. A rule whose conclusion needs deliberation
  (for example a director `escalate`, or an `advise_*` with a goal) emits `task(goal, level)`
  on its output port. That is a `task created` event, and Nina starts a pi run for it. If a
  task on the same specialists is already running, the conclusion is steered into it instead.
- **What is relevant** is decided by the composition, not by guesswork: the task records the
  specialists it attended, and only their lines and conclusions reach it.

### 3.3 Which pi mechanisms, and why

pi offers hooks; a few are used, and none of them for policy:

| pi mechanism | Used? | Why |
|---|---|---|
| `Steer`, `FollowUp` | **yes** | messages from outside the run: Metis's new conclusions, outcomes, notes |
| `TransformContext` | **yes, read-only** | it puts the current world projection in place before each request. It is a projection; it decides nothing |
| `BeforeToolCall` | **no** | every acting tool *is* a Metis verb: submitting the intent is the check, and a refusal is the result. A separate `Validate` before `Execute` would evaluate the same rules twice, and the world can change between the two (check, then act) |
| `AfterToolCall` | **no** | a tool's result already enters Metis as an `in` line, and so the log, by the effector that produced it |
| `FinishTurn` | later | it could keep a task running while its goal atom is not yet concluded |

So the gating, the refusals and the approval of a mutating call are **rules in the
composition**. What the rules don't know, the agent can't do. That is one place to read, one to
change and one to certify.

### 3.4 What is missing

The big items:

- **A Go director tick in metis-agent.** Today the hierarchical tick runs only in the Python
  Live (`service.py`, `director.py`). `dagent.Domains` steps flat agents.
- **The public embedding package in metis-agent.** `internal/rt` cannot be imported from
  another module. DESIGN.md §2 of semantic-world lists the six names to export.
- **Generating specialists from tool schemas,** and `handles/2` from the manifests (DIRECTOR.md's
  open point 1).

## 4. The dataflows

Every arrow goes through the log. Something that is not an event did not happen, as far as the
world, the replay and the mining are concerned.

| # | Flow | Path |
|---|---|---|
| F1 | **A spoken command** (fast) | the ReSpeaker → fsttm-live (VAD, the wake word, the ears) → `heard` → Gemini Live → a card's verb → `intent` → the tick → a rule fires → `command` (the world's prediction) → acasa's effector (HA REST) → `ha_state` comes back → the world → the tool's result → `said` |
| F2 | **An observation** | HA's websocket → `ha_state` → the apply loop → the world → `settle` → the members whose ports changed wake → `conclude` / surprises → the world → the projections: Live's silent context, a NOTE by its level, `Steer` into a running task |
| F3 | **A delegated goal** | Gemini Live → `delegate(goal)` → `task created` → a pi run (the world projection by `TransformContext`, episodes recalled by the memory specialist) → card verbs (as F1 from `intent` on) → `task done` → a NOTE → the floor machine says it when the floor is free |
| F4 | **Autonomy** | a member's conclusion → its output `task(goal, level)` → `task created` → as F3, with no one having asked; or `Steer` into the running task on the same specialists |
| F5 | **A person's word** | "I switched the lamp off" → the director routes it as a report → `report` (weak evidence, the utterance id as its ref) → the world; against a sure, fresh sensor it is an evidential surprise instead |
| F6 | **Rebuild** | at start: the last snapshot of this code version + the tail, or genesis → the world; the episodes, the tasks, the prefs and the transcripts are rebuilt as projections; the Live session is resumed with its context rebuilt from them |
| F7 | **Mining** | the conversations are kept, all of them. A scheduled pi task reads the episodes, the surprises and the director's `nohandler` / `refused(route)` → **proposals**: a new tool where people asked for what no specialist handles, a new rule where the world surprised → metis-loop's gate cascade (`pack_propose`) → the person approves → a catalog commit → a new image (a new code version: the world replays) |

What changed from the first draft, after checking these flows:

- **Metis TCP first?** No. A world that lives in metis-home is written by metis-home, not by
  the log, so the log stops being the source. A one-day spike over the existing ports
  (8768/8766) is fine to try `Steer` and `TransformContext` against a real pi run, but milestone 1
  is in-process.
- **A `ReasoningSystem` interface** (Observe / Infer / Snapshot / Validate / Events) maps onto
  what exists, with one call fewer:
  - `Observe` is an append to the log.
  - `Infer` is the beat; no LLM calls it.
  - `Snapshot` is `Project(world)` filtered to a scope.
  - `Events` is the log's subscription to `conclude` and `task created`.
  - `Validate` is dropped: the verb is the check.
- **Listeners block the loop:** pi's `Subscribe` listeners run on the loop's goroutine
  (agent.go:701). The listener that logs `message_end` only appends to the log's in-memory
  queue; the SQLite write happens on the log's own goroutine.

## 5. fsttm-live's facade

```go
package dialogue // github.com/ibnHatab/fsttm-live/pkg/dialogue

type Config struct{ Audio, Models, Dialog, Wake …; Charter string }   // the dialogue's own settings
type Tool   struct{ Decl *genai.FunctionDeclaration; Run func(ctx, args) map[string]any }

func Start(ctx, Config, Tools func() []Tool) (*Dialogue, error)   // Tools: asked again when attention moves (the two cards)
func (d *Dialogue) Events() <-chan Event   // heard, turn, said, cut, attention, note_spoken, status: nina logs them
func (d *Dialogue) Note(level, text string)
func (d *Dialogue) Context(key, line string)   // the world's projected lines (silent context)
func (d *Dialogue) Do(action, arg string) error // talk, wake, sleep, stop, mic, voice, type, say
func (d *Dialogue) SetPrefs(Prefs) error        // voice name, local voice, narration knob, mic, sound
```

What moves out of fsttm-live:

| from | to |
|---|---|
| `internal/nina` (host, prefs saving, Do/Choices) | nina-live `channels/voice` (wraps `pkg/dialogue`) + `prefs` projection |
| `internal/sources/metis.go` | gone (the composition is in-process) |
| `internal/plugins/web` | nina-live `channels/web` (the panel: the dialogue's status from `Events`, the agent's tasks, the world) |
| `internal/plugins/gpio` | nina-live `channels/gpio` |
| `internal/config` prefs/plugins/sources | nina-live config; fsttm-live keeps the dialogue's part |
| `testdata/charter-v2.txt` | split: the voice's floor and role rules stay in fsttm-live; Nina's identity, home and tasks go to nina-live |

## 6. nina-live's layout

```
nina-live/
├── cmd/nina/                 the process: config, log, projections, composition, channels
├── log/                      the SQLite event log: Append, Read(from), Subscribe; snapshots
├── world/                    the apply loop: events → semantic world; checkpoints; replay tool
├── memory/                   episodes (FTS5), recall; prefs; transcripts
├── metis/                    the composition: load the image, the director tick, cards → registry
├── tools/                    the registry (one neutral Tool) + adapters: genai (Live), pi (AgentTool)
│   ├── web/  memory/  tasks/ the Go effectors of the specialists
├── agent/                    pi: one Agent per task; delegate; results → task events → notes
├── tasks/  sched/            tasks; the scheduler (time events: a stream, so replay sees them)
├── channels/voice web gpio/  the ways to reach her
└── domains/nina/             the director instance + the generated specialists + iface/nina.llp
```

acasa supplies `hass/` (the HA tool: the feed becomes `ha_state` events, the REST calls become
the home specialists' effectors), `home/` (metis-home's host: families, views) and the
deployment.

## 7. Milestones

1. **The same Nina, in the new layout, without pi** (done on Pascal 2026-11-24, except the Pi 4 build):
   - `cmd/nina`: fsttm-live through `pkg/dialogue`, the home packages flat (as today) on the
     embedded runtime, and the semantic world from the event log.
   - acasa's HA tool, the panel.
   - Passes: the dialog tests, the Prana tests, replay-equals-live, and **a build on the Pi 4**
     (aarch64: libmetis_rt, sherpa-onnx's aarch64 libs, the VAD).
   - One systemd service.
2. **The director:**
   - over the home's packages (scenes' fan-out as rules);
   - Live sees two cards;
   - `metisc --link` certifies.
3. **System 2** (the user: when testing, experiment with `Steer`, `FollowUp` and
   `TransformContext` to make the system flexible and natural; embedding pi is meant to reuse
   its tools and some public plugins):
   - pi for `delegate`, with tasks, results as notes;
   - web and memory as specialists;
   - transcripts in the log.
4. **The rest:** the scheduler, skills (pi's SKILL.md, given programmatically:
   `BuildSystemPromptOptions.Skills`), calendar and mail as specialists, MCP for outside clients.

## 8. Facts this rests on (read 2026-11-24)

- **pi (sky-valley/pi, MIT, go 1.26, deps: x/image, x/text; 50 commits 11-14…11-23):**
  - `agent.NewAgent` needs only `ai` (+ the providers' blank import).
  - `AgentTool.Execute(ctx, id, params, onUpdate)`.
  - `Steer` / `FollowUp` are safe from any goroutine; there is one run per Agent; several
    Agents can run in one process.
  - It has no extension registry: hooks, tools and providers only.
  - Sessions are JSONL files, not an interface.
  - Its Google provider is `streamGenerateContent` v1beta with function calls and thinking,
    **and no Live**: the voice stays fsttm-live's.
- **Metis:**
  - The director base: `metis-catalog/domains/director`.
  - Compositions with interface packages: `metis-worlds/alfworld/domains/hier`, kitchen_pot,
    road.
  - Spec 15 §10 (interface packages, natures, I-checks), spec 17 (images, L-checks).
  - The home world is flat by design today (its README: "a director is the next step").
- **semantic-world:**
  - Its writers are `Ingest`, `FeedDown`, `Command`, `Report`, `Conclude` and `Settle`, each
    taking `now`.
  - Three map-order loops in `home.go` are to fix.

## 9. Decided

Decided (2026-11-24):

- **nina-live** is a public repository under ibnHatab; fsttm-live is public too.
- **Go 1.26** for nina-live and fsttm-live (pi needs it). The toolchain comes through the
  module's `go` line.
- **The conversations are kept, all of them,** for mining (F7): what people ask that nothing
  handles, and where the world surprised, are the input for new tools and new rules.
- **The license** is the Anti-Capitalist Software License v1.4 (`LICENSE`).
- **metis-agent's changes,** the public embedding package and the Go director tick, go on its
  `home-cgo` branch.
- **pi's mechanisms** are as in 3.3. `Steer`, `FollowUp` and a read-only `TransformContext`
  carry state and messages. No hook holds policy, so `BeforeToolCall` and `AfterToolCall` are
  not used; the rules decide.

Nothing is left to decide for milestone 1.
