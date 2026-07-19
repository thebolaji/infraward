package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".infraward.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func TestLoad_missingFileIsNotAnError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yml"))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for a missing file", err)
	}
	if len(cfg.Suppress) != 0 {
		t.Errorf("expected no rules from a missing file, got %d", len(cfg.Suppress))
	}
}

func TestLoad_emptyRuleRejected(t *testing.T) {
	path := writeConfig(t, "suppress:\n  - {}\n")
	if _, err := Load(path); err == nil {
		t.Error("expected an error for a rule with no type/id/id_pattern/tag")
	}
}

func TestLoad_unscopedTagRuleRejected(t *testing.T) {
	// Regression: this exact shape reliably throttled a real account with a
	// large number of unmanaged resources, because it has nothing to narrow
	// which resources need a tag lookup. It must fail fast at load time instead.
	path := writeConfig(t, "suppress:\n  - tag:\n      managed-by: console\n")
	if _, err := Load(path); err == nil {
		t.Error("expected an error for a tag rule with no type/id/id_pattern to narrow it")
	}
}

func TestLoad_scopedTagRuleAccepted(t *testing.T) {
	path := writeConfig(t, "suppress:\n  - type: aws_instance\n    tag:\n      Name: myapp\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for a type-scoped tag rule", err)
	}
	if !cfg.HasTagRules() {
		t.Error("expected HasTagRules() to be true")
	}
}

func TestSuppressed_byType(t *testing.T) {
	cfg := &Config{Suppress: []Rule{{Type: "aws_iam_policy"}}}
	if ok, _ := cfg.Suppressed("aws_iam_policy", "arn:aws:iam::aws:policy/AdministratorAccess", nil); !ok {
		t.Error("expected type match to suppress")
	}
	if ok, _ := cfg.Suppressed("aws_instance", "i-123", nil); ok {
		t.Error("expected non-matching type to not suppress")
	}
}

func TestSuppressed_byID(t *testing.T) {
	cfg := &Config{Suppress: []Rule{{ID: "i-0123456789abcdef0"}}}
	if ok, _ := cfg.Suppressed("aws_instance", "i-0123456789abcdef0", nil); !ok {
		t.Error("expected exact ID match to suppress")
	}
	if ok, _ := cfg.Suppressed("aws_instance", "i-other", nil); ok {
		t.Error("expected different ID to not suppress")
	}
}

func TestSuppressed_byIDPattern_matchesARNsWithSlashes(t *testing.T) {
	// Regression: path.Match's "*" refuses to cross "/", which silently
	// fails to match ARNs -- ELB target group ARNs have several "/"
	// segments. The glob must treat "*" as matching anything, slashes included.
	path := writeConfig(t, "suppress:\n  - id_pattern: \"arn:aws:elasticloadbalancing:*\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	arn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-app-alb/1234567890abcdef"
	if ok, _ := cfg.Suppressed("aws_lb", arn, nil); !ok {
		t.Errorf("expected id_pattern to match ARN with slashes: %s", arn)
	}
	if ok, _ := cfg.Suppressed("aws_lb", "arn:aws:s3:::my-bucket", nil); ok {
		t.Error("expected non-matching ARN to not suppress")
	}
}

func TestSuppressed_byIDPattern_singleCharWildcard(t *testing.T) {
	path := writeConfig(t, "suppress:\n  - id_pattern: \"i-???\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ok, _ := cfg.Suppressed("aws_instance", "i-abc", nil); !ok {
		t.Error("expected ? to match exactly one character")
	}
	if ok, _ := cfg.Suppressed("aws_instance", "i-abcd", nil); ok {
		t.Error("expected ? to not match extra characters")
	}
}

func TestSuppressed_byTag(t *testing.T) {
	path := writeConfig(t, "suppress:\n  - type: aws_instance\n    tag:\n      Name: myapp\n      env: prod\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	ok, reason := cfg.Suppressed("aws_instance", "i-123", map[string]string{"Name": "myapp", "env": "prod", "extra": "ignored"})
	if !ok {
		t.Fatal("expected all-tags-match to suppress")
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}

	if ok, _ := cfg.Suppressed("aws_instance", "i-123", map[string]string{"Name": "myapp"}); ok {
		t.Error("expected a partial tag match (missing env) to not suppress")
	}
	if ok, _ := cfg.Suppressed("aws_instance", "i-123", nil); ok {
		t.Error("expected nil tags (e.g. a Missing finding) to never match a tag rule")
	}
}

func TestSuppressed_rulesAreORed(t *testing.T) {
	cfg := &Config{Suppress: []Rule{{Type: "aws_iam_policy"}, {ID: "i-specific"}}}
	if ok, _ := cfg.Suppressed("aws_instance", "i-specific", nil); !ok {
		t.Error("expected a match on the second rule to suppress even though the first didn't match")
	}
}

func TestNeedsTagCheck_onlyForScopedResources(t *testing.T) {
	path := writeConfig(t, "suppress:\n  - type: aws_instance\n    tag:\n      Name: myapp\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.NeedsTagCheck("aws_instance", "i-123") {
		t.Error("expected NeedsTagCheck to be true for the type a tag rule is scoped to")
	}
	if cfg.NeedsTagCheck("aws_iam_policy", "arn:x") {
		t.Error("expected NeedsTagCheck to be false for a type no tag rule mentions -- this is the whole point of scoping")
	}
}

func TestNeedsTagCheck_falseWithoutTagRules(t *testing.T) {
	cfg := &Config{Suppress: []Rule{{Type: "aws_instance"}}}
	if cfg.NeedsTagCheck("aws_instance", "i-123") {
		t.Error("expected NeedsTagCheck to be false when no rule uses tag at all")
	}
}
