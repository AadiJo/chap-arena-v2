package practice

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Team254/cheesy-arena/model"
	"github.com/Team254/cheesy-arena/network"
	"github.com/stretchr/testify/require"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func readLiveStatus(t *testing.T, s *Server) liveStatus {
	t.Helper()
	w := call(t, s, "GET", "/api/status", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var status liveStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	return status
}

func TestMonitorReportsAPLinksAndRecoversWithoutApplying(t *testing.T) {
	s := newTestServer(t)
	s.config.Stations = [6]int{254, 1678, 0, 971, 0, 0}
	clock := time.Now()
	s.now = func() time.Time { return clock }
	snapshot := network.AccessPointSnapshot{State: "ACTIVE", Stations: [6]network.TeamWifiStatus{
		{TeamId: 254, RadioLinked: true}, {TeamId: 1678}, {}, {TeamId: 1114, RadioLinked: true},
	}}
	var failure error
	reads := 0
	s.readAP = func(context.Context, model.PracticeNetwork) (network.AccessPointSnapshot, error) {
		reads++
		return snapshot, failure
	}
	var writes atomic.Int32
	s.configureAP = func(model.PracticeNetwork, [6]*model.Team) error { writes.Add(1); return nil }
	s.configureSwitch = s.configureAP
	status := readLiveStatus(t, s)
	require.Equal(t, "active", status.AP.State)
	require.Equal(t, "linked", status.Radios[0].State)
	require.Equal(t, "missing", status.Radios[1].State)
	require.Equal(t, "empty", status.Radios[2].State)
	require.Equal(t, "mismatch", status.Radios[3].State)
	readLiveStatus(t, s)
	require.Equal(t, 1, reads, "multiple viewers share the cached poll")

	for _, apState := range []string{"CONFIGURING", "BOOTING"} {
		clock = clock.Add(apPollInterval)
		snapshot.State = apState
		status = readLiveStatus(t, s)
		require.Equal(t, "applying", status.AP.State)
		require.Equal(t, "checking", status.Radios[0].State)
		require.Equal(t, "empty", status.Radios[2].State)
	}
	clock = clock.Add(apPollInterval)
	failure = errors.New("AP unreachable")
	status = readLiveStatus(t, s)
	require.Equal(t, "unavailable", status.AP.State)
	require.Equal(t, "unknown", status.Radios[0].State, "an old link must not stay green")
	require.Equal(t, "empty", status.Radios[2].State)

	clock = clock.Add(apPollInterval)
	failure = nil
	snapshot.State = "ACTIVE"
	snapshot.Stations[1].RadioLinked = true
	status = readLiveStatus(t, s)
	require.Equal(t, "linked", status.Radios[1].State)
	s.status.AP = deviceResult{State: "failed", Detail: "Apply rejected"}
	status = readLiveStatus(t, s)
	require.Equal(t, "failed", status.AP.State, "monitoring cannot hide an Apply failure")
	require.Equal(t, "Apply rejected", status.AP.Detail)
	require.Zero(t, writes.Load(), "polling and mismatch detection must never reapply")
}

func TestMonitorExpiresLinksWhileAnotherReadIsPending(t *testing.T) {
	s := newTestServer(t)
	s.config.Stations[0] = 254
	clock := time.Now()
	s.now = func() time.Time { return clock }
	s.readAP = func(context.Context, model.PracticeNetwork) (network.AccessPointSnapshot, error) {
		return network.AccessPointSnapshot{State: "ACTIVE", Stations: [6]network.TeamWifiStatus{{TeamId: 254, RadioLinked: true}}}, nil
	}
	require.Equal(t, "linked", readLiveStatus(t, s).Radios[0].State)
	clock = clock.Add(apStatusMaxAge + time.Second)
	started, release := make(chan struct{}), make(chan struct{})
	s.readAP = func(context.Context, model.PracticeNetwork) (network.AccessPointSnapshot, error) {
		close(started)
		<-release
		return network.AccessPointSnapshot{}, errors.New("offline")
	}
	done := make(chan struct{})
	go func() { s.refreshAP(context.Background()); close(done) }()
	<-started
	status := readLiveStatus(t, s)
	require.Equal(t, "unknown", status.AP.State)
	require.Equal(t, "unknown", status.Radios[0].State)
	close(release)
	<-done
}

func TestMonitorDiscardsReadStartedBeforeApplyOrSettingsChange(t *testing.T) {
	for _, change := range []string{"apply", "settings"} {
		t.Run(change, func(t *testing.T) {
			s := newTestServer(t)
			s.config.Stations[0] = 254
			started, release := make(chan struct{}), make(chan struct{})
			s.readAP = func(context.Context, model.PracticeNetwork) (network.AccessPointSnapshot, error) {
				close(started)
				<-release
				return network.AccessPointSnapshot{State: "ACTIVE", Stations: [6]network.TeamWifiStatus{{TeamId: 254, RadioLinked: true}}}, nil
			}
			done := make(chan struct{})
			go func() { s.refreshAP(context.Background()); close(done) }()
			<-started
			if change == "apply" {
				s.configureAP = func(model.PracticeNetwork, [6]*model.Team) error { return nil }
				s.configureSwitch = s.configureAP
				w := call(t, s, "POST", "/api/apply", lineupRequest{Stations: []int{1678, 0, 0, 0, 0, 0}, CommonPassword: "password1"})
				require.Equal(t, http.StatusAccepted, w.Code)
				waitForApply(t, s) // The pending AP read is shared rather than duplicated.
			} else {
				settings := s.config.Network
				settings.ApAddress = "new-ap.example"
				w := call(t, s, "POST", "/api/settings", map[string]any{"revision": 0, "network": settings})
				require.Equal(t, http.StatusOK, w.Code)
			}
			close(release)
			<-done
			s.mu.Lock()
			status := s.liveStatus()
			s.mu.Unlock()
			require.Equal(t, 1, status.Revision)
			if change == "apply" {
				require.Equal(t, "applying", status.AP.State, "acceptance alone cannot turn the AP green")
				require.Equal(t, "checking", status.Radios[0].State)
			} else {
				require.Equal(t, "unknown", status.AP.State)
				require.Equal(t, "unknown", status.Radios[0].State)
			}
		})
	}
}

func TestMonitorSkipsDisabledNetworkingAndInFlightAPWrite(t *testing.T) {
	s := newTestServer(t)
	s.config.Stations[0] = 254
	var reads atomic.Int32
	s.readAP = func(context.Context, model.PracticeNetwork) (network.AccessPointSnapshot, error) {
		reads.Add(1)
		return network.AccessPointSnapshot{}, nil
	}
	s.config.Network.NetworkSecurityEnabled = false
	require.Equal(t, "disabled", readLiveStatus(t, s).AP.State)
	s.config.Network.NetworkSecurityEnabled = true
	s.status.AP.State = "applying"
	status := readLiveStatus(t, s)
	require.Equal(t, "applying", status.AP.State)
	require.Equal(t, "checking", status.Radios[0].State)
	require.Zero(t, reads.Load())
}
