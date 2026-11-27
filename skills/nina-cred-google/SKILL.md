---
name: nina-cred-google
description: Give Nina her Google Calendar and Google Tasks without Home Assistant (nina-live services/google) — create the person's own OAuth client of type Desktop app, enable the Calendar and Tasks APIs, run the one sign-in (cmd/nina-google-auth) that writes ~/.config/nina/google-token.json, and switch her agenda to it in the core config (services.agenda: google). Use when Nina runs where there is no Home Assistant (the tablet), when moving her agenda off Home Assistant's Google integrations, or when Google refuses her token (invalid_grant).
---

# Nina's Google agenda, direct

The person does every step that touches their account. Claude never types a password, never sees the
token, and never prints the token file.

## 1. The OAuth client (the person, in the Google Cloud console)

1. The project Nina's Home Assistant integrations use, or a new one. **APIs & Services → Library**: enable
   **Google Calendar API**, **Google Tasks API** and **Photos Library API**.
2. **OAuth consent screen**: the scopes `.../auth/calendar.events`, `.../auth/tasks`,
   `.../auth/photoslibrary.readonly.appcreateddata` and `.../auth/photoslibrary.appendonly`. Keep it **published**
   ("In production"). In *Testing*, Google expires the refresh token after 7 days (`invalid_grant`).
3. **The client: Home Assistant's own (recommended where it has one), or a new Desktop app.**
   - *Home Assistant's Web client* (the one its Google integrations use as application credentials): Nina
     is then the same app to Google, and sees the Photos albums Home Assistant made. On the client's page add
     the authorized redirect URI `http://localhost:8765/` and download its JSON. Home Assistant's own sign-in
     keeps working: each sign-in has its own refresh token.
   - *A new Desktop app* (where there is no Home Assistant: the tablet): no redirect URI. Nina sees only
     the albums she makes herself.

## 2. The sign-in (the person, on a desktop with a browser)

```
cd ~/repo/nina-live && go run ./cmd/nina-google-auth -client ~/Downloads/client_secret_XXXX.json
```

It prints a Google URL. Sign in with the account whose calendar and tasks Nina keeps, and allow both scopes
(an unverified app shows a warning: Advanced → continue). The sign-in asks for all four scopes and refuses a token without them. It writes `~/.config/nina/google-token.json`
(chmod 600). Delete the downloaded client JSON afterwards, or keep it private.

On another machine (the tablet, a Pi), copy the token file as the other keys are copied (scp; on Android, the
app's private files, `android-build.sh`). Never put it in a repo.

## 3. Nina's config (the core's nina.yaml)

```yaml
services:
  agenda: google                    # the agenda's writes, and the read tools' lists and calendars, on Google
  google:
    token: ~/.config/nina/google-token.json
    calendars: {"<her calendar id>@group.calendar.google.com": "my calendar", "primary": "your calendar"}
  photos: google                    # her albums on Google Photos directly (the albums she made)
  weather: {lat: 45.75, lon: 21.23} # Met.no directly (optional: the weather without Home Assistant)
agenda:                             # each target handle → Google's id: a calendar id, or a task list's title or id
  nina_cal: "<her calendar id>@group.calendar.google.com"
  person_cal: primary
  my_tasks: "My Tasks"
  shopping: "Shopping"
```

Without `services.agenda`, the agenda stays on the deployment's backend (Home Assistant's at home). The
switch is per machine. At home, Home Assistant's dashboards show Google's calendars and lists through its
own integrations, which poll: a direct write shows there after their next poll.

## Checks

- `go test ./services/...` (a fake Google, a fake Met.no).
- Nina's log after "add milk to the shopping list": `agenda todo` with no `err`, and the item in Google Tasks.
- `invalid_grant`: the token was revoked, or the consent screen is in Testing. Run the sign-in again.

## Photos: what the API shows

Since 31 March 2025, Google's Photos Library API shows an app only the albums and photos that app created.
Home Assistant's Google Photos integration is under the same rule: its photos card shows the albums the
integration made. Nina's direct access works the same way: `services/google` lists her albums, lists their
photos and uploads into an album (creating it when needed). Photos taken on the phone reach her only through
an upload. Google ties app-created content to the app (the OAuth client) that created it: signed in with Home
Assistant's Web client, Nina reads the albums Home Assistant made; with a Desktop client of her own, only
hers. Check it on the first sign-in.
