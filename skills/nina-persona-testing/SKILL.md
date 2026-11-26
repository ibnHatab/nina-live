---
name: nina-persona-testing
description: Test Nina end to end without making a sound — wake-cycle scripts that speak into her remote microphone and print what she heard, which tools she called and what she said; screenshots of her screens (Chrome, or a Pi kiosk with grim); her event log in SQLite; and cleaning up the test data afterwards. Use when verifying a change to Nina's voice, tools, rules or screens on a desktop or a Pi, or when diagnosing what she misheard or why she stayed silent.
---

# Testing Nina silently

## Speak to her: wake-cycle

`fsttm-live/bin/wake-cycle -panel http://HOST:8770 -script FILE` synthesises each line (a local TTS
voice) and streams it into her **remote microphone** over the panel's websocket; her voice comes back
over the socket, so **the room hears nothing**. One phrase per line; `| asleep` at the end of a line
when she should end asleep. It prints one JSON line per phrase (`said`, `heard`, `reply`, `trace`)
then a readable trace. Example script:

```
Nina, add "renew the passport" to my tasks.
What's on my to-do list now?
Remove "renew the passport" from my tasks.
Go to sleep. | asleep
```

A small reader for the JSON lines: print `said`, `heard`, the trace items containing ` tool `, and
`reply`. An empty `reply` can mean she was silent or only that she took longer than its 14 s wait:
check `reply_done` / `no_reply` in the log.

## What she did: the event log

`~/.local/share/nina/nina.db` (SQLite, table `events`: seq, at, stream, kind, data JSON):

```sh
sqlite3 ~/.local/share/nina/nina.db "select kind, substr(data,1,300) from events
  where stream='dialog' and kind in ('heard','turn','tool','reply_done','no_reply') order by seq desc limit 12"
```

Useful kinds: `heard` (the transcript), `turn` (`text` = the person's words, `context` = her internal
note), `tool` (`args`, `result`), `reply_done` (`said`, `audio`), `no_reply` (the model never
answered), `silent_after_tool`, `ears_refusal`, `attention` (`awake`, `told`), `stage`. Host streams:
`agenda`, `task`. On a Pi with no user journal, the log is the place to look; the service's stderr:
`sudo journalctl _SYSTEMD_USER_UNIT=nina.service`.

## Her screens

- Desktop: Chrome at `http://localhost:8123/<dashboard>?kiosk` (Home Assistant) or
  `http://HOST:8770/nina/shell.html` (her own screen). Screenshot after each request. The stage
  (videos/photos) state is on `window.__ninaStage` (`what`, `opts`, `playing`); set
  `window.__ninaStage.vol = 0` before a video plays.
- A Pi kiosk (labwc, Wayland) over SSH:
  `XDG_RUNTIME_DIR=/run/user/1000 WAYLAND_DISPLAY=wayland-0 grim /tmp/s.png`, then `scp` it back.
  `wtype -k F5` reloads the kiosk's page (keyboard only: there is no pointer tool, so ask the person
  to tap when a test needs a touch). Chrome and the kiosk's Firefox render differently: check the
  device itself (Firefox painted blurred layers over an expanded card that Chrome drew correctly).

## Rules

- **No sound after 22:00** (neighbours): wake-cycle is silent, but radio and videos are not — on a
  Pi they play in the room. Timers and alarms speak when they ring: cancel any you set before you
  leave, especially if it would ring in quiet hours.
- **Clean up what the test created**: list items (`todo.get_items` / `todo.remove_item`), calendar
  events (websocket `calendar/event/delete` by uid, matching summary + start + Nina's mark
  "(set by Nina)"), timers ("cancel my timers" — this cancels *all*, the person's own reminders too:
  say so). Take a "before" listing so you remove only your own items.
- Never send messages (WhatsApp) to real people as a test.
- Report each test as said / heard / tools / reply / screen verdict, and what you fixed.

## Known transcriber behaviour (Gemini Live transcription)

- It begins each utterance's text anew; the ears take that whole (an old offset-cut had clipped
  first letters: "ext video.").
- It sometimes answers instead of transcribing ("I'm just a language model…"); the ears drop it and
  she asks the person to repeat. One synthetic recording triggered it every time: rephrase the test.
