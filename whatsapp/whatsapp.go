// Package whatsapp is Nina's WhatsApp channel (nina-live, 2026-11-26; the user: WhatsApp in Nina, not
// in Home Assistant, on the Pi; a spare number of hers). whatsmeow (go.mau.fi/whatsmeow, the
// multi-device protocol, unofficial: a number can be banned, so never the person's own): Nina is a
// linked device of that number.
//
//   - Every message of the chats she is in is kept, apart from her log: its own SQLite file (with
//     whatsmeow's keys), table nina_messages.
//   - She answers (the user's choice) a direct message to her number, and in a group a message that
//     names her ("Nina", @-mention) or replies to one of hers; text answers, written by her task
//     runner with her tools (Answer).
//   - Throttled: one answer at a time per chat, at least MinGap apart; at most PerHour an hour in
//     all (past it, the request is kept and logged, not answered).
//   - The groups named in Show are her screen's (a "whatsapp" event with their recent messages, and
//     each new message as "wa_message").
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

// Config is the channel's (nina.yaml whatsapp:).
type Config struct {
	Store   string        // the SQLite file (whatsmeow's keys and her messages)
	Names   []string      // words that name her in a group ("nina")
	Show    []string      // groups on her screen, by name ("Office")
	MinGap  time.Duration // between two answers in one chat (0: 10 s)
	PerHour int           // answers an hour in all (0: 30)
	// Answer writes her reply to a request (her task runner); "" sends nothing.
	Answer func(ctx context.Context, r Request) (string, error)
	// Log is her screens' event stream (the card's "whatsapp" and "wa_message").
	Log func(kind string, data map[string]any)
	// Strain is told of a failure to answer (her task model busy…).
	Strain func(err error)
	// Debug: whatsmeow's own log on stdout (linking trouble).
	Debug bool
	// Mode: "named" (default: direct messages, and groups naming her), "self" (only the number's
	// own "Message yourself" chat: the person's primary number, 2026-11-26; she never writes to anyone
	// else) or "none" (read only).
	Mode string
	// Keep: the chats kept (stored, shown, readable): "self", chat names, or "all"; empty: all.
	Keep []string
}

// Busy is her word when no answer came in time (her models overloaded).
const Busy = "Sorry, I can't think straight right now (my models are overloaded). Please ask me again in a few minutes."

// Msg is one message as kept.
type Msg struct {
	ID         string    `json:"id"`
	Chat       string    `json:"chat"`
	ChatName   string    `json:"chat_name"`
	Sender     string    `json:"sender"`
	SenderName string    `json:"from"`
	At         time.Time `json:"at"`
	Text       string    `json:"text"`
	Kind       string    `json:"kind"` // text | image | voice | video | document | sticker | other
	FromMe     bool      `json:"me"`
	Addressed  bool      `json:"to_her"`
}

// Request is a message she was asked something in, with its chat's recent messages.
type Request struct {
	Msg
	Group  bool
	Self   bool  // the number's own chat: the person writing to her
	Recent []Msg // oldest first, the request last
}

// Client is the channel.
type Client struct {
	cfg  Config
	db   *sql.DB
	cli  *whatsmeow.Client
	name *regexp.Regexp

	mu     sync.Mutex
	groups map[string]string // chat JID → its name
	last   map[string]time.Time
	busy   map[string]bool
	hour   []time.Time
}

// Open opens the store and the client (not yet connected; LoggedIn tells whether it is linked).
func Open(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.MinGap == 0 {
		cfg.MinGap = 10 * time.Second
	}
	if cfg.PerHour == 0 {
		cfg.PerHour = 30
	}
	if len(cfg.Names) == 0 {
		cfg.Names = []string{"nina"}
	}
	if cfg.Mode == "self" && len(cfg.Keep) == 0 {
		cfg.Keep = []string{"self"} // a primary number: none of the person's other chats kept unless named (claudia, 2026-11-26)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Store), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+cfg.Store+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	container := sqlstore.NewWithDB(db, "sqlite3", waLog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("whatsapp: store: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS nina_messages (
		id TEXT, chat TEXT, chat_name TEXT, sender TEXT, sender_name TEXT, at INTEGER, text TEXT, kind TEXT,
		from_me INTEGER, addressed INTEGER, PRIMARY KEY (chat, id));
		CREATE INDEX IF NOT EXISTS nina_messages_at ON nina_messages (chat, at)`); err != nil {
		return nil, err
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, err
	}
	lg := waLog.Noop
	if cfg.Debug {
		lg = waLog.Stdout("whatsmeow", "DEBUG", true)
	}
	c := &Client{cfg: cfg, db: db, cli: whatsmeow.NewClient(dev, lg), name: nameRe(cfg.Names),
		groups: map[string]string{}, last: map[string]time.Time{}, busy: map[string]bool{}}
	c.cli.AddEventHandler(c.on)
	return c, nil
}

// nameRe: her names as words of their own (lower case: the text is lowered before).
func nameRe(names []string) *regexp.Regexp {
	var words []string
	for _, n := range names {
		words = append(words, regexp.QuoteMeta(strings.ToLower(n)))
	}
	return regexp.MustCompile(`(^|[^\pL])(` + strings.Join(words, "|") + `)([^\pL]|$)`)
}

func lower(s string) string { return strings.ToLower(s) }

// LoggedIn: linked to a number.
func (c *Client) LoggedIn() bool { return c.cli.Store.ID != nil }

// Link pairs her as a linked device of the phone's number: the 8-character code to enter in WhatsApp
// on that phone (Linked devices → Link a device → Link with phone number instead). The pairing session
// first (the QR channel, before connecting), its first event, then the code (whatsmeow: asked earlier,
// the phone answered "couldn't link device", 2026-11-26). done reports the session's end: "success",
// "timeout" (the login socket closes after ~160 s), or an error's text.
func (c *Client) Link(ctx context.Context, phone string) (code string, done <-chan string, err error) {
	qr, err := c.cli.GetQRChannel(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := c.cli.ConnectContext(ctx); err != nil {
		return "", nil, err
	}
	select {
	case <-qr:
	case <-time.After(20 * time.Second):
		return "", nil, fmt.Errorf("whatsapp: no pairing session from the server")
	}
	code, err = c.cli.PairPhone(ctx, strings.TrimPrefix(strings.ReplaceAll(phone, " ", ""), "+"), true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return "", nil, err
	}
	out := make(chan string, 1)
	go func() {
		for it := range qr {
			switch it.Event {
			case "code":
				continue
			case "success":
				out <- "success"
			case "error":
				out <- fmt.Sprintf("error: %v", it.Error)
			default:
				out <- it.Event
			}
			return
		}
		out <- "closed"
	}()
	return code, out, nil
}

// LinkQR pairs her by a QR code scanned on the phone (Linked devices → Link a device): the channel of
// codes ("code": a new one to show, every 20 s or so) up to "success", "timeout" or "error".
func (c *Client) LinkQR(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	qr, err := c.cli.GetQRChannel(ctx)
	if err != nil {
		return nil, err
	}
	return qr, c.cli.ConnectContext(ctx)
}

// Connect starts the channel (linked).
func (c *Client) Connect(ctx context.Context) error {
	if !c.LoggedIn() {
		return fmt.Errorf("whatsapp: not linked (nina-whatsapp link PHONE)")
	}
	if err := c.cli.ConnectContext(ctx); err != nil {
		return err
	}
	go func() { time.Sleep(3 * time.Second); c.loadGroups(ctx); c.screen() }()
	return nil
}

// Close disconnects.
func (c *Client) Close() { c.cli.Disconnect(); c.db.Close() }

// Groups are the joined groups: JID → name.
func (c *Client) Groups(ctx context.Context) map[string]string {
	c.loadGroups(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for k, v := range c.groups {
		out[k] = v
	}
	return out
}

func (c *Client) loadGroups(ctx context.Context) {
	gs, err := c.cli.GetJoinedGroups(ctx)
	if err != nil {
		return
	}
	c.mu.Lock()
	for _, g := range gs {
		c.groups[g.JID.String()] = g.Name
	}
	c.mu.Unlock()
}

func (c *Client) groupName(ctx context.Context, jid types.JID) string {
	c.mu.Lock()
	n, ok := c.groups[jid.String()]
	c.mu.Unlock()
	if ok {
		return n
	}
	if g, err := c.cli.GetGroupInfo(ctx, jid); err == nil {
		c.mu.Lock()
		c.groups[jid.String()] = g.Name
		c.mu.Unlock()
		return g.Name
	}
	return jid.User
}

// text: what a message says, and its kind.
func text(m *waE2E.Message) (string, string) {
	switch {
	case m.GetConversation() != "":
		return m.GetConversation(), "text"
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetText(), "text"
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetCaption(), "image"
	case m.GetAudioMessage() != nil:
		return "", "voice"
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetCaption(), "video"
	case m.GetDocumentMessage() != nil:
		return m.GetDocumentMessage().GetFileName(), "document"
	case m.GetStickerMessage() != nil:
		return "", "sticker"
	}
	return "", "other"
}

func contextOf(m *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetContextInfo()
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetContextInfo()
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetContextInfo()
	case m.GetAudioMessage() != nil:
		return m.GetAudioMessage().GetContextInfo()
	}
	return nil
}

// mine: a JID is hers (her number's, or her LID in groups).
func (c *Client) mine(j string) bool {
	if j == "" || c.cli.Store.ID == nil {
		return false
	}
	p, err := types.ParseJID(j)
	if err != nil {
		return false
	}
	return p.User == c.cli.Store.ID.User || (!c.cli.Store.LID.IsEmpty() && p.User == c.cli.Store.LID.User)
}

func (c *Client) on(evt any) {
	switch e := evt.(type) {
	case *events.Message:
		c.message(e)
	case *events.Connected:
		// online: WhatsApp delivers a client's read receipts (blue ticks) only while it is (the user,
		// 2026-11-26: no blue ticks); a presence needs a push name: the account's, or Nina
		go func() {
			if c.cli.Store.PushName == "" {
				c.cli.Store.PushName = "Nina"
			}
			if err := c.cli.SendPresence(context.Background(), types.PresenceAvailable); err != nil {
				c.log("wa_error", map[string]any{"err": "presence: " + err.Error()})
			}
		}()
		c.log("wa_status", map[string]any{"state": "connected"})
	case *events.Disconnected:
		c.log("wa_status", map[string]any{"state": "disconnected"})
	case *events.LoggedOut:
		c.log("wa_status", map[string]any{"state": "logged_out", "reason": e.Reason.String()})
	}
}

// isSelf: the number's own chat ("Message yourself").
func (c *Client) isSelf(chat types.JID) bool {
	if c.cli.Store.ID == nil || chat.Server == types.GroupServer {
		return false
	}
	return chat.User == c.cli.Store.ID.User || (!c.cli.Store.LID.IsEmpty() && chat.User == c.cli.Store.LID.User)
}

// keeps: whether a chat is kept (Keep: "self", its name, "all"; none listed: all).
func (c *Client) keeps(name string, self bool) bool {
	if len(c.cfg.Keep) == 0 {
		return true
	}
	for _, k := range c.cfg.Keep {
		if strings.EqualFold(k, "all") || (self && strings.EqualFold(k, "self")) || strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// Prefix: her words in the self-chat, where hers and the person's both appear as theirs.
const Prefix = "Nina: "

func (c *Client) message(e *events.Message) {
	ctx := context.Background()
	t, kind := text(e.Message)
	if kind == "other" && t == "" {
		return // receipts, reactions, protocol messages
	}
	if e.Info.IsFromMe && c.cli.Store.ID != nil && e.Info.Sender.Device == c.cli.Store.ID.Device {
		return // sent by her (this device): kept by Send, never answered
	}
	self := c.isSelf(e.Info.Chat)
	m := Msg{ID: e.Info.ID, Chat: e.Info.Chat.String(), Sender: e.Info.Sender.ToNonAD().String(), SenderName: e.Info.PushName,
		At: e.Info.Timestamp, Text: t, Kind: kind, FromMe: e.Info.IsFromMe}
	switch {
	case e.Info.IsGroup:
		m.ChatName = c.groupName(ctx, e.Info.Chat)
	case self:
		m.ChatName = "self"
	default:
		m.ChatName = e.Info.PushName
	}
	if !c.keeps(m.ChatName, self) {
		return // not hers to keep (the person's other chats)
	}
	switch c.cfg.Mode {
	case "self":
		m.Addressed = self && strings.TrimSpace(t) != "" && !strings.HasPrefix(t, Prefix)
	case "none":
	default:
		c.addressed(&m, e, t)
	}
	c.keep(m)
	if c.shown(m.ChatName) {
		c.log("wa_message", map[string]any{"msg": m})
		c.screen()
	}
	if m.Addressed {
		sender := e.Info.Sender // a group's: who wrote it; a direct chat's: none
		if !e.Info.IsGroup {
			sender = types.EmptyJID
		}
		if err := c.cli.MarkRead(ctx, []types.MessageID{e.Info.ID}, time.Now(), e.Info.Chat, sender); err != nil {
			c.log("wa_error", map[string]any{"err": "read: " + err.Error()})
		}
		c.ask(Request{Msg: m, Group: e.Info.IsGroup, Self: self}, e)
	}
}

// addressed: "named" — a direct message, or a group's naming her or replying to hers.
func (c *Client) addressed(m *Msg, e *events.Message, t string) {
	if !m.FromMe {
		switch ci := contextOf(e.Message); {
		case !e.Info.IsGroup:
			m.Addressed = true
		case c.name.MatchString(lower(t)):
			m.Addressed = true
		case ci != nil:
			for _, j := range ci.GetMentionedJID() {
				m.Addressed = m.Addressed || c.mine(j)
			}
			m.Addressed = m.Addressed || c.mine(ci.GetParticipant())
		}
	}
}

func (c *Client) keep(m Msg) {
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	_, _ = c.db.Exec(`INSERT OR IGNORE INTO nina_messages VALUES (?,?,?,?,?,?,?,?,?,?)`, m.ID, m.Chat, m.ChatName, m.Sender,
		m.SenderName, m.At.Unix(), m.Text, m.Kind, b(m.FromMe), b(m.Addressed))
}

// Recent: a chat's last n messages, oldest first.
func (c *Client) Recent(chat string, n int) []Msg {
	rows, err := c.db.Query(`SELECT id, chat, chat_name, sender, sender_name, at, text, kind, from_me, addressed FROM nina_messages
		WHERE chat = ? ORDER BY at DESC LIMIT ?`, chat, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Msg
	for rows.Next() {
		var m Msg
		var at int64
		var me, to int
		if rows.Scan(&m.ID, &m.Chat, &m.ChatName, &m.Sender, &m.SenderName, &at, &m.Text, &m.Kind, &me, &to) == nil {
			m.At, m.FromMe, m.Addressed = time.Unix(at, 0), me == 1, to == 1
			out = append([]Msg{m}, out...)
		}
	}
	return out
}

// ByName: a chat's JID from its name (a group's, or a person's in a direct chat), the latest.
func (c *Client) ByName(name string) string {
	var chat string
	_ = c.db.QueryRow(`SELECT chat FROM nina_messages WHERE lower(chat_name) = lower(?) ORDER BY at DESC LIMIT 1`, name).Scan(&chat)
	return chat
}

// SetShow: the groups on her screen (her settings), the card told at once.
func (c *Client) SetShow(names []string) {
	c.mu.Lock()
	c.cfg.Show = append([]string(nil), names...)
	c.mu.Unlock()
	go c.screen()
}

// Shown: the groups on her screen.
func (c *Client) Shown() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.cfg.Show...)
}

func (c *Client) shown(chatName string) bool {
	for _, s := range c.Shown() {
		if strings.EqualFold(s, chatName) {
			return true
		}
	}
	return false
}

// screen: the shown groups' recent messages, for her card.
func (c *Client) screen() {
	var chats []map[string]any
	for _, s := range c.Shown() {
		chat := c.ByName(s)
		if chat == "" {
			c.mu.Lock()
			for j, n := range c.groups {
				if strings.EqualFold(n, s) {
					chat = j
				}
			}
			c.mu.Unlock()
		}
		chats = append(chats, map[string]any{"name": s, "chat": chat, "messages": c.Recent(chat, 30)})
	}
	c.log("whatsapp", map[string]any{"chats": chats, "linked": c.LoggedIn()})
}

func (c *Client) log(kind string, data map[string]any) {
	if c.cfg.Log != nil {
		c.cfg.Log(kind, data)
	}
}

// ask: the request to her task runner, throttled, and her answer to the chat.
func (c *Client) ask(r Request, e *events.Message) {
	c.mu.Lock()
	now := time.Now()
	keep := c.hour[:0]
	for _, t := range c.hour {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	c.hour = keep
	if len(c.hour) >= c.cfg.PerHour || c.busy[r.Chat] {
		c.mu.Unlock()
		c.log("wa_throttled", map[string]any{"chat": r.ChatName, "from": r.SenderName, "text": r.Text})
		return
	}
	c.busy[r.Chat] = true
	c.hour = append(c.hour, now)
	wait := c.cfg.MinGap - now.Sub(c.last[r.Chat])
	c.mu.Unlock()
	go func() {
		defer func() { c.mu.Lock(); c.busy[r.Chat], c.last[r.Chat] = false, time.Now(); c.mu.Unlock() }()
		if wait > 0 {
			time.Sleep(wait)
		}
		// 100 s for her answer: past it (her models overloaded), a word instead of silence (2026-11-26: a
		// "test 1" waited on Google's "high demand" for the task's five minutes, unanswered)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		_ = c.cli.SendChatPresence(ctx, e.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		r.Recent = c.Recent(r.Chat, 12)
		c.log("wa_asked", map[string]any{"chat": r.ChatName, "from": r.SenderName, "text": r.Text})
		ans, err := c.cfg.Answer(ctx, r)
		_ = c.cli.SendChatPresence(ctx, e.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
		if err != nil {
			if c.cfg.Strain != nil {
				c.cfg.Strain(err)
			}
			c.log("wa_error", map[string]any{"chat": r.ChatName, "err": err.Error()})
			sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer scancel()
			_ = c.Send(sctx, e.Info.Chat, Busy, e)
			return
		}
		if strings.TrimSpace(ans) == "" {
			return
		}
		if err := c.Send(ctx, e.Info.Chat, ans, e); err != nil {
			c.log("wa_error", map[string]any{"chat": r.ChatName, "err": err.Error()})
		}
	}()
}

// Send posts text to a chat; in a group, quoting the message it answers (nil: none).
func (c *Client) Send(ctx context.Context, chat types.JID, body string, quote *events.Message) error {
	if c.isSelf(chat) && !strings.HasPrefix(body, Prefix) {
		body = Prefix + body
	}
	msg := &waE2E.Message{Conversation: proto.String(body)}
	if quote != nil && quote.Info.IsGroup {
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(body), ContextInfo: &waE2E.ContextInfo{
			StanzaID: proto.String(quote.Info.ID), Participant: proto.String(quote.Info.Sender.ToNonAD().String()), QuotedMessage: quote.Message}}}
	}
	resp, err := c.cli.SendMessage(ctx, chat, msg)
	if err != nil {
		return err
	}
	name := chat.User
	if c.isSelf(chat) {
		name = "self"
	} else if chat.Server == types.GroupServer {
		name = c.groupName(ctx, chat)
	} else if quote != nil {
		name = quote.Info.PushName
	}
	m := Msg{ID: resp.ID, Chat: chat.String(), ChatName: name, Sender: c.cli.Store.ID.ToNonAD().String(), SenderName: "Nina",
		At: resp.Timestamp, Text: body, Kind: "text", FromMe: true}
	c.keep(m)
	if c.shown(name) {
		c.log("wa_message", map[string]any{"msg": m})
		c.screen()
	}
	return nil
}
