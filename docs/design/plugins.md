# Plugins: Nina's competences, and the knowledge that transfers (2026-11-28)

The user's architecture, agreed on 2026-11-28. The runtime is nina-lead's; the user gives the
architecture.

## Three layers

**1. The persona: who she is, the same everywhere.** The generalist Nina has:

- her voice and character;
- her memory (the log) and a world rebuilt from it;
- her own clock (timers, routines, waking herself);
- her cognition (attention, what she keeps, escalation).

She talks and reasons about anything, and acts in no particular field. Her semantic world is the
generic one: people, time, her schedule, her own things (metis-worlds `semworld`). She alone is a
deployment, tested on the tablet.

**2. The plugin: a competence, and the knowledge that transfers.** A plugin is a field of work,
packaged:

| Part | What it is | Transfers |
|---|---|---|
| **Domains** | certified, precompiled Metis packages: what the actions mean, what they need and use up (linear logic: resources consumed and produced), what must stay true | yes: the knowledge |
| **Interface** | what the domains ask of the world: the ports, devices and lists they read and command (an interface package) | yes: the contract |
| **World extension** | what it adds to her semantic world: Home Assistant's is the flat's topology and its set of sensors and devices; a kitchen's, its inventory and tools | yes: its schema; each place supplies its own instance (the map) |
| **Skill** | for the LLM: when and how to use it (pi's skill format) | yes |
| **Certificate** | the composition the plugin's owner certified: the image hashes and the scenarios passed (its book, the SITL) at this version | yes, as evidence |
| **Bindings** | connectors that meet the interface: MCP to Home Assistant, Google's APIs, WhatsApp, a recipe library | no: one per backend, replaceable |
| **Setup and cards** | how a person connects it (the guided steps, the keys) and its cards on her screens | with the binding |

The domains, their interface and the world's schema are the knowledge. A binding is plumbing:
the Home Assistant plugin's knowledge (devices, sensors, rooms, scenes, what is safe) works against
another home hub with another binding. Some plugins have no binding at all: Office Worker is
knowledge of how to work with people.

**3. The deployment: where she works, and for whom.** The persona, the chosen plugins, their
bindings' credentials, and the place (a home map, an office's calendars, a kitchen's inventory).

## The plugins

| Plugin | Domains | World extension | Bindings | Status |
|---|---|---|---|---|
| **Google Services** | her agenda's and photos' use (the agenda domain) | calendars, lists, albums | Google's APIs (Workspace MCP before 2027-02-01), YouTube, Gemini; OAuth ours, the user's own project | first, on the tablet (claudia: `creds`, the guided setup) |
| **Home Assistant** | the home's domains (nina-narratives `devices/`: lights, air, comfort, scenes, media, …) | the flat's topology and its sensors (`home.map.json`, the `home` adapter) | MCP to Home Assistant (ha-mcp) | runs today: Pascal, the Pi |
| **Communicator** | social communication: who to answer, when to stay silent, what she keeps | chats, groups, people | WhatsApp (linked device) | the channel runs; its domain to write |
| **Office Worker** | communication skills: working with the people who come and go (book 2) | people, their requests | none (it uses Google Services and Communicator) | from the books |
| **Chef** (experimental) | cooking as resource handling (ingredients, tools, heat, time) in linear logic | inventory, utensils, recipes in progress | recipe libraries; no manipulator: she plans and guides | experimental |

## Deployments

| Deployment | Plugins | Where |
|---|---|---|
| Nina alone | none (the persona), Google Services when given | the tablet |
| **Nina with Home Assistant** | Home Assistant, Google Services, Communicator | Pascal, the Pi |
| Office | Office Worker, Google Services, Communicator | the secretary |
| Kitchen | Chef | experimental |

**Nina with Home Assistant is a deployment variant, not a change to Home Assistant.** Home
Assistant runs as it is. acasa becomes that variant's pages, which compose Nina (the presenter: her
face, voice, cards) with Home Assistant's dashboards, plus its install scripts. What acasa holds of
the Home Assistant plugin today (the MCP client, the device, the home map's reading) moves into the
plugin.

## Certification

The plugin's owner certifies its composition: its domains together with the persona, and with the
plugins it builds on, by its scenarios (its book, the SITL), at a version. The certificate is the
images' hashes (the goldens) and the scenario results. A deployment pins plugin versions with their
certificates, and an upgrade is a gate. A plugin without that evidence stays experimental.

## How knowledge moves

- **Down, by install:** a deployment installs a certified version, and its precompiled domains join
  her composition under the director; its world extension joins her world.
- **Up, by learning:** what surprises her in one deployment becomes a candidate rule for that
  plugin's domain (metis-loop). Re-certified against the plugin's scenarios, it reaches every
  deployment with the plugin at its next version. Knowledge is never copied between deployments by
  hand.

## What exists already

| Concept | Today |
|---|---|
| Domains | nina-narratives `devices/` (the home's), `cognition/` (the persona's) |
| Interface | nina-narratives `interfaces/` (`iface/home.llp`) |
| World: core and extension | metis-worlds `semworld` (generic) and `home` (the adapter) |
| Certificate | the image goldens, the SITL's scenarios and gates |
| Deployments | nina-narratives `workspaces/` (`home`, `office`, `live`), acasa's roles |
| Bindings | acasa's Home Assistant over MCP, nina-live `services/google`, `whatsapp/` |
| Setup and cards | nina-live `setup/`, acasa's `nina-cards.js` |
| Credentials | nina-live `creds` (claudia, 2026-11-28) |

Missing: the plugin's manifest that names these parts, and the runtime that installs one.

## The runtime's questions (nina-lead)

1. **Composing precompiled domains.** Each plugin's image joins one composition under the director
   without the catalog compiled again per deployment (metis-lang's specs 15 and 17, its link of
   roles).
2. **The world extended.** A plugin's world extension joins the persona's world: its schema at the
   build, its instance (the map) from the deployment. The members' manifests already join the board
   (`HomeSem.Join`); the adapter becomes one extension among others.
3. **Two plugins over one resource** (her calendar for Office Worker and Home): the composition's
   rules arbitrate, not the bindings.
4. **A binding meets its interface** at install: its ports and commands checked, not found missing at
   the first call.
5. **Versions:** a deployment pins each plugin and its certificate.
6. **Loading:** for now, bindings and world extensions are Go packages compiled in, their parts
   behind one registry (each brings its setup steps, cards and tools). The agent's side is open in
   pi's standard formats: skills, and MCP servers through a newer Go port of pi.

## The way there

1. **The persona alone on the tablet** (claudia: `app.NoDevice`, an empty map), and **Google
   Services** on it (the guided setup, `creds`).
2. The plugin manifest, and the registry in nina-live: Google Services, then Communicator, behind it.
3. Home Assistant as a plugin: acasa's binding into it, the `home` adapter as its world extension,
   its domains certified as the home's goldens are today. acasa then holds the deployment's pages.
4. Office Worker from book 2; Chef as an experiment.
