package architecture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Allowlist is the explicit, ADR-backed escape hatch for architecture violations.
type Allowlist struct {
	Version    int         `json:"version"`
	Exceptions []Exception `json:"exceptions"`
}

// Exception suppresses diagnostics matching all non-empty fields.
type Exception struct {
	Rule      string `json:"rule"`
	From      string `json:"from"`
	To        string `json:"to,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Reason    string `json:"reason"`
	ADR       string `json:"adr"`
	ExpiresOn string `json:"expires_on,omitempty"`
}

func loadAllowlist(root, name string) (Allowlist, []Diagnostic, error) {
	if name == "" {
		return Allowlist{Version: 1}, nil, nil
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(root, filepath.FromSlash(name))
	}
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return Allowlist{Version: 1}, nil, nil
	}
	if err != nil {
		return Allowlist{}, nil, fmt.Errorf("read allowlist: %w", err)
	}
	var allowlist Allowlist
	if err := json.Unmarshal(data, &allowlist); err != nil {
		return Allowlist{}, nil, fmt.Errorf("parse allowlist: %w", err)
	}
	if allowlist.Version != 1 {
		return Allowlist{}, nil, fmt.Errorf("unsupported allowlist version %d", allowlist.Version)
	}
	var diagnostics []Diagnostic
	valid := allowlist.Exceptions[:0]
	for _, exception := range allowlist.Exceptions {
		message := ""
		adrPath := filepath.Clean(filepath.FromSlash(exception.ADR))
		switch {
		case exception.Rule == "" || exception.From == "" || exception.Reason == "":
			message = "allowlist exception requires rule, from, and reason"
		case exception.ADR == "":
			message = "allowlist exception requires an ADR"
		case filepath.IsAbs(adrPath) || adrPath == ".." || strings.HasPrefix(filepath.ToSlash(adrPath), "../"):
			message = "allowlist ADR must be a project-relative path"
		case !regularFile(filepath.Join(root, adrPath)):
			message = fmt.Sprintf("allowlist ADR %q does not exist", exception.ADR)
		case exception.ExpiresOn != "":
			expires, parseErr := time.Parse(time.DateOnly, exception.ExpiresOn)
			if parseErr != nil {
				message = fmt.Sprintf("allowlist expiration %q is not YYYY-MM-DD", exception.ExpiresOn)
			} else if time.Now().After(expires.Add(24 * time.Hour)) {
				message = fmt.Sprintf("allowlist exception expired on %s", exception.ExpiresOn)
			}
		}
		if message != "" {
			displayName, relErr := filepath.Rel(root, name)
			if relErr != nil {
				displayName = name
			}
			diagnostics = append(diagnostics, Diagnostic{
				Rule: "allowlist.adr", Severity: SeverityError, Message: message,
				Location: Location{Path: filepath.ToSlash(displayName)},
			})
			continue
		}
		valid = append(valid, exception)
	}
	allowlist.Exceptions = valid
	return allowlist, diagnostics, nil
}

func regularFile(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular()
}

func (a Allowlist) allows(diagnostic Diagnostic) bool {
	for _, exception := range a.Exceptions {
		if exception.Rule != diagnostic.Rule || !pathFieldMatches(exception.From, diagnostic.Location.Path) {
			continue
		}
		if exception.To != "" && !pathFieldMatches(exception.To, diagnostic.Target) {
			continue
		}
		if exception.Symbol != "" && exception.Symbol != diagnostic.Symbol {
			continue
		}
		if exception.ADR == "" || exception.Reason == "" {
			continue
		}
		if exception.ExpiresOn != "" {
			expires, err := time.Parse(time.DateOnly, exception.ExpiresOn)
			if err != nil || time.Now().After(expires.Add(24*time.Hour)) {
				continue
			}
		}
		return true
	}
	return false
}

func pathFieldMatches(pattern, name string) bool {
	if pattern == name {
		return true
	}
	matches, _ := matchPattern(pattern, name)
	return matches
}
