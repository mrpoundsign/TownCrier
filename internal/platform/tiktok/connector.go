package tiktok

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/steampoweredtaco/gotiktoklive"
	pb "github.com/steampoweredtaco/gotiktoklive/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/mrpoundsign/TownCrier/internal/chat"
)

// ConnectionStatus represents the live TikTok connection state.
type ConnectionStatus string

const (
	StatusDisconnected     ConnectionStatus = "disconnected"
	StatusWaitingForStream ConnectionStatus = "waiting"
	StatusConnected        ConnectionStatus = "connected"
)

const defaultStreamWaitPoll = 15 * time.Second

// NormalizeUsername extracts the clean TikTok uniqueId from a @handle, username, or TikTok URL.
func NormalizeUsername(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if u, err := url.Parse(trimmed); err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 && parts[0] != "" {
				return strings.TrimPrefix(parts[0], "@")
			}
		}
	}
	return strings.TrimPrefix(trimmed, "@")
}

// Connector manages zero-config TikTok Live WebCast tracking and gift streak debouncing.
type Connector struct {
	mu             sync.Mutex
	waitInterval   time.Duration
	debounceWindow time.Duration
	username       string
	status         ConnectionStatus
	running        bool
	cancelFn       context.CancelFunc
	debouncer      *GiftDebouncer
	catalog        *GiftCatalog
	trackUserFn    func(username string) (<-chan gotiktoklive.Event, func(), error)
	onEvent        func(event *chat.Event)
	onStatusChange func(status ConnectionStatus, detail string)
}

// NewConnector creates a new TikTok Live connector.
func NewConnector(
	onEvent func(event *chat.Event),
	onStatusChange func(status ConnectionStatus, detail string),
) *Connector {
	c := &Connector{
		waitInterval:   defaultStreamWaitPoll,
		debounceWindow: DefaultGiftDebounceWindow,
		status:         StatusDisconnected,
		catalog:        NewGiftCatalog(),
		onEvent:        onEvent,
		onStatusChange: onStatusChange,
	}
	c.trackUserFn = c.defaultTrackUser
	return c
}

// Catalog returns the TikTok gift metadata and icon catalog.
func (c *Connector) Catalog() *GiftCatalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.catalog
}

func (c *Connector) defaultTrackUser(username string) (<-chan gotiktoklive.Event, func(), error) {
	tt, err := gotiktoklive.NewTikTok()
	if err != nil {
		return nil, nil, err
	}
	tt.Debug = true
	tt.SetDebugHandler(func(args ...any) {
		c.handleDebugArgs(args...)
	})
	tt.SetErrorHandler(func(args ...any) {
		log.Printf("[TikTok] WebCast warning/error: %v", args)
	})
	tt.SetWarnHandler(func(args ...any) {
		log.Printf("[TikTok] WebCast warn: %v", args)
	})

	live, err := tt.TrackUser(username)
	if err != nil {
		return nil, nil, err
	}
	if live.GiftInfo != nil && c.catalog != nil {
		c.catalog.PopulateFromGiftInfo(live.GiftInfo)
	}
	return live.Events, live.Close, nil
}

// Start begins watching the given TikTok @username or live URL.
func (c *Connector) Start(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleanUser := NormalizeUsername(target)
	if cleanUser == "" {
		return errors.New("empty TikTok username")
	}

	if c.running && c.username == cleanUser {
		return nil
	}

	c.stopLoopLocked()
	c.username = cleanUser
	c.running = true
	c.debouncer = NewGiftDebouncer(c.debounceWindow, func(ev *chat.Event) {
		if ev != nil && ev.Channel == "" {
			ev.Channel = cleanUser
		}
		if c.onEvent != nil {
			c.onEvent(ev)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.cancelFn = cancel

	go c.runLoop(ctx, cleanUser)
	return nil
}

// Stop halts the TikTok Live connector and cancels pending gift timers.
func (c *Connector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.running = false
	c.stopLoopLocked()
	c.updateStatusLocked(StatusDisconnected, "Stopped")
	return nil
}

// Status returns the current connection status.
func (c *Connector) Status() ConnectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Username returns the active TikTok username.
func (c *Connector) Username() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.username
}

func (c *Connector) stopLoopLocked() {
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
	}
	if c.debouncer != nil {
		c.debouncer.Close()
		c.debouncer = nil
	}
}

func (c *Connector) updateStatus(status ConnectionStatus, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running && status != StatusDisconnected {
		return
	}
	c.updateStatusLocked(status, detail)
}

func (c *Connector) updateStatusLocked(status ConnectionStatus, detail string) {
	c.status = status
	if fn := c.onStatusChange; fn != nil {
		go fn(status, detail)
	}
}

func (c *Connector) runLoop(ctx context.Context, username string) {
	display := "@" + username
	for {
		if ctx.Err() != nil {
			return
		}

		c.updateStatus(StatusWaitingForStream, display)
		eventsCh, closeFn, err := c.trackUserFn(username)
		if err != nil || eventsCh == nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.waitInterval):
				continue
			}
		}

		log.Printf("[TikTok] Connected to live WebCast for %s", display)
		c.updateStatus(StatusConnected, display)

		c.consumeStreamEvents(ctx, eventsCh, closeFn)
	}
}

func (c *Connector) consumeStreamEvents(ctx context.Context, eventsCh <-chan gotiktoklive.Event, closeFn func()) {
	defer func() {
		if closeFn != nil {
			closeFn()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case rawEv, ok := <-eventsCh:
			if !ok {
				return
			}
			if shouldDisconnect := c.handleRawEvent(rawEv); shouldDisconnect {
				return
			}
		}
	}
}

func (c *Connector) handleRawEvent(rawEv gotiktoklive.Event) bool {
	if rawEv == nil {
		return false
	}

	switch e := rawEv.(type) {
	case gotiktoklive.ChatEvent:
		if e.IsHistory() {
			return false
		}
		ev := mapChatEvent(e)
		if ev != nil {
			if ev.Channel == "" {
				ev.Channel = c.Username()
			}
			if c.onEvent != nil {
				c.onEvent(ev)
			}
		}

	case gotiktoklive.GiftEvent:
		if e.IsHistory() {
			return false
		}
		in := c.mapGiftEvent(e)
		c.mu.Lock()
		deb := c.debouncer
		c.mu.Unlock()
		if deb != nil {
			deb.Submit(in)
		}

	case gotiktoklive.UserEvent:
		if e.IsHistory() {
			return false
		}
		ev := mapUserEvent(e)
		if ev != nil {
			if ev.Channel == "" {
				ev.Channel = c.Username()
			}
			if c.onEvent != nil {
				c.onEvent(ev)
			}
		}

	case gotiktoklive.ControlEvent:
		// Action == 3 indicates the livestream has ended
		if e.Action == 3 {
			return true
		}

	case gotiktoklive.DisconnectEvent:
		return true
	}

	return false
}

func mapChatEvent(e gotiktoklive.ChatEvent) *chat.Event {
	text := strings.TrimSpace(e.Comment)
	if text == "" {
		return nil
	}
	userID, userName, displayName := extractUserFields(e.User)
	roles, fanClubLevel := extractRoles(e.User, e.UserIdentity)

	ts := time.Now()
	if e.Timestamp > 0 {
		ts = parseTikTokTimestamp(e.Timestamp)
	}

	msgID := "tt-msg-" + strconv.FormatInt(e.MessageID, 10)
	if e.MessageID == 0 {
		msgID = "tt-msg-" + strconv.FormatInt(ts.UnixNano(), 10)
	}

	return &chat.Event{
		ID:           msgID,
		Platform:     "tiktok",
		Kind:         chat.EventKindMessage,
		Timestamp:    ts,
		UserID:       userID,
		UserName:     userName,
		DisplayName:  displayName,
		Roles:        roles,
		FanClubLevel: fanClubLevel,
		Text:         text,
	}
}

func (c *Connector) mapGiftEvent(e gotiktoklive.GiftEvent) GiftInput {
	userID, userName, displayName := extractUserFields(e.User)
	roles, fanClubLevel := extractRoles(e.User, e.UserIdentity)

	ts := time.Now()
	if e.Timestamp > 0 {
		ts = parseTikTokTimestamp(e.Timestamp)
	}

	msgID := ""
	if e.MessageID != 0 {
		msgID = "tt-gift-" + strconv.FormatInt(e.MessageID, 10)
	}

	giftName := strings.TrimSpace(e.Name)
	unitDiamonds := e.Diamonds
	var imageURL string
	if c != nil && c.catalog != nil {
		if entry, ok := c.catalog.Lookup(e.ID, giftName); ok {
			if giftName == "" && entry.Name != "" {
				giftName = entry.Name
			}
			if unitDiamonds <= 0 && entry.Diamonds > 0 {
				unitDiamonds = entry.Diamonds
			}
			imageURL = entry.ImageURL
		}
	}

	return GiftInput{
		MessageID:    msgID,
		UserID:       userID,
		UserName:     userName,
		DisplayName:  displayName,
		Roles:        roles,
		FanClubLevel: fanClubLevel,
		GiftID:       e.ID,
		GiftName:     giftName,
		UnitDiamonds: unitDiamonds,
		RepeatCount:  e.RepeatCount,
		RepeatEnd:    e.RepeatEnd,
		IsStreakable: e.Type == 1 || e.IsComboGift,
		ImageURL:     imageURL,
		Timestamp:    ts,
	}
}

// ParseDebugProtoMessage decodes a gotiktoklive debugHandler "unimplemented type *gotiktoklive.<Type>\n<base64>" string
// into its typed protobuf message.
func ParseDebugProtoMessage(raw string) (proto.Message, error) {
	const prefix = "unimplemented type "
	_, after, ok0 := strings.Cut(raw, prefix)
	if !ok0 {
		return nil, nil
	}
	typePart, b64Part, ok := strings.Cut(after, "\n")
	if !ok {
		return nil, nil
	}
	typeName := strings.TrimSpace(typePart)
	typeName = strings.TrimPrefix(typeName, "*")
	if dot := strings.LastIndexByte(typeName, '.'); dot >= 0 {
		typeName = typeName[dot+1:]
	}
	if typeName == "" {
		return nil, nil
	}

	b64Clean := strings.TrimSpace(b64Part)
	if b64Clean == "" {
		return nil, nil
	}
	payload, err := base64.RawStdEncoding.DecodeString(b64Clean)
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(b64Clean)
		if err != nil {
			return nil, fmt.Errorf("decode base64 proto payload for %s: %w", typeName, err)
		}
	}

	var msg proto.Message
	switch typeName {
	case "WebcastBarrageMessage":
		msg = &pb.WebcastBarrageMessage{}
	case "WebcastEmoteChatMessage":
		msg = &pb.WebcastEmoteChatMessage{}
	case "WebcastSubNotifyMessage":
		msg = &pb.WebcastSubNotifyMessage{}
	default:
		mt, findErr := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName("TikTok." + typeName))
		if findErr != nil {
			mt, findErr = protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(typeName))
		}
		if findErr != nil {
			return nil, fmt.Errorf("find proto message %s: %w", typeName, findErr)
		}
		msg = mt.New().Interface()
	}

	if err := proto.Unmarshal(payload, msg); err != nil {
		return nil, fmt.Errorf("unmarshal proto %s: %w", typeName, err)
	}
	return msg, nil
}

func (c *Connector) handleDebugArgs(args ...any) {
	for _, arg := range args {
		if arg == nil {
			continue
		}
		msg, err := ParseDebugProtoMessage(fmt.Sprint(arg))
		if err != nil || msg == nil {
			continue
		}
		c.HandleProtoMessage(msg)
	}
}

// HandleProtoMessage translates unimplemented gotiktoklive protobuf messages (Barrage, EmoteChat, SubNotify)
// into normalized TownCrier events.
func (c *Connector) HandleProtoMessage(msg proto.Message) {
	if msg == nil {
		return
	}

	switch pt := msg.(type) {
	case *pb.WebcastBarrageMessage:
		c.handleBarrageMessage(pt)
	case *pb.WebcastEmoteChatMessage:
		if ev := mapEmoteChatMessage(pt); ev != nil {
			c.emitEvent(ev)
		}
	case *pb.WebcastSubNotifyMessage:
		if ev := mapSubNotifyMessage(pt); ev != nil {
			c.emitEvent(ev)
		}
	}
}

func (c *Connector) emitEvent(ev *chat.Event) {
	if ev == nil {
		return
	}
	if ev.Channel == "" {
		ev.Channel = c.Username()
	}
	if c.onEvent != nil {
		c.onEvent(ev)
	}
}

func (c *Connector) handleBarrageMessage(pt *pb.WebcastBarrageMessage) {
	if pt == nil {
		return
	}

	ts := time.Now()
	var msgID int64
	if cm := pt.GetCommon(); cm != nil {
		msgID = cm.GetMsgId()
		if cm.GetCreateTime() > 0 {
			ts = parseTikTokTimestamp(cm.GetCreateTime())
		}
	}

	switch pt.GetMsgType() {
	case pb.WebcastBarrageMessage_FANSLEVELUPGRADE:
		var pbUser *pb.User
		var grade int
		if fl := pt.GetFansLevelParam(); fl != nil {
			pbUser = fl.GetUser()
			grade = int(fl.GetCurrentGrade())
		}
		if pbUser == nil {
			pbUser = firstUserInText(pt.GetContent())
		}
		u := protoUserToTikTokUser(pbUser)
		userID, userName, displayName := extractUserFields(u)
		roles, fanLvl := extractRoles(u, nil)
		if grade > fanLvl {
			fanLvl = grade
		}
		iconURL := firstProtoImageURL(pt.GetIcon())
		if iconURL == "" && c.catalog != nil {
			if entry, ok := c.catalog.Lookup(DefaultHeartMeGiftID, "Heart Me"); ok {
				iconURL = entry.ImageURL
			}
		}
		if iconURL == "" {
			iconURL = DefaultHeartMeIconURL
		}

		idStr := ""
		if msgID != 0 {
			idStr = "tt-barrage-gift-" + strconv.FormatInt(msgID, 10)
		}
		in := GiftInput{
			MessageID:    idStr,
			UserID:       userID,
			UserName:     userName,
			DisplayName:  displayName,
			Roles:        roles,
			FanClubLevel: fanLvl,
			GiftID:       DefaultHeartMeGiftID,
			GiftName:     "Heart Me",
			UnitDiamonds: 1,
			RepeatCount:  1,
			RepeatEnd:    true,
			IsStreakable: false,
			ImageURL:     iconURL,
			Timestamp:    ts,
		}
		c.mu.Lock()
		deb := c.debouncer
		c.mu.Unlock()
		if deb != nil {
			deb.Submit(in)
		} else {
			c.emitEvent(buildGiftChatEvent(in))
		}

	case pb.WebcastBarrageMessage_SUBSCRIBE:
		pbUser := firstUserInText(pt.GetContent())
		u := protoUserToTikTokUser(pbUser)
		userID, userName, displayName := extractUserFields(u)
		roles, fanLvl := extractRoles(u, nil)
		idStr := fmt.Sprintf("tt-sub-%s-%d", userID, ts.UnixNano())
		if msgID != 0 {
			idStr = "tt-sub-" + strconv.FormatInt(msgID, 10)
		}
		c.emitEvent(&chat.Event{
			ID:           idStr,
			Platform:     "tiktok",
			Kind:         chat.EventKindSub,
			Timestamp:    ts,
			UserID:       userID,
			UserName:     userName,
			DisplayName:  displayName,
			Roles:        roles,
			FanClubLevel: fanLvl,
			Months:       1,
		})

	case pb.WebcastBarrageMessage_SUBSCRIBEGIFT:
		pbUser := firstUserInText(pt.GetContent())
		u := protoUserToTikTokUser(pbUser)
		userID, userName, displayName := extractUserFields(u)
		roles, fanLvl := extractRoles(u, nil)
		count := 1
		if sg := pt.GetSubscribeGiftParam(); sg != nil && sg.GetGiftSubCount() > 0 {
			count = int(sg.GetGiftSubCount())
		}
		idStr := fmt.Sprintf("tt-giftsub-%s-%d", userID, ts.UnixNano())
		if msgID != 0 {
			idStr = "tt-giftsub-" + strconv.FormatInt(msgID, 10)
		}
		c.emitEvent(&chat.Event{
			ID:           idStr,
			Platform:     "tiktok",
			Kind:         chat.EventKindGiftSub,
			Timestamp:    ts,
			UserID:       userID,
			UserName:     userName,
			DisplayName:  displayName,
			Roles:        roles,
			FanClubLevel: fanLvl,
			Count:        count,
		})
	}
}

func mapEmoteChatMessage(pt *pb.WebcastEmoteChatMessage) *chat.Event {
	if pt == nil || len(pt.GetEmoteList()) == 0 {
		return nil
	}
	u := protoUserToTikTokUser(pt.GetUser())
	ident := protoIdentityToTikTokIdentity(pt.GetUserIdentity())
	userID, userName, displayName := extractUserFields(u)
	roles, fanLvl := extractRoles(u, ident)

	ts := time.Now()
	var msgID int64
	if cm := pt.GetCommon(); cm != nil {
		msgID = cm.GetMsgId()
		if cm.GetCreateTime() > 0 {
			ts = parseTikTokTimestamp(cm.GetCreateTime())
		}
	}

	var sb strings.Builder
	var spans []chat.EmoteSpan
	runeOffset := 0

	for _, em := range pt.GetEmoteList() {
		if em == nil {
			continue
		}
		emID := strings.TrimSpace(em.GetEmoteId())
		if emID == "" {
			emID = "emote"
		}
		token := ":emote_" + emID + ":"
		imgURL := firstProtoImageURL(em.GetImage())

		if sb.Len() > 0 {
			sb.WriteByte(' ')
			runeOffset++
		}
		tokenRunes := utf8.RuneCountInString(token)
		sb.WriteString(token)
		spans = append(spans, chat.EmoteSpan{
			Start: runeOffset,
			End:   runeOffset + tokenRunes,
			ID:    emID,
			Name:  token,
			URL:   imgURL,
		})
		runeOffset += tokenRunes
	}

	if sb.Len() == 0 {
		return nil
	}

	idStr := fmt.Sprintf("tt-emote-%s-%d", userID, ts.UnixNano())
	if msgID != 0 {
		idStr = "tt-emote-" + strconv.FormatInt(msgID, 10)
	}

	return &chat.Event{
		ID:           idStr,
		Platform:     "tiktok",
		Kind:         chat.EventKindMessage,
		Timestamp:    ts,
		UserID:       userID,
		UserName:     userName,
		DisplayName:  displayName,
		Roles:        roles,
		FanClubLevel: fanLvl,
		Text:         sb.String(),
		EmoteSpans:   spans,
	}
}

func mapSubNotifyMessage(pt *pb.WebcastSubNotifyMessage) *chat.Event {
	if pt == nil {
		return nil
	}
	u := protoUserToTikTokUser(pt.GetUser())
	userID, userName, displayName := extractUserFields(u)
	roles, fanLvl := extractRoles(u, nil)

	ts := time.Now()
	var msgID int64
	if cm := pt.GetCommon(); cm != nil {
		msgID = cm.GetMsgId()
		if cm.GetCreateTime() > 0 {
			ts = parseTikTokTimestamp(cm.GetCreateTime())
		}
	}

	months := int(pt.GetSubMonth())
	if months <= 0 {
		months = 1
	}
	kind := chat.EventKindSub
	if months > 1 {
		kind = chat.EventKindResub
	}

	idStr := fmt.Sprintf("tt-subnotify-%s-%d", userID, ts.UnixNano())
	if msgID != 0 {
		idStr = "tt-subnotify-" + strconv.FormatInt(msgID, 10)
	}

	return &chat.Event{
		ID:           idStr,
		Platform:     "tiktok",
		Kind:         kind,
		Timestamp:    ts,
		UserID:       userID,
		UserName:     userName,
		DisplayName:  displayName,
		Roles:        roles,
		FanClubLevel: fanLvl,
		Months:       months,
	}
}

func firstProtoImageURL(img *pb.Image) string {
	if img == nil {
		return ""
	}
	return firstNonEmptyURL(img.GetUrlList())
}

func firstUserInText(txt *pb.Text) *pb.User {
	if txt == nil {
		return nil
	}
	for _, p := range txt.GetPiecesList() {
		if uv := p.GetUserValue(); uv != nil && uv.GetUser() != nil {
			return uv.GetUser()
		}
	}
	return nil
}

func protoUserToTikTokUser(u *pb.User) *gotiktoklive.User {
	if u == nil {
		return nil
	}
	username := u.GetDisplayId()
	if username == "" {
		username = u.GetIdStr()
	}
	if username == "" {
		username = u.GetNickname()
	}
	out := &gotiktoklive.User{
		ID:       u.GetId(),
		Username: username,
		Nickname: u.GetNickname(),
	}
	if bl := u.GetBadgeList(); len(bl) > 0 {
		var badges []*gotiktoklive.UserBadge
		for _, b := range bl {
			if b == nil {
				continue
			}
			badges = append(badges, &gotiktoklive.UserBadge{
				Type: b.GetDisplayType().String(),
				Name: b.String(),
			})
		}
		out.Badge = &gotiktoklive.BadgeAttributes{Badges: badges}
	}
	return out
}

func protoIdentityToTikTokIdentity(uid *pb.UserIdentity) *gotiktoklive.UserIdentity {
	if uid == nil {
		return nil
	}
	return &gotiktoklive.UserIdentity{
		IsGiftGiver:       uid.GetIsGiftGiverOfAnchor(),
		IsSubscriber:      uid.GetIsSubscriberOfAnchor(),
		IsMutualFollowing: uid.GetIsMutualFollowingWithAnchor(),
		IsFollower:        uid.GetIsFollowerOfAnchor(),
		IsModerator:       uid.GetIsModeratorOfAnchor(),
		IsAnchor:          uid.GetIsAnchor(),
	}
}

func mapUserEvent(e gotiktoklive.UserEvent) *chat.Event {
	if e.Event != gotiktoklive.USER_FOLLOW {
		return nil
	}
	userID, userName, displayName := extractUserFields(e.User)
	roles, fanClubLevel := extractRoles(e.User, nil)

	ts := time.Now()
	if e.Timestamp > 0 {
		ts = parseTikTokTimestamp(e.Timestamp)
	}

	msgID := "tt-follow-" + strconv.FormatInt(e.MessageID, 10)
	if e.MessageID == 0 {
		msgID = "tt-follow-" + userID + "-" + strconv.FormatInt(ts.UnixNano(), 10)
	}

	return &chat.Event{
		ID:           msgID,
		Platform:     "tiktok",
		Kind:         chat.EventKindFollow,
		Timestamp:    ts,
		UserID:       userID,
		UserName:     userName,
		DisplayName:  displayName,
		Roles:        roles,
		FanClubLevel: fanClubLevel,
	}
}

func extractUserFields(u *gotiktoklive.User) (userID, userName, displayName string) {
	if u == nil {
		return "unknown", "Viewer", "Viewer"
	}
	if u.ID != 0 {
		userID = strconv.FormatInt(u.ID, 10)
	}
	userName = strings.TrimLeft(strings.TrimSpace(u.Username), "@")
	displayName = strings.TrimLeft(strings.TrimSpace(u.Nickname), "@")
	if displayName == "" {
		displayName = userName
	}
	if userName == "" {
		userName = displayName
	}
	if userID == "" {
		userID = userName
	}
	return userID, userName, displayName
}

var (
	reBadgeStrLevel = regexp.MustCompile(`(?i)\bstr:\s*"(?:lv\.?\s*)?(\d+)"`)
	reBadgeNumLevel = regexp.MustCompile(`(?i)\b(?:number|level):\s*"?(\d+)"?`)
)

func extractRoles(u *gotiktoklive.User, ident *gotiktoklive.UserIdentity) ([]string, int) {
	var roles []string
	var fanClubLevel int
	seen := make(map[string]struct{})
	add := func(r string) {
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			roles = append(roles, r)
		}
	}

	if ident != nil {
		if ident.IsAnchor {
			add("broadcaster")
		}
		if ident.IsModerator {
			add("mod")
		}
		if ident.IsSubscriber {
			add("subscriber")
		}
		if ident.IsGiftGiver {
			add("vip")
		}
	}

	if u != nil && u.Badge != nil {
		for _, b := range u.Badge.Badges {
			if b == nil {
				continue
			}
			lower := strings.ToLower(b.Name + " " + b.Type)
			if strings.Contains(lower, "moderator") || strings.Contains(lower, "admin") {
				add("mod")
			}
			if isSubscriberOrSuperFanBadge(lower) {
				add("subscriber")
			}
			if isFanClubBadge(lower) {
				add("fan_club")
				if lvl := parseBadgeLevel(b.Name); lvl > fanClubLevel {
					fanClubLevel = lvl
				} else if fanClubLevel == 0 {
					fanClubLevel = 1
				}
			}
			if strings.Contains(lower, "top_gifter") || strings.Contains(lower, "gifter") {
				add("vip")
			}
		}
	}

	return roles, fanClubLevel
}

func isSubscriberOrSuperFanBadge(lower string) bool {
	return strings.Contains(lower, "subscriber") ||
		strings.Contains(lower, "subs_badge") ||
		strings.Contains(lower, "superfan") ||
		strings.Contains(lower, "super_fan")
}

func isFanClubBadge(lower string) bool {
	return strings.Contains(lower, "fans_badge") ||
		strings.Contains(lower, "fansclub") ||
		strings.Contains(lower, "fan_club") ||
		strings.Contains(lower, "fans_club") ||
		strings.Contains(lower, "fans_level") ||
		strings.Contains(lower, "team_member")
}

func parseBadgeLevel(raw string) int {
	if m := reBadgeStrLevel.FindStringSubmatch(raw); len(m) == 2 {
		if lvl, err := strconv.Atoi(m[1]); err == nil && lvl > 0 {
			return lvl
		}
	}
	if m := reBadgeNumLevel.FindStringSubmatch(raw); len(m) == 2 {
		if lvl, err := strconv.Atoi(m[1]); err == nil && lvl > 0 {
			return lvl
		}
	}
	return 0
}

func parseTikTokTimestamp(ts int64) time.Time {
	// TikTok WebCast timestamps may be in milliseconds or seconds
	if ts > 1_000_000_000_000 {
		return time.UnixMilli(ts)
	}
	return time.Unix(ts, 0)
}
