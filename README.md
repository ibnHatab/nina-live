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
[ARCHITECTURE.md](ARCHITECTURE.md) for the roles, the event log and its determinism contract,
Metis and pi, the dataflows and the milestones.

    ./build.sh                 # → bin/nina (cgo: metis-lang's libmetis_rt; METIS_LANG_DIR)
    ./build.sh test ./...

License: [Anti-Capitalist Software License v1.4](LICENSE).
