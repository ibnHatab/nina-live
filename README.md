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

Status: design. Read [ARCHITECTURE.md](ARCHITECTURE.md): the roles, the event log and its
determinism contract, Metis and pi, the dataflows, the milestones.
