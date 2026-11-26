package main

import (
	"testing"
	"time"
)

func TestAutomaticUpdateWindow(t *testing.T) {
	for _, tc := range []struct {
		from, to, now string
		want          bool
	}{
		{"05:00", "21:00", "14:30:00", false},
		{"05:00", "21:00", "21:00:00", true},
		{"05:00", "21:00", "04:50:00", true},
		{"05:00", "21:00", "04:50:01", false},
		{"20:00", "04:00", "01:00:00", false},
		{"20:00", "04:00", "04:00:00", true},
		{"00:00", "00:00", "18:45:00", false},
		{"00:00", "23:59", "23:59:00", false},
		{"bad", "21:00", "22:00:00", false},
	} {
		now, err := time.Parse("15:04:05", tc.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := automaticUpdateWindow(now, Config{WatchFrom: tc.from, WatchTo: tc.to}); got != tc.want {
			t.Errorf("khung %s–%s, lúc %s: got %v, want %v", tc.from, tc.to, tc.now, got, tc.want)
		}
	}
}

func TestAutomaticUpdateRetryPolicy(t *testing.T) {
	oldDir := exeDir
	exeDir = t.TempDir()
	t.Cleanup(func() { exeDir = oldDir })
	stage := t.TempDir()
	now := time.Unix(100000, 0)
	if !autoVersionAllowed("2.16.1", now) {
		t.Fatal("first release should be eligible")
	}
	retry := now.Add(time.Hour)
	if err := recordAutoAttempt(stage, "2.16.2", retry.Unix()); err != nil {
		t.Fatal(err)
	}
	if autoVersionAllowed("2.16.2", now) || autoVersionAllowed("2.16.1", retry) {
		t.Fatal("same version during cooldown and older versions should be blocked")
	}
	if !autoVersionAllowed("2.16.2", retry) || !autoVersionAllowed("2.16.3", now) {
		t.Fatal("retry after cooldown and newer release should remain eligible")
	}
	if err := recordAutoAttempt(stage, "2.16.2", 0); err != nil {
		t.Fatal(err)
	}
	if autoVersionAllowed("2.16.2", retry) {
		t.Fatal("completed attempt should remain blocked")
	}
}

func TestGitHubUpdateURL(t *testing.T) {
	const want = "https://github.com/owner-1/ghn-tele/releases/latest/download/update.json"
	if got, err := githubUpdateURL("owner-1/ghn-tele"); err != nil || got != want {
		t.Fatalf("got URL %q, error %v, want %q", got, err, want)
	}
	for _, name := range []string{"org", "org/..", "org/repo/path", "org/repo?token=key", "https://github.com/org/repo"} {
		if _, err := githubUpdateURL(name); err == nil {
			t.Errorf("accepted invalid repo %q", name)
		}
	}
}
