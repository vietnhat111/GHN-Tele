package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	outboxFile      = "telegram-outbox.json"
	outboxLimit     = 2000
	outboxMaxAge    = 30 * time.Minute
	outboxTimeout   = 8 * time.Second
	outboxDiskLimit = 32 << 20
)

type telegramPeer struct {
	Chat   string
	Thread int
}

func peerFor(t chatTarget) telegramPeer { return telegramPeer{Chat: t.chat, Thread: t.thread} }

type pendingTelegram struct {
	ID      uint64
	Peer    telegramPeer
	Text    string
	Quiet   bool
	Created time.Time
	Next    time.Time
	Tries   int
}

type telegramHealth struct {
	Peer     telegramPeer
	OKAt     time.Time
	ErrorAt  time.Time
	Error    string
	Overflow int
	Rejected int
}

type deliveryState struct {
	Binding  string
	Location string `json:",omitempty"`
	LastID   uint64
	Pending  []pendingTelegram
	Health   []telegramHealth
	Scan     json.RawMessage `json:",omitempty"`
}

var outbox struct {
	sync.Mutex
	path       string
	value      deliveryState
	saveFailed bool
}

func deliveryBinding(cfg Config) string {
	hash := sha256.Sum256([]byte(cfg.TelegramToken + "\x00" + cfg.LocationID))
	return hex.EncodeToString(hash[:])
}

func configuredPeer(peers []chatTarget, peer telegramPeer) bool {
	return slices.Contains(peers, chatTarget{chat: peer.Chat, thread: peer.Thread})
}

// outboxAccess giữ khóa trong lúc đọc/ghi đĩa, còn gửi mạng chạy bên ngoài.
func outboxAccess(cfg Config, change func(*deliveryState)) (deliveryState, bool) {
	return checkedOutbox(cfg, change, false, false)
}

func checkedOutbox(cfg Config, change func(*deliveryState), requireDisk, sameBinding bool) (deliveryState, bool) {
	outbox.Lock()
	defer outbox.Unlock()
	path := filepath.Join(exeDir, outboxFile)
	if outbox.path != path {
		outbox.path = path
		outbox.value = deliveryState{}
		outbox.saveFailed = false
		b, err := readUpdateFile(path, outboxDiskLimit)
		if err == nil {
			if json.Unmarshal(b, &outbox.value) != nil {
				outbox.value = deliveryState{}
				logf("[telegram] Tệp hàng đợi không đọc được")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			logf("[telegram] Không đọc được hàng đợi")
		}
	}
	v := &outbox.value
	binding := deliveryBinding(cfg)
	changed := false
	if binding != v.Binding {
		if sameBinding {
			return deliveryState{}, outbox.saveFailed
		}
		var scan json.RawMessage
		if v.Location == cfg.LocationID {
			scan = v.Scan
		}
		*v = deliveryState{Binding: binding, Location: cfg.LocationID, Scan: scan, LastID: v.LastID}
		changed = true
	}
	peers := cfg.targets()
	nPending, nHealth := len(v.Pending), len(v.Health)
	v.Pending = slices.DeleteFunc(v.Pending, func(m pendingTelegram) bool { return !configuredPeer(peers, m.Peer) })
	v.Health = slices.DeleteFunc(v.Health, func(h telegramHealth) bool { return !configuredPeer(peers, h.Peer) })
	var before deliveryState
	if change != nil {
		if requireDisk {
			before = *v
			before.Pending = slices.Clone(v.Pending)
		}
		change(v)
		changed = true
	}
	if changed || nPending != len(v.Pending) || nHealth != len(v.Health) || outbox.saveFailed {
		b, err := json.Marshal(v)
		if err == nil && len(b) > outboxDiskLimit {
			err = errors.New("trạng thái và hàng đợi vượt sức chứa tệp")
		}
		if err == nil {
			err = writeAtomicFile(path, b)
		}
		if err != nil && !outbox.saveFailed {
			logf("[telegram] Chưa ghi được hàng đợi xuống đĩa: %v", err)
		}
		outbox.saveFailed = err != nil
		if err != nil && requireDisk {
			*v = before
		}
	}
	if change != nil {
		return deliveryState{}, outbox.saveFailed
	}
	return deliveryState{Pending: slices.Clone(v.Pending), Health: slices.Clone(v.Health), Scan: v.Scan}, outbox.saveFailed
}

func (s *deliveryState) health(peer telegramPeer) *telegramHealth {
	for i := range s.Health {
		if s.Health[i].Peer == peer {
			return &s.Health[i]
		}
	}
	s.Health = append(s.Health, telegramHealth{Peer: peer})
	return &s.Health[len(s.Health)-1]
}

func (s *deliveryState) record(peer telegramPeer, err error, now time.Time) {
	h := s.health(peer)
	if err == nil {
		h.OKAt = now
		h.Error = ""
	} else {
		h.ErrorAt = now
		h.Error = err.Error()
	}
}

func (s *deliveryState) append(peer telegramPeer, parts []string, quiet bool, now, next time.Time, tries int) {
	for _, part := range parts {
		if len(s.Pending) >= outboxLimit {
			s.health(peer).Overflow++
			continue
		}
		s.LastID++
		s.Pending = append(s.Pending, pendingTelegram{
			ID: s.LastID, Peer: peer, Text: part, Quiet: quiet, Created: now, Next: next, Tries: tries,
		})
	}
}

func retryDelay(err error, tries int) time.Duration {
	var limit tgRateLimit
	if errors.As(err, &limit) {
		return max(2*time.Second, limit.delay)
	}
	backoff := []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
	return backoff[min(max(tries-1, 0), len(backoff)-1)]
}

func sendOutboxPart(cfg Config, peer telegramPeer, part string, quiet bool) error {
	return tgCallOnce(cfg.TelegramToken, "sendMessage", tgSendParams(peer.Chat, peer.Thread, part, quiet), outboxTimeout)
}

func deliver(cfg Config, m outMsg) {
	if cfg.TelegramToken == "" {
		return
	}
	parts := splitMsg(m.Text)
	if len(parts) == 0 {
		return
	}
	for _, target := range cfg.targets() {
		peer := peerFor(target)
		state, _ := outboxAccess(cfg, nil)
		if slices.ContainsFunc(state.Pending, func(p pendingTelegram) bool { return p.Peer == peer }) {
			now := time.Now()
			outboxAccess(cfg, func(s *deliveryState) { s.append(peer, parts, m.Quiet, now, now, 0) })
			continue
		}
		for i, part := range parts {
			if i > 0 {
				time.Sleep(tgGap)
			}
			err := sendOutboxPart(cfg, peer, part, m.Quiet)
			now := time.Now()
			outboxAccess(cfg, func(s *deliveryState) {
				s.record(peer, err, now)
				if err != nil && !isPermanent(err) {
					s.append(peer, parts[i:i+1], m.Quiet, now, now.Add(retryDelay(err, 1)), 1)
					s.append(peer, parts[i+1:], m.Quiet, now, now, 0)
				}
			})
			if err != nil {
				break
			}
		}
	}
}

func spoolOverflow(cfg Config, m outMsg, now time.Time) {
	if cfg.TelegramToken == "" {
		return
	}
	parts := splitMsg(m.Text)
	outboxAccess(cfg, func(s *deliveryState) {
		for _, t := range cfg.targets() {
			s.append(peerFor(t), parts, m.Quiet, now, now, 0)
		}
	})
}

func retryOutbox(cfg Config, now time.Time) bool {
	if cfg.TelegramToken == "" {
		return false
	}
	state, writeFailed := outboxAccess(cfg, nil)
	if writeFailed {
		return false
	}
	// Mỗi nơi nhận giữ thứ tự riêng; một nhóm lỗi không chặn chat riêng.
	seen := make(map[telegramPeer]bool)
	for _, p := range state.Pending {
		if seen[p.Peer] {
			continue
		}
		seen[p.Peer] = true
		if now.Before(p.Next) {
			continue
		}
		if !updateInProgress() {
			retryPending(cfg, state, p, now)
			return true
		}
		return false
	}
	return false
}

func retryPending(cfg Config, state deliveryState, first pendingTelegram, now time.Time) {
	ids := map[uint64]bool{first.ID: true}
	text, quiet := first.Text, first.Quiet
	if now.Sub(first.Created) >= outboxMaxAge {
		for _, p := range state.Pending {
			if p.Peer == first.Peer && now.Sub(p.Created) >= outboxMaxAge {
				ids[p.ID] = true
			}
		}
		text = delayedSummary(state.Pending, ids, now)
		quiet = true
	}
	err := sendOutboxPart(cfg, first.Peer, text, quiet)
	at := time.Now()
	checkedOutbox(cfg, func(s *deliveryState) {
		if !slices.ContainsFunc(s.Pending, func(p pendingTelegram) bool { return p.ID == first.ID && p.Peer == first.Peer }) {
			return
		}
		s.record(first.Peer, err, at)
		if err == nil || isPermanent(err) {
			if err != nil {
				s.health(first.Peer).Rejected += len(ids)
			}
			s.Pending = slices.DeleteFunc(s.Pending, func(p pendingTelegram) bool { return ids[p.ID] })
			return
		}
		for i := range s.Pending {
			if p := &s.Pending[i]; p.ID == first.ID {
				p.Tries++
				p.Next = at.Add(retryDelay(err, p.Tries))
				break
			}
		}
	}, false, true)
}

func delayedSummary(pending []pendingTelegram, ids map[uint64]bool, now time.Time) string {
	var arrived, upcoming, changed int
	for _, p := range pending {
		if !ids[p.ID] {
			continue
		}
		switch {
		case strings.HasPrefix(p.Text, "🟠"):
			arrived++
		case strings.HasPrefix(p.Text, "🔵"):
			upcoming++
		case strings.HasPrefix(p.Text, "🔄"):
			changed++
		}
	}
	msg := fmt.Sprintf("📌 <b>Tóm tắt tin gửi chậm</b>\n%d phần tin cũ trong lúc gián đoạn.", len(ids))
	if arrived+upcoming+changed > 0 {
		msg += fmt.Sprintf("\nTin xe vào kho: %d · Xe sắp đến: %d · Đổi kiện: %d.", arrived, upcoming, changed)
	}
	s := getSnapshot()
	if !s.At.IsZero() && now.Sub(s.At) <= 15*time.Minute {
		msg += fmt.Sprintf("\nLần quét %s: %d xe trong kho · %d xe sắp đến.", s.At.Format("15:04"), len(s.Arr), len(s.Up))
	}
	return msg + "\nAnh dùng /xe để xem chi tiết."
}
