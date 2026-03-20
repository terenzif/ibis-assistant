package search

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/deckonline/knowledge_mcp/internal/ai"
	"github.com/deckonline/knowledge_mcp/internal/logger"
)

// AgenticResult represents the outcome of an agentic search
type AgenticResult struct {
	Answer  string   `json:"answer"`
	Sources []Result `json:"sources"`
	Steps   []string `json:"reasoning_steps"`
}

// Pre-compiled regexes for performance
var (
	reFinalAnswer = regexp.MustCompile(`(?i)FINAL ANSWER:`)
	reSearch      = regexp.MustCompile(`(?i)SEARCH:`)
)

const SystemPrompt = `You are a Senior Software Engineer Agent.
Your goal is to answer questions about the codebase using the provided SEARCH tool.

PROTOCOL:
1. THOUGHT: Explain your reasoning. What do you need to know?
2. ACTION: If you need information, output "SEARCH: \"<query>\"". Always use double quotes for the query.
3. OBSERVATION: I will provide the search results.
4. REPEAT: You can search multiple times if needed.
5. FINAL ANSWER: When you have enough information, output "FINAL ANSWER: <your answer>".

GUIDELINES:
- Be precise.
- If the search results are not relevant, try a different query.
- If you cannot find the answer, admit it.
- Cite the file paths in your answer.`

// AskProjectAgentic performs a multi-step ReAct search
func (s *Service) AskProjectAgentic(ctx context.Context, query string) (*AgenticResult, error) {
	// Initialize History
	history := []ai.Content{
		{
			Role: "user",
			Parts: []ai.Part{
				{Text: SystemPrompt + "\n\nQuestion: " + query},
			},
		},
	}

	result := &AgenticResult{
		Steps: []string{},
	}

	uniqueSources := make(map[string]Result)

	maxSteps := 5
	for i := 0; i < maxSteps; i++ {
		// Call AI
		candidate, err := s.AI.GenerateContent(ctx, history, ai.GenerationConfig{
			Temperature:     0.2,
			MaxOutputTokens: 2000,
		})
		if err != nil {
			return nil, fmt.Errorf("AI generation failed: %w", err)
		}

		if candidate.UsageMetadata != nil && s.DB != nil {
			tracker := ai.NewCostTracker(s.DB)
			tracker.RecordUsage(ctx, "system:search_agentic", candidate.UsageMetadata.PromptTokenCount, candidate.UsageMetadata.CandidatesTokenCount)
		}

		response := ""
		for _, p := range candidate.Content.Parts {
			response += p.Text
		}

		// Store step
		result.Steps = append(result.Steps, response)

		// Add model response to history (ensure role is model)
		modelContent := candidate.Content
		if modelContent.Role == "" {
			modelContent.Role = "model"
		}
		history = append(history, modelContent)

		// Parse Response (Case Insensitive using Regex to be Unicode safe)
		locFinal := reFinalAnswer.FindStringIndex(response)
		locSearch := reSearch.FindStringIndex(response)

		// Determine which action to take (priority to first occurrence)
		action := "none"
		if locFinal != nil && locSearch != nil {
			if locFinal[0] < locSearch[0] {
				action = "final"
			} else {
				action = "search"
			}
		} else if locFinal != nil {
			action = "final"
		} else if locSearch != nil {
			action = "search"
		}

		if action == "final" {
			// locFinal[1] is the end index of the match
			answerPart := response[locFinal[1]:]
			result.Answer = strings.TrimSpace(answerPart)
			break
		} else if action == "search" {
			// Extract query using locSearch
			rawRest := response[locSearch[1]:]
			trimmedRest := strings.TrimSpace(rawRest)

			searchQuery := ""
			quotedExtracted := false
			// 1. Check for quotes at the start (supports multiline)
			if strings.HasPrefix(trimmedRest, "\"") || strings.HasPrefix(trimmedRest, "'") {
				quote := trimmedRest[0:1]
				// Find next quote
				endQuote := strings.Index(trimmedRest[1:], quote)
				if endQuote != -1 {
					searchQuery = trimmedRest[1 : 1+endQuote]
					quotedExtracted = true
				}
			}

			if !quotedExtracted && searchQuery == "" {
				// Fallback to line-based extraction if not quoted properly
				lineEnd := strings.Index(rawRest, "\n")
				var rawQuery string
				if lineEnd == -1 {
					rawQuery = strings.TrimSpace(rawRest)
				} else {
					rawQuery = strings.TrimSpace(rawRest[:lineEnd])
				}

				// Stop at FINAL ANSWER if present in the same line
				if loc := reFinalAnswer.FindStringIndex(rawQuery); loc != nil {
					rawQuery = strings.TrimSpace(rawQuery[:loc[0]])
				}

				// If it was a mismatched quote (started with quote but no end quote), strip the leading quote
				if strings.HasPrefix(rawQuery, "\"") || strings.HasPrefix(rawQuery, "'") {
					quote := rawQuery[0:1]
					searchQuery = strings.TrimPrefix(rawQuery, quote)
				} else {
					// 2. If no quotes, first look for sentence boundary (. ) to strip chatter
					if idx := strings.Index(rawQuery, ". "); idx != -1 {
						rawQuery = rawQuery[:idx]
					}
					// Also strip trailing punctuation
					searchQuery = strings.TrimRight(rawQuery, ".")
				}
			}

			searchQuery = strings.TrimSpace(searchQuery)

			if searchQuery == "" {
				logger.Info("Agentic Search Step %d: Skipping empty search query", i+1)
				history = append(history, ai.Content{Role: "user", Parts: []ai.Part{{Text: "OBSERVATION: Search query was empty. Please provide a valid search term."}}})
				continue
			}

			logger.Info("Agentic Search Step %d: Searching for '%s'", i+1, searchQuery)

			// Execute Search
			searchResults, err := s.AskProject(ctx, searchQuery)
			if err != nil {
				history = append(history, ai.Content{Role: "user", Parts: []ai.Part{{Text: fmt.Sprintf("OBSERVATION: Search failed: %v", err)}}})
				continue
			}

			// Collect sources
			for _, r := range searchResults {
				uniqueSources[r.ID] = r
			}

			// Format results for AI
			var obs strings.Builder
			obs.WriteString("OBSERVATION: Found the following files:\n")
			if len(searchResults) == 0 {
				obs.WriteString("No results found.\n")
			}
			for _, r := range searchResults {
				// Truncate content to save tokens
				contentSnippet := r.Content
				if len(contentSnippet) > 2000 {
					contentSnippet = contentSnippet[:2000] + "...(truncated)"
				}
				obs.WriteString(fmt.Sprintf("- File: %s (Score: %.2f)\nContext: %s\n", r.Path, r.Score, contentSnippet))

				if len(r.Context.RelatedIssues) > 0 {
					obs.WriteString("  Related Issues:\n")
					for _, issue := range r.Context.RelatedIssues {
						obs.WriteString(fmt.Sprintf("    - [%s] %s (Status: %s)\n", issue.ID, issue.Subject, issue.Status))
					}
				}
				if len(r.Context.Commits) > 0 {
					obs.WriteString("  Recent Commits:\n")
					for _, commit := range r.Context.Commits {
						hashShort := commit.Hash
						if len(hashShort) > 7 {
							hashShort = hashShort[:7]
						}
						obs.WriteString(fmt.Sprintf("    - [%s] %s (by %s)\n", hashShort, commit.Message, commit.Author))
					}
				}
				if len(r.Context.ExpertAuthors) > 0 {
					obs.WriteString(fmt.Sprintf("  Experts: %s\n", strings.Join(r.Context.ExpertAuthors, ", ")))
				}
				obs.WriteString("\n")
			}

			history = append(history, ai.Content{Role: "user", Parts: []ai.Part{{Text: obs.String()}}})
		} else {
			// No explicit action, nudge
			history = append(history, ai.Content{Role: "user", Parts: []ai.Part{{Text: "OBSERVATION: Please continue. Output SEARCH or FINAL ANSWER."}}})
		}
	}

	// Flatten sources
	for _, v := range uniqueSources {
		result.Sources = append(result.Sources, v)
	}

	if result.Answer == "" {
		result.Answer = "I could not find a definitive answer after multiple steps. Please try a more specific question."
	}

	return result, nil
}
