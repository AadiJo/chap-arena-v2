// Package practice serves the networking-only application. It never starts the
// arena state machine or Driver Station listeners.
package practice

import (
	"context"
	"embed"
	"encoding/json"
	"github.com/Team254/cheesy-arena/model"
	"github.com/Team254/cheesy-arena/network"
	"io"
	"io/fs"
	"maps"
	"mime"
	"net/http"
	"sync"
	"time"
)

//go:embed assets/*
var assets embed.FS

type deviceResult struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type applyStatus struct {
	Revision int          `json:"revision"`
	Applying bool         `json:"applying"`
	AP       deviceResult `json:"ap"`
	Switch   deviceResult `json:"switch"`
}

type Server struct {
	mu              sync.Mutex
	database        *model.Database
	config          model.PracticeConfig
	status          applyStatus
	configureAP     func(model.PracticeNetwork, [6]*model.Team) error
	configureSwitch func(model.PracticeNetwork, [6]*model.Team) error
	readAP          func(context.Context, model.PracticeNetwork) (network.AccessPointSnapshot, error)
	observation     apObservation
	polling         bool
	now             func() time.Time
}

func NewServer(database *model.Database) (*Server, error) {
	config, err := database.GetPracticeConfig()
	if err != nil {
		return nil, err
	}
	return &Server{
		database: database,
		config:   *config,
		status:   applyStatus{Revision: config.Revision, AP: deviceResult{State: "idle"}, Switch: deviceResult{State: "idle"}},
		now:      time.Now,
		configureAP: func(settings model.PracticeNetwork, teams [6]*model.Team) error {
			var ap network.AccessPoint
			ap.SetSettings(settings.ApAddress, settings.ApPassword, settings.ApChannel, true, [6]*network.TeamWifiStatus{})
			return ap.ConfigureTeamWifi(teams)
		},
		configureSwitch: func(settings model.PracticeNetwork, teams [6]*model.Team) error {
			return network.NewSwitch(settings.SwitchAddress, settings.SwitchPassword).ConfigureTeamEthernet(teams)
		},
		readAP: func(ctx context.Context, settings model.PracticeNetwork) (network.AccessPointSnapshot, error) {
			var ap network.AccessPoint
			ap.SetSettings(settings.ApAddress, settings.ApPassword, settings.ApChannel, settings.NetworkSecurityEnabled, [6]*network.TeamWifiStatus{})
			return ap.ReadStatus(ctx)
		},
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	files, _ := fs.Sub(assets, "assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(files)))
	page := func(w http.ResponseWriter, r *http.Request) {
		data, _ := assets.ReadFile("assets/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
	mux.HandleFunc("GET /{$}", page)
	mux.HandleFunc("GET /settings", page)
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("GET /api/status", s.getStatus)
	mux.HandleFunc("POST /api/settings", s.saveSettings)
	mux.HandleFunc("POST /api/apply", s.apply)
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'")
		protected.ServeHTTP(w, r)
	})
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.config)
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	s.refreshAP(r.Context())
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.liveStatus())
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Revision int                   `json:"revision"`
		Network  model.PracticeNetwork `json:"network"`
	}
	if !readJSON(w, r, &request) {
		return
	}
	if err := validateNetwork(request.Network); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canChange(w, request.Revision) {
		return
	}
	next := s.config
	next.Network = request.Network
	if !s.save(w, next) {
		return
	}
	s.status = applyStatus{Revision: s.config.Revision, AP: deviceResult{State: "idle"}, Switch: deviceResult{State: "idle"}}
	writeJSON(w, http.StatusOK, s.config)
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Revision       int            `json:"revision"`
		Stations       []int          `json:"stations"`
		CommonPassword string         `json:"commonPassword"`
		Overrides      map[int]string `json:"overrides"`
	}
	if !readJSON(w, r, &request) {
		return
	}
	if len(request.Stations) != 6 {
		http.Error(w, "Provide exactly six station assignments.", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canChange(w, request.Revision) {
		return
	}
	next := s.config
	next.Stations = [6]int(request.Stations)
	next.CommonPassword = request.CommonPassword
	next.Overrides = maps.Clone(request.Overrides)
	for team, password := range next.Overrides {
		if password == "" {
			delete(next.Overrides, team)
		}
	}
	teams, err := configuredTeams(next)
	if err == nil {
		err = validateNetwork(next.Network)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !next.Network.NetworkSecurityEnabled {
		http.Error(w, "Enable advanced network security in Settings before applying.", http.StatusBadRequest)
		return
	}
	if !s.save(w, next) {
		return
	}
	s.status = applyStatus{Revision: s.config.Revision, Applying: true, AP: deviceResult{State: "applying"}, Switch: deviceResult{State: "pending"}}
	go s.configure(next.Network, teams)
	writeJSON(w, http.StatusAccepted, s.status)
}

// Serialize complete applies, including persistence, so two clients cannot mix
// AP assignments from one request with switch assignments from another.
func (s *Server) canChange(w http.ResponseWriter, revision int) bool {
	if s.status.Applying {
		http.Error(w, "Configuration is in progress. Wait for it to finish.", http.StatusConflict)
		return false
	}
	if revision != s.config.Revision {
		http.Error(w, "Configuration changed in another tab. Reload before applying your changes.", http.StatusConflict)
		return false
	}
	return true
}

func (s *Server) save(w http.ResponseWriter, next model.PracticeConfig) bool {
	next.Revision++
	if err := s.database.SavePracticeConfig(&next); err != nil {
		http.Error(w, "Could not save configuration. Hardware was not changed.", http.StatusInternalServerError)
		return false
	}
	s.config = next
	return true
}

func (s *Server) configure(settings model.PracticeNetwork, teams [6]*model.Team) {
	apErr := s.configureAP(settings, teams)
	s.mu.Lock()
	s.status.AP = deviceResult{State: "accepted", Detail: "Configuration accepted. The AP applies it asynchronously."}
	if apErr != nil {
		s.status.AP = deviceResult{State: "failed", Detail: apErr.Error()}
	}
	s.status.Switch = deviceResult{State: "applying"}
	s.mu.Unlock()

	switchErr := s.configureSwitch(settings, teams)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Switch = deviceResult{State: "applied"}
	if switchErr != nil {
		s.status.Switch = deviceResult{State: "failed", Detail: switchErr.Error()}
	}
	s.status.Applying = false
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Send application/json.", http.StatusUnsupportedMediaType)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		http.Error(w, "Invalid configuration JSON.", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "Send one configuration object.", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// A disconnected browser does not cancel an accepted hardware operation.
		return
	}
}
