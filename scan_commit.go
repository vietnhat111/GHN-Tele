package main

import (
	"encoding/json"
	"errors"
	"time"
)

// Scan state and its Telegram messages share one atomic write. No worker can see
// the messages until the new scan state has also been saved.
func commitScan(cfg Config, seen *SeenStore, messages []outMsg, now time.Time) error {
	data, err := json.Marshal(seen)
	if err != nil {
		return err
	}
	peers := cfg.targets()
	parts := make([]pendingTelegram, 0, len(messages)*len(peers))
	for _, msg := range messages {
		for _, part := range splitMsg(msg.Text) {
			for _, target := range peers {
				parts = append(parts, pendingTelegram{Peer: peerFor(target), Text: part, Quiet: msg.Quiet, Created: now, Next: now})
			}
		}
	}
	admitted := false
	failed := withOutbox(cfg, func(state *deliveryState) {
		if len(state.Pending)+len(parts) > outboxLimit {
			return
		}
		admitted = true
		state.Scan = data
		for _, part := range parts {
			state.LastID++
			part.ID = state.LastID
			state.Pending = append(state.Pending, part)
		}
	}, nil, true, false)
	if failed {
		return errors.New("chưa ghi được trạng thái quét và tin Telegram")
	}
	if !admitted {
		return errors.New("hàng đợi Telegram đầy, lần quét chưa được chốt")
	}
	if len(parts) > 0 {
		wakeDelivery()
	}
	return nil
}

func loadCommittedSeen(cfg Config) *SeenStore {
	var saved json.RawMessage
	withOutbox(cfg, nil, func(state *deliveryState) { saved = state.Scan }, false, false)
	if len(saved) > 0 {
		return decodeSeen(saved)
	}
	return loadSeen()
}
