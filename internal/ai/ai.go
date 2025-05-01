package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp" // Import regexp package
	"strings"
	"time"

	"aws-tag-analyzer/internal/policy"

	"github.com/rs/zerolog/log"
)

// ResourceExample holds information about a well-tagged resource for context.
type ResourceExample struct {
	Kind   string            `json:"kind"`
	Name   string            `json:"name"`
	Region string            `json:"region"`
	Tags   map[string]string `json:"tags"`
}

// Message defines the structure for chat messages in the AI request.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AIRequest defines the structure for the AI API request body.
type AIRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"` // Add MaxTokens field
}

// AIResponse defines the structure for the AI API response body.
type AIResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	// Include other fields if needed, like usage statistics
}

// Define a regex to find the recommendation pattern
// It looks for add:tag(...), update:tag(...), or remove:tag(...)
var recommendationRegex = regexp.MustCompile(`(add|update|remove):tag\([^)]+\)`)

// GetAIRecommendation queries the AI API for a tag recommendation.
// NOTE: Ensure this function is not declared elsewhere in the 'ai' package (e.g., in client.go) to avoid redeclaration errors.
func GetAIRecommendation(apiURL, apiToken, modelName, kind, name, region string, tags map[string]string, issue policy.ValidationIssue, examples []ResourceExample, timeout time.Duration) (string, error) {
	prompt := buildPrompt(kind, name, region, tags, issue, examples)
	log.Trace().Str("kind", kind).Str("name", name).Str("issue", issue.IssueType).Str("prompt", prompt).Msg("Built AI prompt")

	requestBody := AIRequest{
		Model: modelName,
		Messages: []Message{
			{Role: "system", Content: "You are an expert in AWS tagging. Provide ONLY the suggested tag change in the format 'action:tag(key=value)' or 'action:tag(key)'. Action can be 'add', 'update', or 'remove'. Do not add any explanations or markdown formatting."},
			{Role: "user", Content: prompt},
		},
		MaxTokens: 50, // Keep response concise
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal AI request: %w", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create AI request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send AI request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to read body for more details, but don't fail if reading fails
		var bodyBytes []byte
		if resp.Body != nil {
			bodyBytes, _ = io.ReadAll(resp.Body)
		}
		return "", fmt.Errorf("AI API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var aiResp AIResponse
	if err := json.NewDecoder(resp.Body).Decode(&aiResp); err != nil {
		return "", fmt.Errorf("failed to decode AI response: %w", err)
	}

	if len(aiResp.Choices) == 0 || aiResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("AI response was empty or invalid")
	}

	rawContent := aiResp.Choices[0].Message.Content
	log.Trace().Str("raw_response", rawContent).Msg("Received raw AI response")

	// --- Extract the recommendation pattern ---
	match := recommendationRegex.FindString(rawContent)
	if match == "" {
		log.Warn().Str("raw_response", rawContent).Msg("Could not extract recommendation pattern from AI response. Returning raw content.")
		// Return the raw content but maybe log a warning, or return an error?
		// For now, let's return the raw content and let the parser handle it later.
		// This might be noisy if the AI consistently adds explanations.
		// Alternatively, return an error:
		// return "", fmt.Errorf("could not extract recommendation pattern from AI response: %s", rawContent)
		return strings.TrimSpace(rawContent), nil // Return trimmed raw content if no pattern found
	}

	log.Debug().Str("extracted_recommendation", match).Str("raw_response", rawContent).Msg("Extracted recommendation from AI response")
	return match, nil
	// --- End extraction ---

}

// buildPrompt constructs the prompt for the AI based on resource details and the issue.
func buildPrompt(kind, name, region string, tags map[string]string, issue policy.ValidationIssue, examples []ResourceExample) string {
	var promptBuilder strings.Builder // Use strings.Builder

	promptBuilder.WriteString(fmt.Sprintf("Resource Details:\nKind: %s\nName: %s\nRegion: %s\n", kind, name, region))

	promptBuilder.WriteString("Existing Tags:\n")
	if len(tags) > 0 {
		for k, v := range tags {
			promptBuilder.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	} else {
		promptBuilder.WriteString("(No tags)\n")
	}

	promptBuilder.WriteString("\nTagging Issue Found:\n")
	promptBuilder.WriteString(fmt.Sprintf("- Type: %s\n", issue.IssueType))
	promptBuilder.WriteString(fmt.Sprintf("- Tag Key: %s\n", issue.TagKey))
	if issue.IssueType == "InvalidValue" {
		promptBuilder.WriteString(fmt.Sprintf("- Current Value: %s\n", issue.TagValue))
		promptBuilder.WriteString(fmt.Sprintf("- Allowed Values: %s\n", strings.Join(issue.AllowedValues, ", ")))
	}

	if len(examples) > 0 {
		promptBuilder.WriteString("\nExamples of well-tagged resources:\n")
		for _, ex := range examples {
			promptBuilder.WriteString(fmt.Sprintf("\n---\nKind: %s\nName: %s\nRegion: %s\nTags:\n", ex.Kind, ex.Name, ex.Region))
			for k, v := range ex.Tags {
				promptBuilder.WriteString(fmt.Sprintf("  - %s: %s\n", k, v))
			}
		}
		promptBuilder.WriteString("---\n")
	}

	promptBuilder.WriteString("\nBased on the resource details, the specific tagging issue, and the examples (if provided), suggest a fix in the format 'action:tag(key=value)' or 'action:tag(key)'.")

	return promptBuilder.String()
}
