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
	peers      []chatTarget
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
	var snapshot deliveryState
	var read func(*deliveryState)
	if change == nil {
		read = func(v *deliveryState) {
			snapshot = deliveryState{Pending: slices.Clone(v.Pending), Health: slices.Clone(v.Health), Scan: v.Scan}
		}
	}
	failed := withOutbox(cfg, change, read, false, false)
	return snapshot, failed
}

func withOutbox(cfg Config, change, read func(*deliveryState), requireDisk, sameBinding bool) bool {
	outbox.Lock()
	defer outbox.Unlock()
	path := filepath.Join(exeDir, outboxFile)
	if outbox.path != path {
		outbox.path = path
		outbox.value = deliveryState{}
		outbox.saveFailed = false
		outbox.peers = nil
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
			return outbox.saveFailed
		}
		var scan json.RawMessage
		if v.Location == cfg.LocationID {
			scan = v.Scan
		}
		*v = deliveryState{Binding: binding, Location: cfg.LocationID, Scan: scan, LastID: v.LastID}
		changed = true
	}
	peers := cfg.targets()
	if outbox.peers == nil || !slices.Equal(peers, outbox.peers) {
		if sameBinding {
			return outbox.saveFailed
		}
		nPending, nHealth := len(v.Pending), len(v.Health)
		v.Pending = slices.DeleteFunc(v.Pending, func(m pendingTelegram) bool { return !configuredPeer(peers, m.Peer) })
		v.Health = slices.DeleteFunc(v.Health, func(h telegramHealth) bool { return !configuredPeer(peers, h.Peer) })
		outbox.peers = append(make([]chatTarget, 0, len(peers)), peers...)
		changed = changed || nPending != len(v.Pending) || nHealth != len(v.Health)
	}
	var before deliveryState
	if change != nil {
		if requireDisk {
			// commitScan only replaces Scan and appends messages, so the old slice
			// header is sufficient to roll back without copying the backlog.
			before = *v
		}
		change(v)
		changed = true
	}
	if changed || outbox.saveFailed {
		err := writeOutbox(path, *v)
		if err != nil && !outbox.saveFailed {
			logf("[telegram] Chưa ghi được hàng đợi xuống đĩa: %v", err)
		}
		outbox.saveFailed = err != nil
		if err != nil && requireDisk {
			*v = before
		}
	}
	if read != nil {
		read(v)
	}
	return outbox.saveFailed
}

// The checkpoint is already valid JSON from commitScan or the initial disk read.
// Write it directly to avoid re-encoding the entire trip history for every Telegram ack.
func encodeOutbox(state deliveryState) ([][]byte, error) {
	scan := state.Scan
	state.Scan = nil
	header, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if len(scan) == 0 {
		if len(header) > outboxDiskLimit {
			return nil, errors.New("hàng đợi vượt sức chứa tệp")
		}
		return [][]byte{header}, nil
	}
	header = append(header[:len(header)-1], `,"Scan":`...)
	if len(header)+len(scan)+1 > outboxDiskLimit {
		return nil, errors.New("trạng thái và hàng đợi vượt sức chứa tệp")
	}
	return [][]byte{header, scan, []byte("}")}, nil
}

func writeOutbox(path string, state deliveryState) error {
	parts, err := encodeOutbox(state)
	if err != nil {
		return err
	}
	return writeAtomicFile(path, parts...)
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

func stageDelivery(cfg Config, m outMsg, now time.Time) {
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

type outboxAttempt struct {
	first  pendingTelegram
	oldIDs map[uint64]bool
	text   string
	quiet  bool
}

func nextOutboxAttempt(pending []pendingTelegram, now time.Time) outboxAttempt {
	var blocked []telegramPeer
	for _, p := range pending {
		if slices.Contains(blocked, p.Peer) {
			continue
		}
		if now.Before(p.Next) {
			blocked = append(blocked, p.Peer)
			continue
		}
		attempt := outboxAttempt{first: p, text: p.Text, quiet: p.Quiet}
		if now.Sub(p.Created) >= outboxMaxAge {
			attempt.oldIDs = make(map[uint64]bool)
			for _, old := range pending {
				if old.Peer == p.Peer && now.Sub(old.Created) >= outboxMaxAge {
					attempt.oldIDs[old.ID] = true
				}
			}
			attempt.text = delayedSummary(pending, attempt.oldIDs, now)
			attempt.quiet = true
		}
		return attempt
	}
	return outboxAttempt{}
}

func retryOutbox(cfg Config, now time.Time) bool {
	if cfg.TelegramToken == "" {
		return false
	}
	var attempt outboxAttempt
	failed := withOutbox(cfg, nil, func(state *deliveryState) {
		attempt = nextOutboxAttempt(state.Pending, now)
	}, false, false)
	if failed || attempt.first.ID == 0 || updateInProgress() {
		return false
	}
	first := attempt.first
	err := sendOutboxPart(cfg, first.Peer, attempt.text, attempt.quiet)
	at := time.Now()
	withOutbox(cfg, func(s *deliveryState) {
		i := slices.IndexFunc(s.Pending, func(p pendingTelegram) bool { return p.ID == first.ID && p.Peer == first.Peer })
		if i < 0 {
			return
		}
		s.record(first.Peer, err, at)
		if err == nil || isPermanent(err) {
			if err != nil {
				s.health(first.Peer).Rejected += max(1, len(attempt.oldIDs))
			}
			if len(attempt.oldIDs) == 0 {
				s.Pending = slices.Delete(s.Pending, i, i+1)
			} else {
				s.Pending = slices.DeleteFunc(s.Pending, func(p pendingTelegram) bool { return attempt.oldIDs[p.ID] })
			}
			return
		}
		p := &s.Pending[i]
		p.Tries++
		p.Next = at.Add(retryDelay(err, p.Tries))
	}, nil, false, true)
	return true
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
