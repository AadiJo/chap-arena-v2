package practice

import (
	"fmt"
	"github.com/Team254/cheesy-arena/model"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var stationNames = [6]string{"Red 1", "Red 2", "Red 3", "Blue 1", "Blue 2", "Blue 3"}

// Build the existing network APIs' team representation without registering teams
// in the event roster. A nil entry clears the corresponding station.
func configuredTeams(config model.PracticeConfig) ([6]*model.Team, error) {
	var teams [6]*model.Team
	if config.CommonPassword != "" && !validPassword(config.CommonPassword) {
		return teams, fmt.Errorf("common password must contain 8 to 63 printable ASCII characters")
	}
	for team, password := range config.Overrides {
		if team < 1 || team > 25599 || !validPassword(password) {
			return teams, fmt.Errorf("team overrides need a team number from 1 to 25599 and an 8 to 63 character password")
		}
	}
	seen := map[int]bool{}
	for i, team := range config.Stations {
		if team == 0 {
			continue
		}
		if team < 1 || team > 25599 {
			return teams, fmt.Errorf("%s needs a team number from 1 to 25599", stationNames[i])
		}
		if seen[team] {
			return teams, fmt.Errorf("team %d is assigned to more than one station", team)
		}
		seen[team] = true
		password := config.CommonPassword
		if override := config.Overrides[team]; override != "" {
			password = override
		}
		if !validPassword(password) {
			return teams, fmt.Errorf("set a common password or a password override for team %d", team)
		}
		teams[i] = &model.Team{Id: team, WpaKey: password}
	}
	return teams, nil
}

func validPassword(password string) bool {
	if len(password) < 8 || len(password) > 63 {
		return false
	}
	for _, character := range password {
		if character < 32 || character > 126 {
			return false
		}
	}
	return true
}

func validateNetwork(settings model.PracticeNetwork) error {
	if settings.ApChannel < 5 || settings.ApChannel > 229 || (settings.ApChannel-5)%8 != 0 {
		return fmt.Errorf("select a valid AP Channel (6 GHz)")
	}
	if strings.ContainsAny(settings.SwitchPassword, "\r\n") {
		return fmt.Errorf("switch password cannot contain line breaks")
	}
	if strings.ContainsAny(settings.ApPassword, "\r\n") {
		return fmt.Errorf("AP API password cannot contain line breaks")
	}
	if !settings.NetworkSecurityEnabled {
		return nil
	}
	u, err := url.Parse("http://" + settings.ApAddress)
	if err != nil || u.Host != settings.ApAddress || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("AP Address must be a hostname or IP address, optionally with a port")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("AP Address has an invalid port")
		}
	}
	if settings.SwitchAddress == "" || strings.ContainsAny(settings.SwitchAddress, "/?#@ \t\r\n") || (strings.Contains(settings.SwitchAddress, ":") && net.ParseIP(settings.SwitchAddress) == nil) {
		return fmt.Errorf("Switch Address must be a hostname or IP address without a port")
	}
	return nil
}
