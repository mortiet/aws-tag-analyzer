package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

// TagRule defines a single rule in the tag policy.
type TagRule struct {
	Key       string   `json:"key"`
	Mandatory bool     `json:"mandatory"`
	Value     []string `json:"value,omitempty"` // Optional list of allowed values
	Status    string   `json:"status"`          // "active" or "inactive"
}

// TagPolicy represents the overall tag policy structure.
type TagPolicy struct {
	Rules []TagRule `json:"rules"`
}

// ValidationIssue defines the structure for a tag validation issue.
// This struct was implicitly used by ValidateResource and is needed here.
type ValidationIssue struct {
	IssueType      string            `json:"issue_type"` // e.g., "MissingMandatory", "InvalidValue", "MissingDesirable"
	TagKey         string            `json:"tag_key"`
	TagValue       string            `json:"tag_value,omitempty"`      // Included for InvalidValue
	AllowedValues  []string          `json:"allowed_values,omitempty"` // Included for InvalidValue
	Recommendation map[string]string `json:"recommendation,omitempty"` // Populated later by AI step
}

// LoadPolicy loads the tag policy from a JSON file.
// If filename is empty, it returns an empty policy without error.
func LoadPolicy(filename string) (*TagPolicy, error) {
	if filename == "" {
		log.Debug().Msg("No policy file specified, returning empty policy.")
		// Return a default empty policy if no file is specified
		return &TagPolicy{Rules: []TagRule{}}, nil // Ensure empty slice, not nil
	}

	log.Debug().Str("file", filename).Msg("Loading policy file")
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file %s: %w", filename, err)
	}

	// Check if the file is empty
	if len(data) == 0 {
		log.Warn().Str("file", filename).Msg("Policy file is empty, returning empty policy.")
		return &TagPolicy{Rules: []TagRule{}}, nil
	}

	var rules []TagRule // Expecting a JSON array of rule objects directly
	err = json.Unmarshal(data, &rules)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal policy JSON from %s (expected JSON array of rules): %w", filename, err)
	}

	// Filter out inactive rules and validate rule structure
	activeRules := []TagRule{}
	for i, rule := range rules {
		if rule.Key == "" {
			log.Warn().Int("rule_index", i).Msg("Skipping rule with empty key in policy file.")
			continue
		}
		rule.Status = strings.ToLower(strings.TrimSpace(rule.Status))
		if rule.Status == "active" {
			activeRules = append(activeRules, rule)
			log.Trace().Str("key", rule.Key).Msg("Loaded active policy rule")
		} else {
			log.Trace().Str("key", rule.Key).Str("status", rule.Status).Msg("Skipping inactive policy rule")
		}
	}

	policy := &TagPolicy{Rules: activeRules}
	log.Debug().Int("active_rules", len(policy.Rules)).Str("file", filename).Msg("Successfully loaded and processed policy file")
	return policy, nil
}

// ValidateResource validates a resource's tags against the policy.
// Takes lowercase flag into account for case-insensitive matching.
func ValidateResource(kind, name, region string, tags map[string]string, policy *TagPolicy, handleLowercase bool) []ValidationIssue {
	issues := []ValidationIssue{}
	// Policy is guaranteed non-nil by LoadPolicy, but check rules just in case
	if policy == nil || len(policy.Rules) == 0 {
		log.Trace().Str("kind", kind).Str("name", name).Msg("Skipping validation, no active policy rules.")
		return issues // No policy or no rules, no issues
	}

	// Prepare tags for comparison (lowercase if needed)
	compareTags := tags
	originalTags := tags // Keep original tags for reporting values
	if handleLowercase {
		compareTags = make(map[string]string, len(tags))
		for k, v := range tags {
			compareTags[strings.ToLower(k)] = strings.ToLower(v)
		}
	}

	// Keep track of keys checked to find missing desirable tags later
	checkedKeys := make(map[string]bool)

	for _, rule := range policy.Rules {
		// Use original rule key for reporting, comparison key for checking
		originalRuleKey := rule.Key
		compareRuleKey := originalRuleKey
		if handleLowercase {
			compareRuleKey = strings.ToLower(originalRuleKey)
		}
		checkedKeys[compareRuleKey] = true // Mark this rule key as checked

		tagValue, exists := compareTags[compareRuleKey]
		originalTagValue := originalTags[originalRuleKey] // Get original value for reporting if exists

		if rule.Mandatory {
			if !exists {
				issues = append(issues, ValidationIssue{
					IssueType: "MissingMandatory",
					TagKey:    originalRuleKey, // Report original key from policy
				})
				log.Trace().Str("kind", kind).Str("name", name).Str("key", originalRuleKey).Msg("Missing mandatory tag")
			} else if len(rule.Value) > 0 { // Mandatory and has value restrictions
				isValidValue := false
				for _, allowedValue := range rule.Value {
					compareAllowedValue := allowedValue
					if handleLowercase {
						compareAllowedValue = strings.ToLower(allowedValue)
					}
					if tagValue == compareAllowedValue {
						isValidValue = true
						break
					}
				}
				if !isValidValue {
					issues = append(issues, ValidationIssue{
						IssueType:     "InvalidValue",
						TagKey:        originalRuleKey,  // Report original key
						TagValue:      originalTagValue, // Report original value
						AllowedValues: rule.Value,       // Report original allowed values
					})
					log.Trace().Str("kind", kind).Str("name", name).Str("key", originalRuleKey).Str("value", originalTagValue).Msg("Invalid value for mandatory tag")
				}
			}
		} else { // Not mandatory (Optional/Desirable)
			if exists && len(rule.Value) > 0 { // Optional tag exists, check value if restricted
				isValidValue := false
				for _, allowedValue := range rule.Value {
					compareAllowedValue := allowedValue
					if handleLowercase {
						compareAllowedValue = strings.ToLower(allowedValue)
					}
					if tagValue == compareAllowedValue {
						isValidValue = true
						break
					}
				}
				if !isValidValue {
					issues = append(issues, ValidationIssue{
						IssueType:     "InvalidValue",
						TagKey:        originalRuleKey,
						TagValue:      originalTagValue,
						AllowedValues: rule.Value,
					})
					log.Trace().Str("kind", kind).Str("name", name).Str("key", originalRuleKey).Str("value", originalTagValue).Msg("Invalid value for optional tag")
				}
			}
			// We don't add "MissingDesirable" here yet, handle at the end
		}
	}

	// Check for missing desirable tags (non-mandatory rules that weren't present)
	for _, rule := range policy.Rules {
		if !rule.Mandatory {
			originalRuleKey := rule.Key
			compareRuleKey := originalRuleKey
			if handleLowercase {
				compareRuleKey = strings.ToLower(originalRuleKey)
			}
			if _, exists := compareTags[compareRuleKey]; !exists {
				// Check if we already added an InvalidValue issue for this key (shouldn't happen, but safety check)
				alreadyReported := false
				for _, issue := range issues {
					// Compare against the original key used in reporting
					if issue.TagKey == originalRuleKey {
						alreadyReported = true
						break
					}
				}
				if !alreadyReported {
					issues = append(issues, ValidationIssue{
						IssueType: "MissingDesirable",
						TagKey:    originalRuleKey,
					})
					log.Trace().Str("kind", kind).Str("name", name).Str("key", originalRuleKey).Msg("Missing desirable tag")
				}
			}
		}
	}

	return issues
}
