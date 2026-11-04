package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// cachedWorld is a quota world with one cache dir, a clock the test moves
// and a count of the Claude usage requests.
func cachedWorld(t *testing.T) (*quotaWorld, *int, *time.Time) {
	w := newQuotaWorld(t)
	w.cache = t.TempDir()
	now := quotaNow
	w.now = func() time.Time { return now }
	w.claudeLogin(quotaNow.Add(24 * time.Hour))
	n := 0
	w.claude = func(rw http.ResponseWriter, r *http.Request) { n++; rw.Write([]byte(claudeFixture)) }
	return w, &n, &now
}

func TestQuotaIsCachedForFiveMinutes(t *testing.T) {
	w, n, now := cachedWorld(t)
	for i, want := range []int{1, 1} { // a second call inside 5m makes no request
		if _, err := w.run("quota", "list", "claude"); err != nil {
			t.Fatal(err)
		}
		if *n != want {
			t.Fatalf("call %d: %d requests, want %d", i, *n, want)
		}
	}
	*now = now.Add(4*time.Minute + 59*time.Second)
	w.run("quota", "list", "claude")
	if *n != 1 {
		t.Fatalf("inside 5m: %d requests", *n)
	}
	*now = now.Add(2 * time.Second)
	w.run("quota", "list", "claude")
	if *n != 2 {
		t.Fatalf("after expiry: %d requests, want 2", *n)
	}
	out, err := w.run("quota", "show", "claude") // show shares the cache
	if err != nil || *n != 2 || !strings.Contains(out, "weekly_all") {
		t.Fatalf("show: %v %d %s", err, *n, out)
	}
}

func TestQuotaFreshAndRefreshBypassTheCache(t *testing.T) {
	w, n, _ := cachedWorld(t)
	w.run("quota", "list", "claude")
	w.run("quota", "list", "claude", "--fresh")
	if *n != 2 {
		t.Fatalf("--fresh: %d requests, want 2", *n)
	}
	if _, err := w.run("quota", "refresh", "claude"); err != nil || *n != 3 {
		t.Fatalf("refresh: %v, %d requests, want 3", err, *n)
	}
	w.run("quota", "list", "claude", "--ttl", "0s")
	if *n != 4 {
		t.Fatalf("--ttl 0s: %d requests, want 4", *n)
	}
}

func TestQuotaKeepsLastGoodRowsOn429(t *testing.T) {
	w, n, now := cachedWorld(t)
	w.run("quota", "list", "claude")
	*now = now.Add(10 * time.Minute)
	w.claude = func(rw http.ResponseWriter, r *http.Request) {
		*n++
		http.Error(rw, "slow down", http.StatusTooManyRequests)
	}
	out, err := w.run("quota", "list", "claude")
	if err != nil {
		t.Fatalf("a refused refresh with last good rows is not a failure: %v\n%s", err, out)
	}
	for _, want := range []string{"quota/claude:weekly_all", "quota/claude:error", "429"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	before := *n
	// The failure was not a fresh fetch: once the back-off is over the
	// next call asks again.
	*now = now.Add(61 * time.Second)
	w.run("quota", "list", "claude")
	if *n != before+1 {
		t.Fatalf("the 429 reset the cache time: %d requests, want %d", *n, before+1)
	}
	// And the cache file holds percentages, never the login.
	b, _ := os.ReadFile(w.quotaFile())
	if strings.Contains(string(b), "sk-ant") || strings.Contains(string(b), "oat") {
		t.Fatalf("the cache holds a credential: %s", b)
	}
}

func TestQuotaWithoutGoodRowsStillShowsTheError(t *testing.T) {
	w, _, _ := cachedWorld(t)
	w.claude = func(rw http.ResponseWriter, r *http.Request) { http.Error(rw, "no", http.StatusTooManyRequests) }
	out, err := w.run("quota", "list", "claude")
	if err == nil || !strings.Contains(out, "quota/claude:error") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestQuotaBacksOffAfterARefusal(t *testing.T) {
	w, n, now := cachedWorld(t)
	w.run("quota", "list", "claude")
	*now = now.Add(10 * time.Minute) // expired
	w.claude = func(rw http.ResponseWriter, r *http.Request) {
		*n++
		http.Error(rw, "slow down", http.StatusTooManyRequests)
	}
	w.run("quota", "list", "claude")
	after := *n // the refused request
	*now = now.Add(30 * time.Second)
	out, err := w.run("quota", "list", "claude")
	if err != nil || *n != after || !strings.Contains(out, "quota/claude:weekly_all") || !strings.Contains(out, "429") {
		t.Fatalf("inside the back-off: %v, %d requests (was %d)\n%s", err, *n, after, out)
	}
	for _, bypass := range [][]string{{"quota", "list", "claude", "--fresh"}, {"quota", "refresh", "claude"}} {
		before := *n
		w.run(bypass...)
		if *n != before+1 {
			t.Fatalf("%v: %d requests, want %d", bypass, *n, before+1)
		}
	}
	*now = now.Add(61 * time.Second) // back-off over
	before := *n
	w.run("quota", "list", "claude")
	if *n != before+1 {
		t.Fatalf("after the back-off: %d requests, want %d", *n, before+1)
	}
	// A good answer ends it: the next call is served from the cache.
	w.claude = func(rw http.ResponseWriter, r *http.Request) { *n++; rw.Write([]byte(claudeFixture)) }
	*now = now.Add(61 * time.Second)
	w.run("quota", "list", "claude")
	before = *n
	w.run("quota", "list", "claude")
	if *n != before {
		t.Fatalf("a good answer was not cached: %d requests, want %d", *n, before)
	}
}

func TestQuotaBacksOffWithNoGoodRows(t *testing.T) {
	w, n, now := cachedWorld(t)
	w.claude = func(rw http.ResponseWriter, r *http.Request) { *n++; http.Error(rw, "no", http.StatusTooManyRequests) }
	w.run("quota", "list", "claude")
	*now = now.Add(20 * time.Second)
	out, err := w.run("quota", "list", "claude")
	if *n != 1 || err == nil || !strings.Contains(out, "quota/claude:error") {
		t.Fatalf("%d requests, %v\n%s", *n, err, out)
	}
}
