package music

import "context"

// Backend plays the music: the far end of the package's host (the redesign's Google services and the
// tablet, s-b478fe59: the rules and the host are the same whichever player it is).
type Backend interface {
	Play(ctx context.Context, url string) error // a station's stream, now
	Stop(ctx context.Context) error
	Volume(ctx context.Context, v float64) error // the player's own volume, 0..1
}

// HA is a Home Assistant media player (media_player.play_media, media_stop, volume_set): Call is its
// client's service call (acasa's hass), Player the entity (default media_player.music_player_daemon).
type HA struct {
	Call   func(ctx context.Context, domain, service string, data map[string]any) error
	Player string
}

func (a HA) entity() string {
	if a.Player != "" {
		return a.Player
	}
	return "media_player.music_player_daemon"
}

func (a HA) Play(ctx context.Context, url string) error {
	return a.Call(ctx, "media_player", "play_media", map[string]any{"entity_id": a.entity(), "media_content_type": "music", "media_content_id": url})
}

func (a HA) Stop(ctx context.Context) error {
	return a.Call(ctx, "media_player", "media_stop", map[string]any{"entity_id": a.entity()})
}

func (a HA) Volume(ctx context.Context, v float64) error {
	return a.Call(ctx, "media_player", "volume_set", map[string]any{"entity_id": a.entity(), "volume_level": v})
}
