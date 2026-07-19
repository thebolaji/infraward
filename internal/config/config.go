// Package config parses .infraward.yml, InfraWard's single config file, in
// the repo root. v0.1.0 only uses it for drift suppressions (type, id,
// id_pattern, and tag).
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where InfraWard looks for its config by default.
const DefaultPath = ".infraward.yml"

// Config is the parsed .infraward.yml.
type Config struct {
	Suppress []Rule `yaml:"suppress"`
}

// Rule is one suppression rule. Fields set within a rule are AND'd
// together; multiple rules in a Config are OR'd (any match suppresses).
//
// Tag matching only ever sees tags on Unmanaged resources: the drift engine
// only pays for the extra per-resource GetResource call (needed to fetch
// tags at all) when a config actually has a tag rule, and only for
// resources that already match that rule's Type/ID/IDPattern -- see
// NeedsTagCheck. Missing resources are gone from AWS, so they have no tags
// to check -- a tag rule never suppresses a Missing finding.
//
// A Tag rule with no Type, ID, or IDPattern to narrow it is rejected at
// load time: on an account with a large number of unmanaged resources,
// fetching tags for every one of them just to evaluate a single global tag
// rule reliably triggers Cloud Control throttling. Scoping the rule (most
// naturally by type) keeps the GetResource calls to the resources that
// could actually match.
type Rule struct {
	Type      string            `yaml:"type,omitempty"`
	ID        string            `yaml:"id,omitempty"`
	IDPattern string            `yaml:"id_pattern,omitempty"`
	Tag       map[string]string `yaml:"tag,omitempty"`

	// idPatternRe is IDPattern compiled by Load. IDPattern is a plain glob
	// ("*" = any run of characters, "?" = any single character) rather than
	// path.Match's semantics, where "*" refuses to cross "/" -- that would
	// silently fail to match ARNs, which are full of "/".
	idPatternRe *regexp.Regexp
}

// Load reads and validates the config at path. A missing file is not an
// error: it just means no suppressions are configured.
func Load(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", configPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", configPath, err)
	}

	for i := range cfg.Suppress {
		r := &cfg.Suppress[i]
		scoped := r.Type != "" || r.ID != "" || r.IDPattern != ""
		if !scoped && len(r.Tag) == 0 {
			return nil, fmt.Errorf("config: %s: suppress rule %d is empty (set type, id, id_pattern, or tag)", configPath, i)
		}
		if len(r.Tag) > 0 && !scoped {
			return nil, fmt.Errorf("config: %s: suppress rule %d uses \"tag\" with nothing to narrow it -- add type, id, or id_pattern; an unscoped tag rule would need to check every unmanaged resource's tags, which triggers AWS throttling on real accounts", configPath, i)
		}
		if r.IDPattern != "" {
			r.idPatternRe = globToRegexp(r.IDPattern)
		}
	}

	return &cfg, nil
}

// HasTagRules reports whether any suppress rule matches by tag. The drift
// engine uses this to skip the extra per-resource GetResource calls
// entirely when no rule needs tags.
func (c *Config) HasTagRules() bool {
	for _, r := range c.Suppress {
		if len(r.Tag) > 0 {
			return true
		}
	}
	return false
}

// NeedsTagCheck reports whether any tag rule's Type/ID/IDPattern already
// match this resource, meaning its tags are actually worth fetching. Every
// tag rule is guaranteed scoped (Load rejects unscoped ones), so this never
// degrades to "check everything."
func (c *Config) NeedsTagCheck(terraformType, id string) bool {
	for _, r := range c.Suppress {
		if len(r.Tag) == 0 {
			continue
		}
		if r.Type != "" && r.Type != terraformType {
			continue
		}
		if r.ID != "" && r.ID != id {
			continue
		}
		if r.idPatternRe != nil && !r.idPatternRe.MatchString(id) {
			continue
		}
		return true
	}
	return false
}

// Suppressed reports whether any rule matches the given resource, and if
// so, a human-readable reason suitable for --show-ignored output. tags may
// be nil (e.g. for Missing findings, or Unmanaged ones when no rule needs tags).
func (c *Config) Suppressed(terraformType, id string, tags map[string]string) (bool, string) {
	for _, r := range c.Suppress {
		if r.matches(terraformType, id, tags) {
			return true, r.describe()
		}
	}
	return false, ""
}

func (r Rule) matches(terraformType, id string, tags map[string]string) bool {
	if r.Type != "" && r.Type != terraformType {
		return false
	}
	if r.ID != "" && r.ID != id {
		return false
	}
	if r.idPatternRe != nil && !r.idPatternRe.MatchString(id) {
		return false
	}
	for k, v := range r.Tag {
		if tags[k] != v {
			return false
		}
	}
	return true
}

// globToRegexp compiles a shell-style glob ("*" = any run of characters,
// "?" = any single character) into an anchored regexp.
func globToRegexp(pattern string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String())
}

func (r Rule) describe() string {
	var parts []string
	if r.Type != "" {
		parts = append(parts, "type="+r.Type)
	}
	if r.ID != "" {
		parts = append(parts, "id="+r.ID)
	}
	if r.IDPattern != "" {
		parts = append(parts, "id_pattern="+r.IDPattern)
	}
	if len(r.Tag) > 0 {
		keys := make([]string, 0, len(r.Tag))
		for k := range r.Tag {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("tag[%s=%s]", k, r.Tag[k]))
		}
	}
	if len(parts) == 0 {
		return "suppressed"
	}
	s := parts[0]
	for _, p := range parts[1:] {
		s += " " + p
	}
	return s
}
