package practice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Team254/cheesy-arena/model"
	"github.com/Team254/cheesy-arena/network"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type lineupRequest struct {
	Revision       int            `json:"revision"`
	Stations       []int          `json:"stations"`
	CommonPassword string         `json:"commonPassword"`
	Overrides      map[int]string `json:"overrides"`
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := model.OpenDatabase(filepath.Join(t.TempDir(), "practice.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	s, err := NewServer(db)
	require.NoError(t, err)
	s.readAP = func(context.Context, model.PracticeNetwork) (network.AccessPointSnapshot, error) {
		return network.AccessPointSnapshot{State: "ACTIVE"}, nil
	}
	return s
}

func call(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func currentConfig(t *testing.T, s *Server) model.PracticeConfig {
	t.Helper()
	w := call(t, s, "GET", "/api/config", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var config model.PracticeConfig
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &config))
	return config
}

func waitForApply(t *testing.T, s *Server) applyStatus {
	t.Helper()
	var status applyStatus
	require.Eventually(t, func() bool {
		w := call(t, s, "GET", "/api/status", nil)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
		return !status.Applying
	}, time.Second*3, time.Millisecond)
	return status
}

func TestApplyPersistsTeamOverridesAndClearsStations(t *testing.T) {
	s := newTestServer(t)
	type station struct {
		Ssid   string `json:"ssid"`
		WpaKey string `json:"wpaKey"`
	}
	type apConfiguration struct {
		Channel  int                `json:"channel"`
		Stations map[string]station `json:"stationConfigurations"`
	}
	apRequests := make(chan apConfiguration, 4)
	ap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configuration" || r.Header.Get("Authorization") != "Bearer ap-token" {
			http.Error(w, "wrong AP request", http.StatusBadRequest)
			return
		}
		var config apConfiguration
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		apRequests <- config
	}))
	t.Cleanup(ap.Close)
	wiredRequests := make(chan [6]*model.Team, 4)
	s.configureSwitch = func(_ model.PracticeNetwork, teams [6]*model.Team) error { wiredRequests <- teams; return nil }
	settings := s.config.Network
	settings.ApAddress = strings.TrimPrefix(ap.URL, "http://")
	settings.ApPassword = "ap-token"
	w := call(t, s, "POST", "/api/settings", map[string]any{"revision": 0, "network": settings})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, apRequests)
	require.Empty(t, wiredRequests)

	lineup := lineupRequest{Revision: 1, Stations: []int{254, 1678, 0, 971, 0, 0}, CommonPassword: "practice2026", Overrides: map[int]string{254: "visitor2026"}}
	w = call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	status := waitForApply(t, s)
	require.Equal(t, "active", status.AP.State)
	require.Equal(t, "applied", status.Switch.State)
	configured := <-apRequests
	require.Equal(t, 5, configured.Channel)
	require.Equal(t, map[string]station{"red1": {"254", "visitor2026"}, "red2": {"1678", "practice2026"}, "blue1": {"971", "practice2026"}}, configured.Stations)
	wired := <-wiredRequests
	require.Equal(t, 254, wired[0].Id)
	require.Nil(t, wired[2])
	require.Nil(t, wired[4])
	roster, err := s.database.GetAllTeams()
	require.NoError(t, err)
	require.Empty(t, roster, "assigning a team must not require an event roster")

	// Removing the team keeps its override. An empty lineup clears all six devices' station entries.
	lineup.Revision = status.Revision
	lineup.Stations = []int{0, 0, 0, 0, 0, 0}
	w = call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusAccepted, w.Code)
	status = waitForApply(t, s)
	require.Empty(t, (<-apRequests).Stations)
	require.Equal(t, [6]*model.Team{}, <-wiredRequests)
	require.Equal(t, "visitor2026", currentConfig(t, s).Overrides[254])

	// Move the team and change the common password; only non-overridden teams inherit it.
	lineup.Revision = status.Revision
	lineup.Stations = []int{0, 1678, 0, 0, 254, 0}
	lineup.CommonPassword = "new-common"
	w = call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusAccepted, w.Code)
	status = waitForApply(t, s)
	require.Equal(t, map[string]station{"red2": {"1678", "new-common"}, "blue2": {"254", "visitor2026"}}, (<-apRequests).Stations)
	<-wiredRequests

	// Clearing the override restores the current common password.
	lineup.Revision = status.Revision
	lineup.Overrides[254] = ""
	w = call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusAccepted, w.Code)
	waitForApply(t, s)
	require.Equal(t, "new-common", (<-apRequests).Stations["blue2"].WpaKey)
	<-wiredRequests
	require.Empty(t, currentConfig(t, s).Overrides)

	// A new server loads the saved state without reconfiguring either device.
	restarted, err := NewServer(s.database)
	require.NoError(t, err)
	require.Equal(t, currentConfig(t, s), currentConfig(t, restarted))
	restarted.readAP = s.readAP
	require.Equal(t, "idle", waitForApply(t, restarted).Switch.State)
	require.Empty(t, apRequests)
	require.Empty(t, wiredRequests)
}

func TestApplyValidationDoesNotSaveOrConfigure(t *testing.T) {
	tests := []struct {
		name      string
		stations  []int
		common    string
		overrides map[int]string
	}{
		{"duplicate", []int{254, 0, 0, 254, 0, 0}, "password1", nil},
		{"missing station", []int{254}, "password1", nil},
		{"extra station", []int{254, 0, 0, 0, 0, 0, 1}, "password1", nil},
		{"negative", []int{-1, 0, 0, 0, 0, 0}, "password1", nil},
		{"invalid IP octet", []int{25600, 0, 0, 0, 0, 0}, "password1", nil},
		{"missing password", []int{254, 0, 0, 0, 0, 0}, "", nil},
		{"short password", []int{254, 0, 0, 0, 0, 0}, "short", nil},
		{"invalid override", []int{254, 0, 0, 0, 0, 0}, "password1", map[int]string{254: "short"}},
		{"non-ASCII", []int{254, 0, 0, 0, 0, 0}, "password\u00e9", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := newTestServer(t)
			var calls atomic.Int32
			s.configureAP = func(model.PracticeNetwork, [6]*model.Team) error { calls.Add(1); return nil }
			s.configureSwitch = s.configureAP
			before := currentConfig(t, s)
			w := call(t, s, "POST", "/api/apply", lineupRequest{Stations: test.stations, CommonPassword: test.common, Overrides: test.overrides})
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Equal(t, before, currentConfig(t, s))
			require.Zero(t, calls.Load())
		})
	}
}

func TestApplySerializesDevicesAndAllowsExplicitRetry(t *testing.T) {
	s := newTestServer(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var apCalls, switchCalls atomic.Int32
	s.configureAP = func(model.PracticeNetwork, [6]*model.Team) error { apCalls.Add(1); return nil }
	s.configureSwitch = func(model.PracticeNetwork, [6]*model.Team) error {
		if switchCalls.Add(1) == 1 {
			started <- struct{}{}
			<-release
			return errors.New("switch unavailable")
		}
		return nil
	}
	lineup := lineupRequest{Stations: []int{254, 0, 0, 0, 0, 0}, CommonPassword: "password1"}
	w := call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusAccepted, w.Code)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("switch was not called")
	}
	lineup.Revision = 1
	w = call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusConflict, w.Code)
	w = call(t, s, "POST", "/api/settings", map[string]any{"revision": 1, "network": s.config.Network})
	require.Equal(t, http.StatusConflict, w.Code)
	close(release)
	status := waitForApply(t, s)
	require.Equal(t, "active", status.AP.State)
	require.Equal(t, "failed", status.Switch.State)
	require.Equal(t, "switch unavailable", status.Switch.Detail)
	require.EqualValues(t, 1, apCalls.Load())
	require.EqualValues(t, 1, switchCalls.Load())

	w = call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusAccepted, w.Code)
	status = waitForApply(t, s)
	require.Equal(t, "applied", status.Switch.State)
	require.EqualValues(t, 2, apCalls.Load())
	require.EqualValues(t, 2, switchCalls.Load())
	w = call(t, s, "POST", "/api/apply", lineup)
	require.Equal(t, http.StatusConflict, w.Code, "stale browser state cannot replace newer assignments")
}

func TestAPFailureStillReportsSwitchResult(t *testing.T) {
	s := newTestServer(t)
	s.configureAP = func(model.PracticeNetwork, [6]*model.Team) error { return errors.New("AP unavailable") }
	s.configureSwitch = func(model.PracticeNetwork, [6]*model.Team) error { return nil }
	w := call(t, s, "POST", "/api/apply", lineupRequest{Stations: []int{0, 0, 0, 0, 0, 0}})
	require.Equal(t, http.StatusAccepted, w.Code)
	status := waitForApply(t, s)
	require.Equal(t, "failed", status.AP.State)
	require.Equal(t, "applied", status.Switch.State)
}

func TestDisabledNetworkingAndFailedSaveNeverContactHardware(t *testing.T) {
	for _, mode := range []string{"disabled", "database closed"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestServer(t)
			var calls atomic.Int32
			s.configureAP = func(model.PracticeNetwork, [6]*model.Team) error { calls.Add(1); return nil }
			s.configureSwitch = s.configureAP
			code := http.StatusBadRequest
			if mode == "disabled" {
				s.config.Network.NetworkSecurityEnabled = false
			} else {
				require.NoError(t, s.database.Close())
				code = http.StatusInternalServerError
			}
			w := call(t, s, "POST", "/api/apply", lineupRequest{Stations: []int{254, 0, 0, 0, 0, 0}, CommonPassword: "password1"})
			require.Equal(t, code, w.Code)
			require.Zero(t, calls.Load())
		})
	}
}
