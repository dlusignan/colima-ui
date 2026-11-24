package colima

import (
	"context"
	"errors"
	"strings"
)

// SSHOption is one key and value line of an OpenSSH config block, in the
// order Colima printed it.
type SSHOption struct {
	Key   string
	Value string
}

// SSHConfig is the parsed output of `colima ssh-config -p <profile>`. Raw is
// the text exactly as Colima printed it. Entries holds every key and value
// line in order, Host included. The typed fields come from the first Host
// block; a repeated key (for example a second IdentityFile) keeps its first
// value in the typed field and stays visible in Entries.
type SSHConfig struct {
	Raw          string
	Host         string
	HostName     string
	Port         string
	User         string
	IdentityFile string
	Entries      []SSHOption
}

// maxSSHConfigLine bounds one line, so garbage cannot become a huge entry.
const maxSSHConfigLine = 4096

// ReadSSHConfig runs `colima ssh-config -p <profile>` (read-only, fixed
// argument vector) and parses the OpenSSH config it prints. The profile is
// validated before any process starts. Output that is not a Host block is a
// *ParseError; its Raw is kept for logs and never reaches Error().
func ReadSSHConfig(ctx context.Context, r Runner, profile string) (SSHConfig, error) {
	if err := ValidateProfile(profile); err != nil {
		return SSHConfig{}, err
	}
	res, err := run(ctx, r, []string{"ssh-config", "-p", profile})
	if err != nil {
		return SSHConfig{}, err
	}
	cfg, perr := parseSSHConfig(string(res.Stdout))
	if perr != nil {
		return SSHConfig{}, &ParseError{Op: "ssh-config", Raw: string(res.Stdout), Err: perr}
	}
	return cfg, nil
}

func parseSSHConfig(raw string) (SSHConfig, error) {
	cfg := SSHConfig{Raw: raw}
	seenHost, seen := false, map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > maxSSHConfigLine {
			return SSHConfig{}, errors.New("line too long")
		}
		key, val, ok := splitSSHLine(line)
		if !ok {
			return SSHConfig{}, errors.New("line without a value")
		}
		if !seenHost && !strings.EqualFold(key, "Host") {
			return SSHConfig{}, errors.New("expected a Host line first")
		}
		cfg.Entries = append(cfg.Entries, SSHOption{Key: key, Value: val})
		lk := strings.ToLower(key)
		if seenHost && lk == "host" {
			// A second block: the typed fields describe the first only.
			break
		}
		if seen[lk] {
			continue
		}
		seen[lk] = true
		switch lk {
		case "host":
			cfg.Host, seenHost = val, true
		case "hostname":
			cfg.HostName = val
		case "port":
			cfg.Port = val
		case "user":
			cfg.User = val
		case "identityfile":
			cfg.IdentityFile = val
		}
	}
	if !seenHost {
		return SSHConfig{}, errors.New("no Host line")
	}
	return cfg, nil
}

// splitSSHLine splits "Key value", "Key=value" or "Key = value" and strips one
// pair of surrounding double quotes from the value.
func splitSSHLine(line string) (key, val string, ok bool) {
	i := strings.IndexAny(line, " \t=")
	if i <= 0 {
		return "", "", false
	}
	key = line[:i]
	val = strings.TrimSpace(strings.TrimLeft(line[i:], " \t"))
	val = strings.TrimSpace(strings.TrimPrefix(val, "="))
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	if val == "" {
		return "", "", false
	}
	return key, val, true
}
