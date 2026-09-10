package practice

import (
	"context"
	"github.com/Team254/cheesy-arena/network"
	"time"
)

const apPollInterval = time.Second
const apStatusMaxAge = 5 * time.Second

type apObservation struct {
	revision int
	readAt   time.Time
	snapshot network.AccessPointSnapshot
	err      error
}

type radioResult struct {
	Team  int    `json:"team"`
	State string `json:"state"`
}

type liveStatus struct {
	applyStatus
	Radios [6]radioResult `json:"radios"`
}

// Browser polling shares a bounded, read-only AP request. Neither concurrent
// viewers nor old responses can reconfigure hardware or replace newer state.
func (s *Server) refreshAP(ctx context.Context) {
	s.mu.Lock()
	revision, settings := s.config.Revision, s.config.Network
	if !settings.NetworkSecurityEnabled || s.status.AP.State == "applying" || s.polling ||
		(s.observation.revision == revision && !s.observation.readAt.IsZero() && s.now().Sub(s.observation.readAt) < apPollInterval) {
		s.mu.Unlock()
		return
	}
	s.polling = true
	s.mu.Unlock()

	snapshot, err := s.readAP(ctx, settings)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polling = false
	if ctx.Err() == nil && revision == s.config.Revision && s.status.AP.State != "applying" {
		s.observation = apObservation{revision: revision, readAt: s.now(), snapshot: snapshot, err: err}
	}
}

// Called with s.mu held. Apply results and live AP observations remain separate:
// a healthy status response must not hide a failed configuration request.
func (s *Server) liveStatus() liveStatus {
	result := liveStatus{applyStatus: s.status}
	for i, team := range s.config.Stations {
		result.Radios[i] = radioResult{Team: team, State: "unknown"}
		if team == 0 {
			result.Radios[i].State = "empty"
		}
	}
	if !s.config.Network.NetworkSecurityEnabled {
		result.AP = deviceResult{State: "disabled"}
		return result
	}
	if s.status.AP.State == "applying" {
		result.checkingRadios()
		return result
	}
	observed := s.observation
	fresh := observed.revision == s.config.Revision && !observed.readAt.IsZero() && s.now().Sub(observed.readAt) <= apStatusMaxAge
	switch {
	case !fresh:
		result.AP = deviceResult{State: "unknown", Detail: "Waiting for a current AP status report."}
		if s.status.AP.State == "accepted" && (observed.revision != s.config.Revision || observed.readAt.IsZero()) {
			result.AP.State = "applying"
			result.checkingRadios()
		}
	case observed.err != nil:
		result.AP = deviceResult{State: "unavailable", Detail: observed.err.Error()}
	case observed.snapshot.State == "ACTIVE":
		result.AP = deviceResult{State: "active"}
		for i, radio := range observed.snapshot.Stations {
			team := result.Radios[i].Team
			if team == 0 {
				continue
			}
			switch {
			case radio.TeamId == 0:
				result.Radios[i].State = "unconfigured"
			case radio.TeamId != team:
				result.Radios[i].State = "mismatch"
			case radio.RadioLinked:
				result.Radios[i].State = "linked"
			default:
				result.Radios[i].State = "missing"
			}
		}
	case observed.snapshot.State == "CONFIGURING" || observed.snapshot.State == "BOOTING":
		result.AP = deviceResult{State: "applying", Detail: "Waiting for the AP to become active."}
		result.checkingRadios()
	case observed.snapshot.State == "DISABLED":
		result.AP = deviceResult{State: "disabled"}
	default:
		result.AP = deviceResult{State: "unavailable", Detail: "The AP has not reported an active network."}
	}
	if s.status.AP.State == "failed" {
		result.AP = s.status.AP
	}
	return result
}

func (status *liveStatus) checkingRadios() {
	for i := range status.Radios {
		if status.Radios[i].Team != 0 {
			status.Radios[i].State = "checking"
		}
	}
}
