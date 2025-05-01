package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

// PolicyRule defines a single rule for a tag.
type PolicyRule struct {
	Key       string   `json:"key"`
	Value     []string `json:"value,omitempty"` // List of allowed values, if restricted
	Status    string   `json:"status"`          // e.g., "include" (currently only supported value)
	Mandatory bool     `json:"mandatory"`       // Renamed from 'mandetory'
}

// Policy is now a slice of PolicyRule.
type Policy []PolicyRule

// ValidationIssue represents a single tagging policy violation found on a resource.
type ValidationIssue struct {
	IssueType      string            `json:"issue_type"` // e.g., "MissingMandatory", "InvalidValue", "MissingDesirable"
	TagKey         string            `json:"tag_key"`
	TagValue       string            `json:"tag_value,omitempty"`      // Current value if relevant (e.g., for InvalidValue)
	AllowedValues  []string          `json:"allowed_values,omitempty"` // Allowed values from policy if relevant
	Recommendation map[string]string `json:"recommendation,omitempty"` // Changed from string to map[string]string
}

// LoadPolicy loads the tag policy from a JSON file (new structure).
func LoadPolicy(filePath string) (*Policy, error) {
	if filePath == "" {
		log.Info().Msg("No policy file specified. Skipping policy validation.")
		return nil, nil // No error, just no policy to apply
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file '%s': %w", filePath, err)
	}

	var pol Policy // Changed to slice
	err = json.Unmarshal(data, &pol)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal policy file '%s': %w", filePath, err)
	}

	log.Info().Str("path", filePath).Int("rules_loaded", len(pol)).Msg("Successfully loaded tag policy")
	return &pol, nil
}

// ValidateResource checks a single resource against the loaded policy (new structure).
// It assumes tags are already potentially lowercased if the flag was set.
func ValidateResource(resKind, resName, resRegion string, tags map[string]string, pol *Policy, isLowercase bool) []ValidationIssue {
	if pol == nil {
		return nil // No policy to validate against
	}

	var issues []ValidationIssue

	// Iterate through each rule in the policy
	for _, rule := range *pol {
		if rule.Status != "include" { // Only handle "include" status for now
			log.Warn().Str("key", rule.Key).Str("status", rule.Status).Msg("Unsupported policy status encountered, skipping rule.")
			continue
		}

		keyToCheck := rule.Key
		if isLowercase {
			keyToCheck = strings.ToLower(rule.Key)
		}

		currentValue, tagExists := tags[keyToCheck]

		// Prepare allowed values list, considering lowercase flag
		var allowedValuesForCheck []string
		hasValueRestriction := len(rule.Value) > 0
		if hasValueRestriction {
			if isLowercase {
				for _, v := range rule.Value {
					allowedValuesForCheck = append(allowedValuesForCheck, strings.ToLower(v))
				}
			} else {
				allowedValuesForCheck = rule.Value
			}
		}

		// --- Mandatory Check ---
		if rule.Mandatory {
			if !tagExists {
				issues = append(issues, ValidationIssue{
					IssueType:      "MissingMandatory",
					TagKey:         rule.Key, // Report original key from policy
					Recommendation: nil,      // Set to nil, AI will populate later if needed
				})
			} else if hasValueRestriction {
				// Mandatory tag exists, check value if restricted
				isValueAllowed := false
				for _, allowed := range allowedValuesForCheck {
					if currentValue == allowed {
						isValueAllowed = true
						break
					}
				}
				if !isValueAllowed {
					issues = append(issues, ValidationIssue{
						IssueType:      "InvalidValue",
						TagKey:         rule.Key,     // Report original key
						TagValue:       currentValue, // Report actual value
						AllowedValues:  rule.Value,   // Report original allowed values
						Recommendation: nil,          // Set to nil, AI will populate later if needed
					})
				}
			}
		} else {
			// --- Optional/Desirable Check ---
			if tagExists && hasValueRestriction {
				// Optional tag exists, check value if restricted
				isValueAllowed := false
				for _, allowed := range allowedValuesForCheck {
					if currentValue == allowed {
						isValueAllowed = true
						break
					}
				}
				if !isValueAllowed {
					issues = append(issues, ValidationIssue{
						IssueType:      "InvalidValue",
						TagKey:         rule.Key,     // Report original key
						TagValue:       currentValue, // Report actual value
						AllowedValues:  rule.Value,   // Report original allowed values
						Recommendation: nil,          // Set to nil, AI will populate later if needed
					})
				}
			} else if !tagExists && !hasValueRestriction {
				// Optional tag does not exist AND has no value restriction -> treat as desirable
				issues = append(issues, ValidationIssue{
					IssueType:      "MissingDesirable",
					TagKey:         rule.Key, // Report original key
					Recommendation: nil,      // Set to nil, AI will populate later if needed
				})
			}
		}
	}

	return issues
}
