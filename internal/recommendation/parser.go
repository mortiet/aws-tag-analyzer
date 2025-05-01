package recommendation

import (
	"fmt"
	"regexp"
	"strings"
)

// Define regex to capture action, key, and value
// Example: add:tag(key=value) or update:tag(key=value)
var recRegex = regexp.MustCompile(`^(add|update):tag\((.+?)=(.+?)\)$`)

// ParseRecommendation extracts the action, tag key, and tag value from a recommendation string.
func ParseRecommendation(rec string) (action string, key string, value string, err error) {
	matches := recRegex.FindStringSubmatch(rec)

	if len(matches) != 4 {
		err = fmt.Errorf("invalid recommendation format: '%s'. Expected 'action:tag(key=value)'", rec)
		return
	}

	action = strings.ToLower(strings.TrimSpace(matches[1]))
	key = strings.TrimSpace(matches[2])
	value = strings.TrimSpace(matches[3])

	if key == "" {
		err = fmt.Errorf("parsed empty key from recommendation: '%s'", rec)
		return
	}
	// Value can potentially be empty, so no check here.

	return action, key, value, nil
}
