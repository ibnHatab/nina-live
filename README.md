# nina-live

Nina as a personal agent: speech is one way to reach her, not her core.

- **The event log (SQLite)** holds everything that happened. The semantic world, the episodes,
  the tasks and the preferences are rebuilt from it.
- **pi** ([sky-valley/pi](https://github.com/sky-valley/pi), the Go port) runs her open-ended
  tasks.
- **Metis** reasons over one composition: a director over specialists, one specialist per tool
  family. It is her second reasoning loop, coupled to the LLMs through the log.
- **[fsttm-live](https://github.com/ibnHatab/fsttm-live)** is how she talks: the floor machine,
  attention, the local wake word, and Gemini Live.
- **[acasa](https://github.com/ibnHatab/acasa)** is the home: the Home Assistant tool and the
  deployment.

Status: milestone 1 runs on Pascal (2026-11-24): `cmd/nina` holds her voice, the home's rules,
the world rebuilt from the log and Home Assistant as her tool, in one service (acasa's
`acasa-nina`). The Pi 4 build is still to do. pi comes in milestone 3. Read
[docs/DOMAINS.md](docs/DOMAINS.md) for the domains (the catalog and its progress), and
[ARCHITECTURE.md](ARCHITECTURE.md) for the roles, the event log and its determinism contract,
Metis and pi, the dataflows and the milestones.

    ./build.sh                 # → bin/nina (cgo: metis-lang's libmetis_rt; METIS_LANG_DIR)
    ./build.sh test ./...
    scripts/persona-build.sh   # the persona whole (metisc, nina, the radar); a machine's update is acasa's install/update.sh

nina-live is the **persona**; [acasa](https://github.com/ibnHatab/acasa) adapts it to a home or a role
(Home Assistant, dashboards, kiosk, deployment). The experience of building and changing her is kept as
agent skills in [`skills/`](skills/) (`skills/link.sh` links them into `~/.claude/skills`):
`nina-persona-build`, `nina-persona-domains`, `nina-persona-testing`, `nina-persona-integration`.

License: [Anti-Capitalist Software License v1.4](LICENSE).
