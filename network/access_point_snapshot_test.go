package network

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestAccessPointReadStatusDoesNotConfigureOrMutate(t *testing.T) {
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/status" {
			writes.Add(1)
			http.Error(w, "unexpected configuration request", 500)
			return
		}
		reads.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "bad authentication", 401)
			return
		}
		w.Write([]byte(`{"status":"ACTIVE","channel":5,"stationStatuses":{"red1":{"ssid":"254","isLinked":true},"red2":{"ssid":"1678","isLinked":false},"blue1":{"ssid":"971","isLinked":true}}}`))
	}))
	defer server.Close()
	var ap AccessPoint
	ap.SetSettings("unused", "test-token", 5, true, [6]*TeamWifiStatus{})
	ap.apiUrl = server.URL
	snapshot, err := ap.ReadStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", snapshot.State)
	require.Equal(t, 5, snapshot.Channel)
	require.Equal(t, 254, snapshot.Stations[0].TeamId)
	require.True(t, snapshot.Stations[0].RadioLinked)
	require.Equal(t, 1678, snapshot.Stations[1].TeamId)
	require.False(t, snapshot.Stations[1].RadioLinked)
	require.Zero(t, snapshot.Stations[2].TeamId)
	require.Equal(t, "UNKNOWN", ap.Status)
	require.EqualValues(t, 1, reads.Load())
	require.Zero(t, writes.Load())

	ap.networkSecurityEnabled = false
	snapshot, err = ap.ReadStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, "DISABLED", snapshot.State)
	require.EqualValues(t, 1, reads.Load())
}

func TestAccessPointReadStatusErrors(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
	}{
		{"authentication", "denied", 401},
		{"invalid JSON", "not JSON", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.code)
				w.Write([]byte(test.body))
			}))
			defer server.Close()
			ap := AccessPoint{apiUrl: server.URL, networkSecurityEnabled: true}
			snapshot, err := ap.ReadStatus(context.Background())
			require.Error(t, err)
			require.Empty(t, snapshot.State)
		})
	}
}

func TestAccessPointReadStatusHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ap := AccessPoint{apiUrl: server.URL, networkSecurityEnabled: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := ap.ReadStatus(ctx); done <- err }()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}
