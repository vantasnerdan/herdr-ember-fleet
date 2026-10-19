package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
)

// UpdateSettings is how the fleet keeps itself current.
type UpdateSettings struct {
	Enabled bool `json:"enabled"` // run once a day
	Hour    int  `json:"hour"`    // at this hour, hub local time (0-23)
	Herdr   bool `json:"herdr"`   // also run `herdr update` on every machine
	Handoff bool `json:"handoff"` // move running herdr servers to the new version (herdr's live handoff)
}

// UpdateResult is what the last update run did on one dock.
type UpdateResult struct {
	At        int64  `json:"at"`
	EmberFrom string `json:"emberFrom,omitempty"`
	EmberTo   string `json:"emberTo,omitempty"`
	HerdrFrom string `json:"herdrFrom,omitempty"`
	HerdrTo   string `json:"herdrTo,omitempty"`
	Servers   string `json:"servers,omitempty"` // running herdr servers afterwards, "name=version ..."
	Error     string `json:"error,omitempty"`
	Output    string `json:"output,omitempty"` // the tail of herdr update's output
}

// By default herdr is updated without live handoff: running servers (and the
// agents' monitors, waits and pane-to-pane messages) are left alone, and pick up
// the new version when they next restart. Handoff can drop those in flight.
func defaultUpdateSettings() UpdateSettings {
	return UpdateSettings{Enabled: true, Hour: 4, Herdr: true, Handoff: false}
}

type updater struct {
	mu      sync.Mutex
	running bool
}

// updateSettings returns the saved settings or the defaults.
func (h *Hub) updateSettings() UpdateSettings {
	var u UpdateSettings
	h.store.View(func(s *State) {
		if s.Updates != nil {
			u = *s.Updates
		} else {
			u = defaultUpdateSettings()
		}
	})
	return u
}

// SetUpdateSettings saves new settings.
func (h *Hub) SetUpdateSettings(u UpdateSettings) error {
	if u.Hour < 0 || u.Hour > 23 {
		return errors.New("hour must be 0-23")
	}
	return h.store.Update(func(s *State) error { s.Updates = &u; return nil })
}

// updateLoop runs the daily update at the configured hour.
func (h *Hub) updateLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		u := h.updateSettings()
		if !u.Enabled || time.Now().Hour() != u.Hour {
			continue
		}
		var last int64
		h.store.View(func(s *State) { last = s.UpdateLastRun })
		if last > 0 && time.Since(time.UnixMilli(last)) < 20*time.Hour {
			continue
		}
		h.RunUpdates(ctx, "daily")
	}
}

// RunUpdates updates every online dock: first ember-fleet (pushed from this
// hub), then herdr. Three machines at a time; returns at once if a run is
// already going.
func (h *Hub) RunUpdates(ctx context.Context, why string, only ...string) bool {
	h.upd.mu.Lock()
	if h.upd.running {
		h.upd.mu.Unlock()
		return false
	}
	h.upd.running = true
	h.upd.mu.Unlock()
	h.store.Update(func(s *State) error { s.UpdateLastRun = time.Now().UnixMilli(); return nil })
	h.updatesChanged()

	go func() {
		defer func() {
			h.upd.mu.Lock()
			h.upd.running = false
			h.upd.mu.Unlock()
			h.updatesChanged()
		}()
		u := h.updateSettings()
		var ids []string
		h.mu.Lock()
		for id, d := range h.docks {
			if d.online && h.isBound(id) && (len(only) == 0 || only[0] == id) {
				ids = append(ids, id)
			}
		}
		h.mu.Unlock()
		log.Printf("updates (%s): %d machines", why, len(ids))
		sem := make(chan struct{}, 3)
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			sem <- struct{}{}
			go func(id string) {
				defer wg.Done()
				defer func() { <-sem }()
				res := h.updateDock(ctx, id, u)
				h.store.Update(func(s *State) error {
					if s.UpdateLog == nil {
						s.UpdateLog = map[string]*UpdateResult{}
					}
					s.UpdateLog[id] = &res
					return nil
				})
				h.updatesChanged()
			}(id)
		}
		wg.Wait()
	}()
	return true
}

func (h *Hub) dockVersion(id string) (version, goos, arch string, online bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.docks[id]
	if d == nil {
		return "", "", "", false
	}
	version, online = d.version, d.online
	if d.info != nil {
		goos, arch = d.info.OS, d.info.Arch
		if version == "" {
			version = d.info.Version
		}
	}
	return
}

func (h *Hub) updateDock(ctx context.Context, id string, u UpdateSettings) UpdateResult {
	res := UpdateResult{At: time.Now().UnixMilli()}
	ver, goos, arch, _ := h.dockVersion(id)
	target := Version
	if newer(target, ver) {
		res.EmberFrom = ver
		if err := h.pushBinary(ctx, id, goos, arch, target); err != nil {
			res.Error = "ember-fleet: " + err.Error()
			return res
		}
		// wait for the dock to come back on the new version
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			if v, _, _, online := h.dockVersion(id); online && v == target {
				res.EmberTo = target
				break
			}
		}
		if res.EmberTo == "" {
			res.Error = "ember-fleet: the dock did not come back on " + target + " within 90 s"
			return res
		}
	}
	if u.Herdr {
		r, err := h.updateHerdr(ctx, id, u.Handoff)
		res.HerdrFrom, res.HerdrTo, res.Servers, res.Output = r.Before, r.After, r.Servers, r.Output
		if err != nil {
			res.Error = "herdr: " + err.Error()
		}
	}
	return res
}

// newer reports whether version a is newer than b ("0.2.10" > "0.2.9").
// Anything that isn't a plain x.y.z version is never pushed.
func newer(a, b string) bool {
	pa, okA := semver(a)
	pb, okB := semver(b)
	if !okA {
		return false
	}
	if !okB {
		return true // an unknown or dev dock gets the release
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func semver(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// binaryFor finds this hub's build for a platform: dist/ember-fleet-OS-ARCH
// (with dist/VERSION saying which version those are), or this very binary
// when the platform matches.
func binaryFor(goos, arch, version string) (string, error) {
	if goos == "" || arch == "" {
		return "", errors.New("the dock has not said what platform it runs on yet")
	}
	dist := filepath.Join(Dir(), "dist")
	if b, err := os.ReadFile(filepath.Join(dist, "VERSION")); err == nil && strings.TrimSpace(string(b)) == version {
		p := filepath.Join(dist, "ember-fleet-"+goos+"-"+arch)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if goos == runtime.GOOS && arch == runtime.GOARCH {
		return os.Executable()
	}
	return "", fmt.Errorf("no %s/%s build of %s on the hub (put it in %s with a VERSION file)", goos, arch, version, dist)
}

func (h *Hub) pushBinary(ctx context.Context, id, goos, arch, version string) error {
	path, err := binaryFor(goos, arch, version)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	hdr := e2e.Header{Kind: e2e.KindUpdate, Operator: "hub:updates", Version: version, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	t, br, rep, cut, err := h.openPaired(cctx, id, hdr)
	if err != nil {
		return err
	}
	defer cut()
	defer t.Close()
	if !rep.OK {
		return errors.New(rep.Error)
	}
	if _, err := io.Copy(t, strings.NewReader(string(data))); err != nil {
		return err
	}
	var done e2e.Reply
	if err := e2e.ReadJSON(br, &done); err != nil {
		return errors.New("the dock did not confirm the update")
	}
	if !done.OK {
		return errors.New(done.Error)
	}
	log.Printf("updated %s to %s", keys.Short(id), version)
	return nil
}

type herdrResult struct {
	Before  string `json:"before"`
	After   string `json:"after"`
	Servers string `json:"servers"`
	Output  string `json:"output"`
	Error   string `json:"error"`
}

func (h *Hub) updateHerdr(ctx context.Context, id string, handoff bool) (herdrResult, error) {
	var r herdrResult
	cctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	t, br, rep, cut, err := h.openPaired(cctx, id, e2e.Header{Kind: e2e.KindHerdrUp, Operator: "hub:updates", Handoff: handoff})
	if err != nil {
		return r, err
	}
	defer cut()
	defer t.Close()
	_ = br
	if len(rep.Result) > 0 {
		json.Unmarshal(rep.Result, &r)
	}
	if !rep.OK {
		if rep.Error != "" {
			return r, errors.New(rep.Error)
		}
		return r, errors.New("herdr update failed")
	}
	return r, nil
}

func (h *Hub) updatesChanged() {
	h.live.Lock()
	defer h.live.Unlock()
	for w := range h.watchers {
		w.markUpdates()
	}
}

// UpdateStatus is what the settings page shows.
type UpdateStatus struct {
	Settings UpdateSettings     `json:"settings"`
	Target   string             `json:"target"` // the ember-fleet version this hub hands out
	Running  bool               `json:"running"`
	LastRun  int64              `json:"lastRun"`
	Docks    []UpdateDockStatus `json:"docks"`
}

// UpdateDockStatus is one machine user's versions and last update result.
type UpdateDockStatus struct {
	ID          string        `json:"id"`
	Machine     string        `json:"machine"`
	User        string        `json:"user"`
	Online      bool          `json:"online"`
	Ember       string        `json:"ember"`
	Herdr       string        `json:"herdr"`       // installed herdr
	HerdrServer string        `json:"herdrServer"` // the default session's running server
	Accepts     bool          `json:"accepts"`
	Result      *UpdateResult `json:"result,omitempty"`
}

// Updates returns the current update status.
func (h *Hub) Updates() UpdateStatus {
	st := UpdateStatus{Settings: h.updateSettings(), Target: Version}
	h.upd.mu.Lock()
	st.Running = h.upd.running
	h.upd.mu.Unlock()
	logs := map[string]UpdateResult{}
	h.store.View(func(s *State) {
		st.LastRun = s.UpdateLastRun
		for id, r := range s.UpdateLog {
			logs[id] = *r
		}
	})
	for _, m := range h.Machines() {
		for _, d := range m.Docks {
			ds := UpdateDockStatus{ID: d.ID, Machine: m.Host, User: d.User, Online: d.Online, Ember: d.Version, Herdr: d.Herdr, Accepts: true}
			for _, raw := range d.Sessions {
				var s struct {
					Name     string `json:"name"`
					Snapshot struct {
						Version string `json:"version"`
					} `json:"snapshot"`
				}
				if json.Unmarshal(raw, &s) == nil && s.Name == "default" {
					ds.HerdrServer = s.Snapshot.Version
				}
			}
			if r, ok := logs[d.ID]; ok {
				ds.Result = &r
			}
			st.Docks = append(st.Docks, ds)
		}
	}
	return st
}
